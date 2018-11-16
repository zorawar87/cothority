package service

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/dedis/cothority"
	"github.com/dedis/cothority/byzcoin"
	"github.com/dedis/cothority/byzcoin/contracts"
	"github.com/dedis/cothority/darc"
	"github.com/dedis/cothority/darc/expression"
	"github.com/dedis/kyber"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/dedis/protobuf"
)

// This file holds the contracts for the pop-party. The following contracts
// are defined here:
//   - PopParty - holds the Configuration and later the FinalStatement
//   - PopCoinAccount - represents an account of popcoins

// ContractPopParty represents a pop-party that holds either a configuration
// or a final statement.
var ContractPopParty = "popParty"

// ContractPopCoinAccount holds popcoins of an attendee or a service.
var ContractPopCoinAccount = "popCoinAccount"

// PoPCoinName is the identifier of the popcoins.
var PoPCoinName byzcoin.InstanceID

func init() {
	h := sha256.New()
	h.Write([]byte("popcoin"))
	PoPCoinName = byzcoin.NewInstanceID(h.Sum(nil))
}

type contract struct {
	byzcoin.BasicContract
	PopPartyInstance
}

func contractPopParty(in []byte) (byzcoin.Contract, error) {
	c := &contract{}
	err := protobuf.DecodeWithConstructors(in, &c.PopPartyInstance, network.DefaultConstructors(cothority.Suite))
	if err != nil {
		return nil, errors.New("couldn't unmarshal existing PopPartyInstance: " + err.Error())
	}
	return c, nil
}

func (c *contract) Spawn(rst byzcoin.ReadOnlyStateTrie, inst byzcoin.Instruction, coins []byzcoin.Coin) (scs []byzcoin.StateChange, cout []byzcoin.Coin, err error) {
	cout = coins

	/*
		var darcID darc.ID
		var ppi PopPartyInstance
		if inst.Spawn == nil {
			var ppiBuf []byte
		} else {
			darcID = inst.InstanceID.Slice()
		}
		switch {
		case inst.Spawn != nil:
	*/

	fsBuf := inst.Spawn.Args.Search("FinalStatement")
	if fsBuf == nil {
		return nil, nil, errors.New("need FinalStatement argument")
	}
	c.State = 1

	var fs FinalStatement
	err = protobuf.DecodeWithConstructors(fsBuf, &fs, network.DefaultConstructors(cothority.Suite))
	if err != nil {
		return nil, nil, errors.New("couldn't unmarshal the final statement: " + err.Error())
	}
	c.FinalStatement = &fs

	ppiBuf, err := protobuf.Encode(c.PopPartyInstance)
	if err != nil {
		return nil, nil, errors.New("couldn't marshal PopPartyInstance: " + err.Error())
	}

	scs = byzcoin.StateChanges{
		byzcoin.NewStateChange(byzcoin.Create, inst.DeriveID(""), inst.Spawn.ContractID, ppiBuf, darc.ID(inst.InstanceID[:])),
	}
	return
}

