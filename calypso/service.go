// Package calypso implements the LTS functionality of the Calypso paper. It
// implements both the access-control cothority and the secret management
// cothority. (1) The access-control cothority is implemented using ByzCoin
// with two contracts, `Write` and `Read` (2) The secret-management cothority
// uses an onet service with methods to set up a Long Term Secret (LTS)
// distributed key and to request a re-encryption
//
// For more details, see
// https://github.com/dedis/cothority/tree/master/calypso/README.md
package calypso

import (
	"errors"
	"time"

	"github.com/dedis/cothority"
	"github.com/dedis/cothority/byzcoin"
	"github.com/dedis/cothority/calypso/protocol"
	"github.com/dedis/cothority/darc"
	dkgprotocol "github.com/dedis/cothority/dkg/pedersen"
	"github.com/dedis/kyber"
	"github.com/dedis/kyber/share"
	dkg "github.com/dedis/kyber/share/dkg/pedersen"
	"github.com/dedis/kyber/util/key"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/dedis/protobuf"
)

// Used for tests
var calypsoID onet.ServiceID

// ServiceName of the secret-management part of Calypso.
var ServiceName = "Calypso"

// dkgTimeout is how long the system waits for the DKG to finish
const propagationTimeout = 10 * time.Second

func init() {
	var err error
	calypsoID, err = onet.RegisterNewService(ServiceName, newService)
	log.ErrFatal(err)
	network.RegisterMessages(&storage1{}, &vData{})
}

// Service is our calypso-service. It stores all created LTSs.
type Service struct {
	*onet.ServiceProcessor
	storage *storage1
}

// pubPoly is a serializable version of share.PubPoly
type pubPoly struct {
	B       kyber.Point
	Commits []kyber.Point
}

// vData is sent to all nodes when re-encryption takes place. If Ephemeral
// is non-nil, Signature needs to hold a valid signature from the reader
// in the Proof.
type vData struct {
	Proof     byzcoin.Proof
	Ephemeral kyber.Point
	Signature *darc.Signature
}

// CreateLTS takes as input a roster with a list of all nodes that should
// participate in the DKG. Every node will store its private key and wait for
// decryption requests. The LTSID should be the InstanceID.
func (s *Service) CreateLTS(cl *CreateLTS) (reply *CreateLTSReply, err error) {
	roster, instID, err := s.getLtsRoster(&cl.Proof)
	if err != nil {
		return nil, err
	}

	// NOTE: the roster stored in ByzCoin must have myself.
	tree := roster.GenerateNaryTreeWithRoot(len(roster.List), s.ServerIdentity())
	cfg := newLtsConfig{
		cl.Proof,
	}
	cfgBuf, err := protobuf.Encode(&cfg)
	if err != nil {
		return nil, err
	}
	pi, err := s.CreateProtocol(dkgprotocol.Name, tree)
	if err != nil {
		return nil, err
	}
	setupDKG := pi.(*dkgprotocol.Setup)
	setupDKG.Wait = true
	setupDKG.SetConfig(&onet.GenericConfig{Data: cfgBuf})
	setupDKG.KeyPair = key.NewKeyPair(cothority.Suite)
	if err := pi.Start(); err != nil {
		return nil, err
	}

	log.Lvl3("Started DKG-protocol - waiting for done", len(roster.List))
	select {
	case <-setupDKG.Finished:
		shared, dks, err := setupDKG.SharedSecret()
		if err != nil {
			return nil, err
		}
		reply = &CreateLTSReply{
			ByzCoinID:  cl.Proof.Latest.SkipChainID(),
			InstanceID: instID,
			X:          shared.X,
		}
		s.storage.Lock()
		s.storage.Shared[string(reply.Hash())] = shared
		s.storage.Polys[string(reply.Hash())] = &pubPoly{s.Suite().Point().Base(), dks.Commits}
		s.storage.Rosters[string(reply.Hash())] = roster
		s.storage.Replies[string(reply.Hash())] = reply
		s.storage.DKS[string(reply.Hash())] = dks
		s.storage.Unlock()
		s.save()
	case <-time.After(propagationTimeout):
		return nil, errors.New("new-dkg didn't finish in time")
	}
	return
}

