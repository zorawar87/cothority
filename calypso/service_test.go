package calypso

import (
	"testing"
	"time"

	"github.com/dedis/cothority"
	"github.com/dedis/cothority/byzcoin"
	"github.com/dedis/cothority/darc"
	"github.com/dedis/kyber"
	"github.com/dedis/kyber/util/key"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
	"github.com/dedis/protobuf"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.MainTest(m)
}

// TestService_CreateLTS runs the DKG protocol on the service and check that we
// get back valid results.
func TestService_CreateLTS(t *testing.T) {
	for _, nodes := range []int{4, 7} {
		func(nodes int) {
			if nodes > 5 && testing.Short() {
				log.Info("skipping, dkg might take too long for", nodes)
				return
			}
			s := newTS(t, nodes)
			defer s.closeAll(t)
			require.NotNil(t, s.ltsReply.ByzCoinID)
			require.NotNil(t, s.ltsReply.InstanceID)
			require.NotNil(t, s.ltsReply.X)
		}(nodes)
	}
}

func TestService_ReshareLTS(t *testing.T) {
	for _, nodes := range []int{4, 7} {
		func(nodes int) {
			if nodes > 5 && testing.Short() {
				log.Info("skipping, dkg might take too long for", nodes)
				return
			}
			s := newTS(t, nodes)
			defer s.closeAll(t)
			require.NotNil(t, s.ltsReply.ByzCoinID)
			require.NotNil(t, s.ltsReply.InstanceID)
			require.NotNil(t, s.ltsReply.X)

			// Create a new roster that has one fewer node that
			// before
			newRoster := onet.NewRoster(s.roster.List[1:])
			rosterBuf, err := protobuf.Encode(newRoster)
			require.NoError(t, err)

			ctx := byzcoin.ClientTransaction{
				Instructions: []byzcoin.Instruction{
					{
						InstanceID: byzcoin.NewInstanceID(s.ltsReply.InstanceID),
						Invoke: &byzcoin.Invoke{
							Command: "reshare",
							Args: []byzcoin.Argument{
								{
									Name:  "roster",
									Value: rosterBuf,
								},
							},
						},
						SignerCounter: []uint64{2},
					},
				},
			}
			require.Nil(t, ctx.SignWith(s.signer))
			_, err = s.cl.AddTransactionAndWait(ctx, 4)
			require.NoError(t, err)

			// Get the proof and start resharing
			proof, err := s.cl.GetProof(s.ltsReply.InstanceID)
			require.NoError(t, err)
			s.services[0].ReshareLTS(&ReshareLTS{proof.Proof})
		}(nodes)
	}
}

// TestContract_Write creates a write request and check that it gets stored.
func TestContract_Write(t *testing.T) {
	s := newTS(t, 5)
	defer s.closeAll(t)

	pr := s.addWriteAndWait(t, []byte("secret key"))
	require.Nil(t, pr.Verify(s.gbReply.Skipblock.Hash))
}

// TestContract_Write_Benchmark makes many write requests transactions and logs
// the transaction per second.
func TestContract_Write_Benchmark(t *testing.T) {
	if testing.Short() {
		t.Skip("running benchmark takes too long and it's extremely CPU intensive (100% CPU usage)")
	}

	s := newTS(t, 5)
	defer s.closeAll(t)

	totalTrans := 10
	var times []time.Duration

	var ctr uint64 = 1
	for i := 0; i < 50; i++ {
		iids := make([]byzcoin.InstanceID, totalTrans)
		start := time.Now()
		for i := 0; i < totalTrans; i++ {
			iids[i] = s.addWrite(t, []byte("secret key"), ctr)
			ctr++
		}
		timeSend := time.Now().Sub(start)
		log.Lvlf1("Time to send %d writes to the ledger: %s", totalTrans, timeSend)
		start = time.Now()
		for i := 0; i < totalTrans; i++ {
			s.waitInstID(t, iids[i])
		}
		timeWait := time.Now().Sub(start)
		log.Lvlf1("Time to wait for %d writes in the ledger: %s", totalTrans, timeWait)
		times = append(times, timeSend+timeWait)
		for _, ti := range times {
			log.Lvlf1("Total time: %s - tps: %f", ti,
				float64(totalTrans)/ti.Seconds())
		}
	}
}

