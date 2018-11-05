package calypso

import (
	"errors"

	"github.com/dedis/cothority"
	"github.com/dedis/cothority/byzcoin"
	"github.com/dedis/cothority/darc"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/dedis/protobuf"
)

// ContractWriteID references a write contract system-wide.
var ContractWriteID = "calypsoWrite"

// contractWrite is used to store a secret in the ledger, so that an
// authorized reader can retrieve it by creating a Read-instance.
//
// Accepted Instructions:
//  - spawn:calypsoWrite creates a new write-request. TODO: verify the LTS exists
//  - spawn:calypsoRead creates a new read-request for this write-request.
func (s *Service) contractWrite(cdb byzcoin.ReadOnlyStateTrie, inst byzcoin.Instruction, ctxHash []byte, c []byzcoin.Coin) (sc []byzcoin.StateChange, cOut []byzcoin.Coin, err error) {
	cOut = c

	err = inst.Verify(cdb, ctxHash)
	if err != nil {
		return
	}

	var darcID darc.ID
	var contract string
	_, _, contract, darcID, err = cdb.GetValues(inst.InstanceID.Slice())
	if err != nil {
		return nil, nil, err
	}

	switch inst.GetType() {
	case byzcoin.SpawnType:
		switch contract {
		case ContractWriteID:
			// Spawn arrived on a write instance, so the request is to make a read instance.
			//
			// TODO: correctly handle multi signatures for read requests: to whom should the
			// secret be re-encrypted to? Perhaps for multi signatures we only want to have
			// ephemeral keys.
			r := inst.Spawn.Args.Search("read")
			if r == nil || len(r) == 0 {
				return nil, nil, errors.New("need a read argument")
			}
			var re Read
			err = protobuf.DecodeWithConstructors(r, &re, network.DefaultConstructors(cothority.Suite))
			if err != nil {
				return nil, nil, errors.New("passed read argument is invalid: " + err.Error())
			}

			var cid string
			_, _, cid, _, err = cdb.GetValues(re.Write.Slice())
			if err != nil {
				return nil, nil, errors.New("referenced write-id is not correct: " + err.Error())
			}
			if cid != ContractWriteID {
				return nil, nil, errors.New("referenced write-id is not a write instance, got " + cid)
			}

			sc = byzcoin.StateChanges{byzcoin.NewStateChange(byzcoin.Create, inst.DeriveID(""), contractReadID, r, darcID)}

			return
		case byzcoin.ContractDarcID:
			// This spawn arrived on another kind of instance (probably a Darc), so the request is to
			// make a Write.
			w := inst.Spawn.Args.Search("write")
			if w == nil || len(w) == 0 {
				return nil, nil, errors.New("need a write request in 'write' argument")
			}
			var wr Write
			err = protobuf.DecodeWithConstructors(w, &wr, network.DefaultConstructors(cothority.Suite))
			if err != nil {
				return nil, nil, errors.New("couldn't unmarshal write: " + err.Error())
			}
			if err = wr.CheckProof(cothority.Suite, darcID); err != nil {
				return nil, nil, errors.New("proof of write failed: " + err.Error())
			}
			instID := inst.DeriveID("")
			log.Lvlf3("Successfully verified write request and will store in %x", instID)
			sc = append(sc, byzcoin.NewStateChange(byzcoin.Create, instID, ContractWriteID, w, darcID))
			return
		default:
			err = errors.New("unexpected contract type")
			return
		}
	default:
		return nil, nil, errors.New("asked for something we cannot do")
	}
}

// contractReadID is used to mark instances that prove a reader has access to a
// given write instance. It is not a contract that can be called directly;
// instead instances with this contract ID are only ever created as a result of
// a Spawn on the targeted write instance.
var contractReadID = "calypsoRead"