// ReshareLTS starts a request to reshare the LTS. The new roster which holds
// the new secret shares must exist in the InstanceID specified by the request.
func (s *Service) ReshareLTS(req *ReshareLTS) (*ReshareLTSReply, error) {
	roster, instID, err := s.getLtsRoster(&req.Proof)
	if err != nil {
		return nil, err
	}

	// Check that we know the shared secret, otherwise don't do re-sharing
	s.storage.Lock()
	if s.storage.Shared[string(instID)] == nil || s.storage.DKS[string(instID)] == nil {
		s.storage.Unlock()
		return nil, errors.New("cannot start resharing without an LTS")
	}
	s.storage.Unlock()

	// NOTE: the roster stored in ByzCoin must have myself.
	tree := roster.GenerateNaryTreeWithRoot(len(roster.List), s.ServerIdentity())
	cfg := reshareLtsConfig{
		req.Proof,
	}
	cfgBuf, err := protobuf.Encode(&cfg)
	if err != nil {
		return nil, err
	}
	pi, err := s.CreateProtocol("reshare", tree)
	if err != nil {
		return nil, err
	}
	setupDKG := pi.(*dkgprotocol.Setup)
	setupDKG.Wait = true
	setupDKG.SetConfig(&onet.GenericConfig{Data: cfgBuf})
	if err := pi.Start(); err != nil {
		return nil, err
	}
	log.Lvl3("Started resharing DKG-protocol - waiting for done", len(roster.List))

	select {
	case <-setupDKG.Finished:
		shared, dks, err := setupDKG.SharedSecret()
		if err != nil {
			return nil, err
		}
		s.storage.Lock()
		// Check the secret shares are different
		if shared.V.Equal(s.storage.Shared[string(instID)].V) {
			s.storage.Unlock()
			return nil, errors.New("the reshared secret is the same")
		}
		// Check the public key remains the same
		if !shared.X.Equal(s.storage.Shared[string(instID)].X) {
			s.storage.Unlock()
			return nil, errors.New("the reshared public point is different")
		}
		s.storage.Shared[string(instID)] = shared
		s.storage.Polys[string(instID)] = &pubPoly{s.Suite().Point().Base(), dks.Commits}
		s.storage.Rosters[string(instID)] = roster
		s.storage.DKS[string(instID)] = dks
		s.storage.Unlock()
		s.save()
	case <-time.After(propagationTimeout):
		return nil, errors.New("resharing-dkg didn't finish in time")
	}

	return &ReshareLTSReply{}, nil
}

func (s *Service) getLtsRoster(proof *byzcoin.Proof) (*onet.Roster, []byte, error) {
	instanceID, buf, _, _, err := proof.KeyValue()
	if err != nil {
		return nil, nil, err
	}
	// TODO additional verification

	var ltsRoster onet.Roster
	err = protobuf.DecodeWithConstructors(buf, &ltsRoster, network.DefaultConstructors(cothority.Suite))
	if err != nil {
		return nil, nil, err
	}
	return &ltsRoster, instanceID, nil
}