// TestContract_Read makes a write requests and a corresponding read request
// which should be created from the write instance.
func TestContract_Read(t *testing.T) {
	s := newTS(t, 5)
	defer s.closeAll(t)

	prWrite := s.addWriteAndWait(t, []byte("secret key"))
	pr := s.addReadAndWait(t, prWrite, s.signer.Ed25519.Point)
	require.Nil(t, pr.Verify(s.gbReply.Skipblock.Hash))
}

// TestService_DecryptKey is an end-to-end test that logs two write and read
// requests and make sure that we can decrypt the secret afterwards.
func TestService_DecryptKey(t *testing.T) {
	s := newTS(t, 5)
	defer s.closeAll(t)

	key1 := []byte("secret key 1")
	prWr1 := s.addWriteAndWait(t, key1)
	prRe1 := s.addReadAndWait(t, prWr1, s.signer.Ed25519.Point)
	key2 := []byte("secret key 2")
	prWr2 := s.addWriteAndWait(t, key2)
	prRe2 := s.addReadAndWait(t, prWr2, s.signer.Ed25519.Point)

	_, err := s.services[0].DecryptKey(&DecryptKey{Read: *prRe1, Write: *prWr2})
	require.NotNil(t, err)
	_, err = s.services[0].DecryptKey(&DecryptKey{Read: *prRe2, Write: *prWr1})
	require.NotNil(t, err)

	dk1, err := s.services[0].DecryptKey(&DecryptKey{Read: *prRe1, Write: *prWr1})
	require.Nil(t, err)
	require.True(t, dk1.X.Equal(s.ltsReply.X))
	keyCopy1, err := DecodeKey(cothority.Suite, s.ltsReply.X, dk1.Cs, dk1.XhatEnc, s.signer.Ed25519.Secret)
	require.Nil(t, err)
	require.Equal(t, key1, keyCopy1)

	dk2, err := s.services[0].DecryptKey(&DecryptKey{Read: *prRe2, Write: *prWr2})
	require.Nil(t, err)
	require.True(t, dk2.X.Equal(s.ltsReply.X))
	keyCopy2, err := DecodeKey(cothority.Suite, s.ltsReply.X, dk2.Cs, dk2.XhatEnc, s.signer.Ed25519.Secret)
	require.Nil(t, err)
	require.Equal(t, key2, keyCopy2)
}

// TestService_DecryptEphemeralKey requests a read to a different key than the
// readers.
func TestService_DecryptEphemeralKey(t *testing.T) {
	s := newTS(t, 5)
	defer s.closeAll(t)

	ephemeral := key.NewKeyPair(cothority.Suite)

	key1 := []byte("secret key 1")
	prWr1 := s.addWriteAndWait(t, key1)
	prRe1 := s.addReadAndWait(t, prWr1, ephemeral.Public)

	dk1, err := s.services[0].DecryptKey(&DecryptKey{Read: *prRe1, Write: *prWr1})
	require.Nil(t, err)
	require.True(t, dk1.X.Equal(s.ltsReply.X))

	keyCopy1, err := DecodeKey(cothority.Suite, s.ltsReply.X, dk1.Cs, dk1.XhatEnc, ephemeral.Private)
	require.Nil(t, err)
	require.Equal(t, key1, keyCopy1)
}

type ts struct {
	local      *onet.LocalTest
	servers    []*onet.Server
	services   []*Service
	roster     *onet.Roster
	ltsReply   *CreateLTSReply
	signer     darc.Signer
	cl         *byzcoin.Client
	gbReply    *byzcoin.CreateGenesisBlockResponse
	genesisMsg *byzcoin.CreateGenesisBlock
	gDarc      *darc.Darc
}

func (s *ts) addRead(t *testing.T, write *byzcoin.Proof, Xc kyber.Point, ctr uint64) byzcoin.InstanceID {
	var readBuf []byte
	read := &Read{
		Write: byzcoin.NewInstanceID(write.InclusionProof.Key()),
		Xc:    Xc,
	}
	var err error
	readBuf, err = protobuf.Encode(read)
	require.Nil(t, err)
	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{{
			InstanceID: byzcoin.NewInstanceID(write.InclusionProof.Key()),
			Spawn: &byzcoin.Spawn{
				ContractID: ContractReadID,
				Args:       byzcoin.Arguments{{Name: "read", Value: readBuf}},
			},
			SignerCounter: []uint64{ctr},
		}},
	}
	require.Nil(t, ctx.SignWith(s.signer))
	_, err = s.cl.AddTransaction(ctx)
	require.Nil(t, err)
	return ctx.Instructions[0].DeriveID("")
}

