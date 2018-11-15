package pedersen

import (
	"errors"
	"fmt"

	"github.com/dedis/cothority"
	"github.com/dedis/kyber"
	"github.com/dedis/kyber/util/key"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"

	dkgpedersen "github.com/dedis/kyber/share/dkg/pedersen"
)

// Name is the protocol identifier string.
const Name = "Pedersen_DKG"

func init() {
	onet.GlobalProtocolRegister(Name, NewSetup)
}

// Setup can give the DKG that can be used to get the shared public key.
type Setup struct {
	*onet.TreeNodeInstance
	DKG       *dkgpedersen.DistKeyGenerator
	Threshold uint32
	Finished  chan bool
	Wait      bool
	NewDKG    func() (*dkgpedersen.DistKeyGenerator, error)

	// KeyPair must be set by the caller, if this is a new DKG, then simply
	// generate a new KeyPair.
	KeyPair *key.Pair

	nodes   []*onet.TreeNode
	publics []kyber.Point

	structStartDeal chan structStartDeal
	structDeal      chan structDeal
	structResponse  chan structResponse
	structWaitSetup chan structWaitSetup
	structWaitReply chan []structWaitReply
}

// NewSetup initialises the structure for use in one round
func NewSetup(n *onet.TreeNodeInstance) (onet.ProtocolInstance, error) {
	o := &Setup{
		TreeNodeInstance: n,
		Finished:         make(chan bool, 1),
		Threshold:        uint32(len(n.Roster().List) - (len(n.Roster().List)-1)/3),
		nodes:            n.List(),
	}

	err := o.RegisterHandlers(o.childInit, o.rootStartDeal)
	if err != nil {
		return nil, err
	}
	err = o.RegisterChannels(&o.structStartDeal, &o.structDeal, &o.structResponse,
		&o.structWaitSetup, &o.structWaitReply)
	if err != nil {
		return nil, err
	}
	o.publics = make([]kyber.Point, len(o.nodes))
	return o, nil
}

// SharedSecret returns the necessary information for doing shared
// encryption and decryption.
func (o *Setup) SharedSecret() (*SharedSecret, *dkgpedersen.DistKeyShare, error) {
	return NewSharedSecret(o.DKG)
}

// NewSharedSecret takes an initialized DistKeyGenerator and returns the
// minimal set of values necessary to do shared encryption/decryption.
func NewSharedSecret(gen *dkgpedersen.DistKeyGenerator) (*SharedSecret, *dkgpedersen.DistKeyShare, error) {
	if gen == nil {
		return nil, nil, errors.New("no valid dkg given")
	}
	dks, err := gen.DistKeyShare()
	if err != nil {
		return nil, nil, err
	}
	return &SharedSecret{
		Index:   dks.Share.I,
		V:       dks.Share.V,
		X:       dks.Public(),
		Commits: dks.Commits,
	}, dks, nil
}

// Start sends the Announce-message to all children
func (o *Setup) Start() error {
	log.Lvl3("Starting Protocol")
	// 1a - root asks children to send their public key
	errs := o.Broadcast(&Init{Wait: o.Wait})
	if len(errs) != 0 {
		return fmt.Errorf("broadcast failed with error(s): %v", errs)
	}
	return nil
}

// Dispatch takes care for channel-messages that need to be treated in the correct order.
func (o *Setup) Dispatch() error {
	defer o.Done()
	err := o.allStartDeal(<-o.structStartDeal)
	if err != nil {
		return err
	}
	for range o.publics[1:] {
		err := o.allDeal(<-o.structDeal)
		if err != nil {
			return err
		}
	}
	l := len(o.publics)
	for i := 0; i < l*(l-1); i++ {
		// This is expected to return some errors, so do not stop on them.
		err := o.allResponse(<-o.structResponse)
		if err != nil && err.Error() != "vss: already existing response from same origin" {
			return err
		}
	}

	if o.Wait {
		if o.IsRoot() {
			o.SendToChildren(&WaitSetup{})
			<-o.structWaitReply
		} else {
			<-o.structWaitSetup
			o.SendToParent(&WaitReply{})
		}
	}

	if !o.DKG.Certified() {
		return errors.New("not certified")
	}

	o.Finished <- true
	return nil
}

// Children reactions
func (o *Setup) childInit(i structInit) error {
	o.Wait = i.Wait
	log.Lvl3(o.Name(), o.Wait)
	return o.SendToParent(&InitReply{Public: o.KeyPair.Public})
}

// Root-node messages
func (o *Setup) rootStartDeal(replies []structInitReply) error {
	log.Lvl3(o.Name(), replies)
	o.publics[0] = o.KeyPair.Public
	for _, r := range replies {
		index, _ := o.Roster().Search(r.ServerIdentity.ID)
		if index < 0 {
			return errors.New("unknown serverIdentity")
		}
		o.publics[index] = r.Public
	}
	return o.fullBroadcast(&StartDeal{
		Publics:   o.publics,
		Threshold: o.Threshold,
	})
}

// Messages for both
func (o *Setup) allStartDeal(ssd structStartDeal) error {
	log.Lvl3(o.Name(), "received startDeal from:", ssd.ServerIdentity)
	var err error
	if o.NewDKG == nil {
		o.DKG, err = dkgpedersen.NewDistKeyGenerator(cothority.Suite, o.KeyPair.Private,
			ssd.Publics, int(ssd.Threshold))
	} else {
		o.DKG, err = o.NewDKG()
	}
	if err != nil {
		return err
	}
	o.publics = ssd.Publics
	deals, err := o.DKG.Deals()
	if err != nil {
		return err
	}
	log.Lvl3(o.Name(), "sending out deals", len(deals))
	for i, d := range deals {
		if err := o.SendTo(o.nodes[i], &Deal{d}); err != nil {
			return err
		}
	}
	return nil
}

func (o *Setup) allDeal(sd structDeal) error {
	log.Lvl3(o.Name(), sd.ServerIdentity)
	resp, err := o.DKG.ProcessDeal(sd.Deal.Deal)
	if err != nil {
		log.Error(o.Name(), err)
		return err
	}
	return o.fullBroadcast(&Response{resp})
}

func (o *Setup) allResponse(resp structResponse) error {
	log.Lvl3(o.Name(), resp.ServerIdentity)
	just, err := o.DKG.ProcessResponse(resp.Response.Response)
	if err != nil {
		return err
	}
	if just != nil {
		log.Warn(o.Name(), "Got a justification: ", just)
	}
	return nil
}

// Convenience functions
func (o *Setup) fullBroadcast(msg interface{}) error {
	errs := o.Multicast(msg, o.nodes...)
	if len(errs) != 0 {
		return fmt.Errorf("multicast failed with error(s): %v", errs)
	}
	return nil
}