// DecryptKey takes as an input a Read- and a Write-proof. Proofs contain
// everything necessary to verify that a given instance is correct and
// stored in ByzCoin.
// Using the Read and the Write-instance, this method verifies that the
// requests match and then re-encrypts the secret to the public key given
// in the Read-instance.
// TODO: support ephemeral keys.
func (s *Service) DecryptKey(dkr *DecryptKey) (reply *DecryptKeyReply, err error) {
	reply = &DecryptKeyReply{}
	log.Lvl2("Re-encrypt the key to the public key of the reader")

	var read Read
	if err := dkr.Read.VerifyAndDecode(cothority.Suite, ContractReadID, &read); err != nil {
		return nil, errors.New("didn't get a read instance: " + err.Error())
	}
	var write Write
	if err := dkr.Write.VerifyAndDecode(cothority.Suite, ContractWriteID, &write); err != nil {
		return nil, errors.New("didn't get a write instance: " + err.Error())
	}
	if !read.Write.Equal(byzcoin.NewInstanceID(dkr.Write.InclusionProof.Key())) {
		return nil, errors.New("read doesn't point to passed write")
	}
	s.storage.Lock()
	roster := s.storage.Rosters[string(write.LTSID)]
	if roster == nil {
		s.storage.Unlock()
		return nil, errors.New("don't know the LTSID stored in write")
	}
	scID := make([]byte, 32)
	copy(scID, s.storage.Replies[string(write.LTSID)].ByzCoinID)
	s.storage.Unlock()
	if err = dkr.Read.Verify(scID); err != nil {
		return nil, errors.New("read proof cannot be verified to come from scID: " + err.Error())
	}
	if err = dkr.Write.Verify(scID); err != nil {
		return nil, errors.New("write proof cannot be verified to come from scID: " + err.Error())
	}

	// Start ocs-protocol to re-encrypt the file's symmetric key under the
	// reader's public key.
	nodes := len(roster.List)
	threshold := nodes - (nodes-1)/3
	tree := roster.GenerateNaryTreeWithRoot(nodes, s.ServerIdentity())
	pi, err := s.CreateProtocol(protocol.NameOCS, tree)
	if err != nil {
		return nil, err
	}
	ocsProto := pi.(*protocol.OCS)
	ocsProto.U = write.U
	verificationData := &vData{
		Proof: dkr.Read,
	}
	ocsProto.Xc = read.Xc
	log.Lvlf2("Public key is: %s", ocsProto.Xc)
	ocsProto.VerificationData, err = protobuf.Encode(verificationData)
	if err != nil {
		return nil, errors.New("couldn't marshal verification data: " + err.Error())
	}

	// Make sure everything used from the s.Storage structure is copied, so
	// there will be no races.
	s.storage.Lock()
	ocsProto.Shared = s.storage.Shared[string(write.LTSID)]
	pp := s.storage.Polys[string(write.LTSID)]
	reply.X = s.storage.Shared[string(write.LTSID)].X.Clone()
	var commits []kyber.Point
	for _, c := range pp.Commits {
		commits = append(commits, c.Clone())
	}
	ocsProto.Poly = share.NewPubPoly(s.Suite(), pp.B.Clone(), commits)
	s.storage.Unlock()

	log.Lvl3("Starting reencryption protocol")
	ocsProto.SetConfig(&onet.GenericConfig{Data: write.LTSID})
	err = ocsProto.Start()
	if err != nil {
		return nil, err
	}
	if !<-ocsProto.Reencrypted {
		return nil, errors.New("reencryption got refused")
	}
	log.Lvl3("Reencryption protocol is done.")
	reply.XhatEnc, err = share.RecoverCommit(cothority.Suite, ocsProto.Uis,
		threshold, nodes)
	if err != nil {
		return nil, err
	}
	reply.Cs = write.Cs
	log.Lvl3("Successfully reencrypted the key")
	return
}

// GetLTSReply returns the CreateLTSReply message of a previous LTS.
func (s *Service) GetLTSReply(req *GetLTSReply) (*CreateLTSReply, error) {
	log.Lvl2("Getting shared public key")
	s.storage.Lock()
	reply, ok := s.storage.Replies[string(req.LTSID)]
	s.storage.Unlock()
	if !ok {
		return nil, errors.New("didn't find this Long Term Secret")
	}
	return &CreateLTSReply{
		ByzCoinID:  append([]byte{}, reply.ByzCoinID...),
		InstanceID: append([]byte{}, reply.InstanceID...),
		X:          reply.X.Clone(),
	}, nil
}