func (s *ts) addReadAndWait(t *testing.T, write *byzcoin.Proof, Xc kyber.Point) *byzcoin.Proof {
	ctr, err := s.cl.GetSignerCounters(s.signer.Identity().String())
	require.NoError(t, err)
	instID := s.addRead(t, write, Xc, ctr.Counters[0]+1)
	return s.waitInstID(t, instID)
}

func newTS(t *testing.T, nodes int) ts {
	s := ts{}
	s.local = onet.NewLocalTestT(cothority.Suite, t)

	// Create the service
	s.servers, s.roster, _ = s.local.GenTree(nodes, true)
	services := s.local.GetServices(s.servers, calypsoID)
	for _, ser := range services {
		s.services = append(s.services, ser.(*Service))
	}

	// Create the skipchain
	s.signer = darc.NewSignerEd25519(nil, nil)
	s.createGenesis(t)

	// Create LTS instance
	rosterBuf, err := protobuf.Encode(s.roster)
	require.NoError(t, err)
	inst := byzcoin.Instruction{
		InstanceID: byzcoin.NewInstanceID(s.gDarc.GetBaseID()),
		Spawn: &byzcoin.Spawn{
			ContractID: ContractLongTermSecretID,
			Args: []byzcoin.Argument{
				{
					Name:  "roster",
					Value: rosterBuf,
				},
			},
		},
		SignerCounter: []uint64{1},
	}
	tx := byzcoin.ClientTransaction{
		Instructions: []byzcoin.Instruction{inst},
	}
	require.NoError(t, tx.SignWith(s.signer))
	_, err = s.cl.AddTransactionAndWait(tx, 4)
	require.NoError(t, err)

	// Get the proof
	resp, err := s.cl.GetProof(tx.Instructions[0].DeriveID("").Slice())
	require.NoError(t, err)

	// Start DKG
	s.ltsReply, err = s.services[0].CreateLTS(&CreateLTS{
		Proof: resp.Proof,
	})
	require.Nil(t, err)

	return s
}

func (s *ts) createGenesis(t *testing.T) {
	var err error
	s.genesisMsg, err = byzcoin.DefaultGenesisMsg(byzcoin.CurrentVersion, s.roster,
		[]string{"spawn:" + ContractWriteID,
			"spawn:" + ContractReadID,
			"spawn:" + ContractLongTermSecretID,
			"invoke:" + "reshare"},
		s.signer.Identity())
	require.Nil(t, err)
	s.gDarc = &s.genesisMsg.GenesisDarc
	s.genesisMsg.BlockInterval = time.Second

	s.cl, s.gbReply, err = byzcoin.NewLedger(s.genesisMsg, false)
	require.Nil(t, err)
}

func (s *ts) waitInstID(t *testing.T, instID byzcoin.InstanceID) *byzcoin.Proof {
	var err error
	var pr *byzcoin.Proof
	for i := 0; i < 10; i++ {
		pr, err = s.cl.WaitProof(instID, s.genesisMsg.BlockInterval, nil)
		if err == nil {
			require.Nil(t, pr.Verify(s.gbReply.Skipblock.Hash))
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		require.Fail(t, "didn't find proof")
	}
	return pr
}

func (s *ts) addWriteAndWait(t *testing.T, key []byte) *byzcoin.Proof {
	ctr, err := s.cl.GetSignerCounters(s.signer.Identity().String())
	require.NoError(t, err)

	instID := s.addWrite(t, key, ctr.Counters[0]+1)
	return s.waitInstID(t, instID)
}

func (s *ts) addWrite(t *testing.T, key []byte, ctr uint64) byzcoin.InstanceID {
	write := NewWrite(cothority.Suite, s.ltsReply.Hash(), s.gDarc.GetBaseID(), s.ltsReply.X, key)
	writeBuf, err := protobuf.Encode(write)
	require.Nil(t, err)

	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{{
			InstanceID: byzcoin.NewInstanceID(s.gDarc.GetBaseID()),
			Spawn: &byzcoin.Spawn{
				ContractID: ContractWriteID,
				Args:       byzcoin.Arguments{{Name: "write", Value: writeBuf}},
			},
			SignerCounter: []uint64{ctr},
		}},
	}
	require.Nil(t, ctx.SignWith(s.signer))
	_, err = s.cl.AddTransaction(ctx)
	require.Nil(t, err)
	return ctx.Instructions[0].DeriveID("")
}

func (s *ts) closeAll(t *testing.T) {
	require.Nil(t, s.cl.Close())
	s.local.CloseAll()
}