func (c *contract) Invoke(rst byzcoin.ReadOnlyStateTrie, inst byzcoin.Instruction, coins []byzcoin.Coin) (scs []byzcoin.StateChange, cout []byzcoin.Coin, err error) {
	cout = coins

	var darcID darc.ID
	_, _, _, darcID, err = rst.GetValues(inst.InstanceID.Slice())
	if err != nil {
		return nil, nil, errors.New("couldn't get instance data: " + err.Error())
	}

	switch inst.Invoke.Command {
	case "Finalize":
		if c.State != 1 {
			return nil, nil, fmt.Errorf("can only finalize party with state 1, but current state is %d",
				c.State)
		}
		fsBuf := inst.Invoke.Args.Search("FinalStatement")
		if fsBuf == nil {
			return nil, nil, errors.New("missing argument: FinalStatement")
		}
		fs := FinalStatement{}
		err = protobuf.DecodeWithConstructors(fsBuf, &fs, network.DefaultConstructors(cothority.Suite))
		if err != nil {
			return nil, nil, errors.New("argument is not a valid FinalStatement")
		}

		// TODO: check for aggregate signature of all organizers
		ppi := PopPartyInstance{
			State:          2,
			FinalStatement: &fs,
		}

		for i, pub := range fs.Attendees {
			log.Lvlf3("Creating darc for attendee %d %s", i, pub)
			d, sc, err := createDarc(darcID, pub)
			if err != nil {
				return nil, nil, err
			}
			scs = append(scs, sc)

			sc, err = createCoin(inst, d, pub, 1000000)
			if err != nil {
				return nil, nil, err
			}
			scs = append(scs, sc)
		}

		// And add a service if the argument is given
		sBuf := inst.Invoke.Args.Search("Service")
		if sBuf != nil {
			ppi.Service = cothority.Suite.Point()
			err = ppi.Service.UnmarshalBinary(sBuf)
			if err != nil {
				return nil, nil, errors.New("couldn't unmarshal point: " + err.Error())
			}

			log.Lvlf3("Checking if service-darc and account for %s should be appended", ppi.Service)
			d, sc, err := createDarc(darcID, ppi.Service)
			if err != nil {
				return nil, nil, err
			}
			_, _, _, _, err = rst.GetValues(d.GetBaseID())
			if err != nil {
				log.Lvl2("Appending service-darc because it doesn't exist yet")
				scs = append(scs, sc)
			}

			log.Lvl3("Creating coin account for service")
			sc, err = createCoin(inst, d, ppi.Service, 0)
			if err != nil {
				return nil, nil, err
			}

			scs = append(scs, sc)
		}

		ppiBuf, err := protobuf.Encode(&ppi)
		if err != nil {
			return nil, nil, errors.New("couldn't marshal PopPartyInstance: " + err.Error())
		}

		// Update existing final statement
		scs = append(scs, byzcoin.NewStateChange(byzcoin.Update, inst.InstanceID, ContractPopParty, ppiBuf, darcID))

		return scs, coins, nil
	case "AddParty":
		return nil, nil, errors.New("not yet implemented")
	default:
		return nil, nil, errors.New("can only finalize Pop-party contract")
	}
}

func createDarc(darcID darc.ID, pub kyber.Point) (d *darc.Darc, sc byzcoin.StateChange, err error) {
	id := darc.NewIdentityEd25519(pub)
	rules := darc.InitRules([]darc.Identity{id}, []darc.Identity{id})
	rules.AddRule(darc.Action("invoke:transfer"), expression.Expr(id.String()))
	d = darc.NewDarc(rules, []byte("Attendee darc for pop-party"))
	darcBuf, err := d.ToProto()
	if err != nil {
		err = errors.New("couldn't marshal darc: " + err.Error())
		return
	}
	log.Lvlf3("Final %x/%x", d.GetBaseID(), sha256.Sum256(darcBuf))
	sc = byzcoin.NewStateChange(byzcoin.Create, byzcoin.NewInstanceID(d.GetBaseID()),
		byzcoin.ContractDarcID, darcBuf, darcID)
	return
}

func createCoin(inst byzcoin.Instruction, d *darc.Darc, pub kyber.Point, balance uint64) (sc byzcoin.StateChange, err error) {
	iid := sha256.New()
	iid.Write(inst.InstanceID.Slice())
	pubBuf, err := pub.MarshalBinary()
	if err != nil {
		err = errors.New("couldn't marshal public key: " + err.Error())
		return
	}
	iid.Write(pubBuf)
	cci := byzcoin.Coin{
		Name:  PoPCoinName,
		Value: balance,
	}
	cciBuf, err := protobuf.Encode(&cci)
	if err != nil {
		err = errors.New("couldn't encode CoinInstance: " + err.Error())
		return
	}
	coinID := iid.Sum(nil)
	log.Lvlf3("Creating account %x", coinID)
	return byzcoin.NewStateChange(byzcoin.Create, byzcoin.NewInstanceID(coinID),
		contracts.ContractCoinID, cciBuf, d.GetBaseID()), nil
}