// NewProtocol intercepts the DKG and OCS protocols to retrieve the values
func (s *Service) NewProtocol(tn *onet.TreeNodeInstance, conf *onet.GenericConfig) (onet.ProtocolInstance, error) {
	log.Lvl3(s.ServerIdentity(), tn.ProtocolName(), conf)
	switch tn.ProtocolName() {
	case dkgprotocol.Name:
		var cfg newLtsConfig
		if err := protobuf.DecodeWithConstructors(conf.Data, &cfg, network.DefaultConstructors(cothority.Suite)); err != nil {
			return nil, err
		}

		pi, err := dkgprotocol.NewSetup(tn)
		if err != nil {
			return nil, err
		}
		setupDKG := pi.(*dkgprotocol.Setup)
		setupDKG.KeyPair = key.NewKeyPair(cothority.Suite)
		// TODO check proof that the roster is in ByzCoin
		// cfg.Verify()

		ltsID, _, _, _, err := cfg.KeyValue()
		if err != nil {
			return nil, err
		}
		go func(key []byte) {
			<-setupDKG.Finished
			shared, dks, err := setupDKG.SharedSecret()
			if err != nil {
				log.Error(err)
				return
			}
			log.Lvl3(s.ServerIdentity(), "Got shared", shared)
			s.storage.Lock()
			s.storage.Shared[string(key)] = shared
			s.storage.DKS[string(key)] = dks
			s.storage.LongtermPair[string(key)] = setupDKG.KeyPair
			s.storage.Unlock()
			s.save()
		}(ltsID)
		return pi, nil
	case "reshare":
		var cfg reshareLtsConfig
		if err := protobuf.DecodeWithConstructors(conf.Data, &cfg, network.DefaultConstructors(cothority.Suite)); err != nil {
			return nil, err
		}

		pi, err := dkgprotocol.NewSetup(tn)
		if err != nil {
			return nil, err
		}
		setupDKG := pi.(*dkgprotocol.Setup)

		// Setup configuration for doing the reshare protocol.
		ltsID, _, _, _, err := cfg.KeyValue()
		if err != nil {
			return nil, err
		}

		s.storage.Lock()
		if _, ok := s.storage.LongtermPair[string(ltsID)]; !ok {
			s.storage.Unlock()
			return nil, errors.New("cannot reshare uninitiated LTS")
		}
		c := &dkg.Config{
			Suite:    cothority.Suite,
			Longterm: s.storage.LongtermPair[string(ltsID)].Private,
			OldNodes: s.storage.Rosters[string(ltsID)].Publics(),
			NewNodes: cfg.Latest.Roster.Publics(),
			Share:    s.storage.DKS[string(ltsID)],
		}
		s.storage.Unlock()
		setupDKG.NewDKG = func() (*dkg.DistKeyGenerator, error) {
			return dkg.NewDistKeyHandler(c)
		}

		// Wait for DKG to end
		go func(key []byte) {
			<-setupDKG.Finished
			shared, dks, err := setupDKG.SharedSecret()
			if err != nil {
				log.Error(err)
				return
			}
			log.Lvl3(s.ServerIdentity(), "Got shared", shared)
			s.storage.Lock()
			// Check the secret shares are different
			if shared.V.Equal(s.storage.Shared[string(key)].V) {
				s.storage.Unlock()
				log.Error("the reshared secret is the same")
				return
			}
			// Check the public key remains the same
			if !shared.X.Equal(s.storage.Shared[string(key)].X) {
				s.storage.Unlock()
				log.Error("the reshared public point is different")
				return
			}
			s.storage.Shared[string(key)] = shared
			s.storage.DKS[string(key)] = dks
			s.storage.LongtermPair[string(key)] = setupDKG.KeyPair
			s.storage.Unlock()
			s.save()
		}(ltsID)
		return pi, nil
	case protocol.NameOCS:
		s.storage.Lock()
		shared, ok := s.storage.Shared[string(conf.Data)]
		s.storage.Unlock()
		if !ok {
			return nil, errors.New("didn't find skipchain")
		}
		pi, err := protocol.NewOCS(tn)
		if err != nil {
			return nil, err
		}
		ocs := pi.(*protocol.OCS)
		ocs.Shared = shared
		ocs.Verify = s.verifyReencryption
		return ocs, nil
	}
	return nil, nil
}

// verifyReencryption checks that the read and the write instances match.
func (s *Service) verifyReencryption(rc *protocol.Reencrypt) bool {
	err := func() error {
		var verificationData vData
		err := protobuf.DecodeWithConstructors(*rc.VerificationData, &verificationData, network.DefaultConstructors(cothority.Suite))
		if err != nil {
			return err
		}
		_, v0, contractID, _, err := verificationData.Proof.KeyValue()
		if err != nil {
			return errors.New("proof cannot return values: " + err.Error())
		}
		if contractID != ContractReadID {
			return errors.New("proof doesn't point to read instance")
		}
		var r Read
		err = protobuf.DecodeWithConstructors(v0, &r, network.DefaultConstructors(cothority.Suite))
		if err != nil {
			return errors.New("couldn't decode read data: " + err.Error())
		}
		if verificationData.Ephemeral != nil {
			return errors.New("ephemeral keys not supported yet")
		}
		if !r.Xc.Equal(rc.Xc) {
			return errors.New("wrong reader")
		}
		return nil
	}()
	if err != nil {
		log.Lvl2(s.ServerIdentity(), "wrong reencryption:", err)
		return false
	}
	return true
}

// newService receives the context that holds information about the node it's
// running on. Saving and loading can be done using the context. The data will
// be stored in memory for tests and simulations, and on disk for real deployments.
func newService(c *onet.Context) (onet.Service, error) {
	s := &Service{
		ServiceProcessor: onet.NewServiceProcessor(c),
	}
	if err := s.RegisterHandlers(s.CreateLTS, s.ReshareLTS, s.DecryptKey, s.GetLTSReply); err != nil {
		return nil, errors.New("couldn't register messages")
	}
	byzcoin.RegisterContract(c, ContractWriteID, s.ContractWrite)
	byzcoin.RegisterContract(c, ContractReadID, s.ContractRead)
	byzcoin.RegisterContract(c, ContractLongTermSecretID, s.ContractLongTermSecret)
	if err := s.tryLoad(); err != nil {
		log.Error(err)
		return nil, err
	}
	return s, nil
}
