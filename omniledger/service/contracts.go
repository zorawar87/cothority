package service

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/dedis/cothority/omniledger/darc"
	"github.com/dedis/onet/log"
	"github.com/dedis/protobuf"
)

// ZeroNonce is 32 bytes of zeroes.
var ZeroNonce = Nonce([32]byte{})

// ZeroSubID is 32 bytes of zeroes as a subid for storing Darcs.
var ZeroSubID = SubID([32]byte{})

// oneSubID is the subid for storing the omniledger config.
var oneSubID = SubID(func() [32]byte {
	var one [32]byte
	one[31] = 1
	return one
}())

// ZeroDarc is a DarcID with all zeroes.
var ZeroDarc = darc.ID(make([]byte, 32))

// GenesisReferenceID is 64 bytes of zeroes. Its value is a reference to the
// genesis-darc.
var GenesisReferenceID = InstanceID{ZeroDarc, ZeroSubID}

// ContractConfigID denotes a config-contract
var ContractConfigID = "config"

// ContractDarcID denotes a darc-contract
var ContractDarcID = "darc"

// CmdDarcEvolve is needed to evolve a darc.
var CmdDarcEvolve = "evolve"

// LoadConfigFromColl loads the configuration data from the collections.
func LoadConfigFromColl(coll CollectionView) (*ChainConfig, error) {
	// Find the genesis-darc ID.
	val, contract, err := getValueContract(coll, GenesisReferenceID.Slice())
	if err != nil {
		return nil, err
	}
	if string(contract) != ContractConfigID {
		return nil, errors.New("did not get " + ContractConfigID)
	}
	if len(val) != 32 {
		return nil, errors.New("value has a invalid length")
	}
	// Use the genesis-darc ID to create the config key and read the config.
	configID := InstanceID{
		DarcID: darc.ID(val),
		SubID:  oneSubID,
	}
	val, contract, err = getValueContract(coll, configID.Slice())
	if err != nil {
		return nil, err
	}
	if string(contract) != ContractConfigID {
		return nil, errors.New("did not get " + ContractConfigID)
	}
	config := ChainConfig{}
	err = protobuf.Decode(val, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// LoadBlockIntervalFromColl loads the block interval from the collections.
func LoadBlockIntervalFromColl(coll CollectionView) (time.Duration, error) {
	config, err := LoadConfigFromColl(coll)
	if err != nil {
		return defaultInterval, err
	}
	return config.BlockInterval, nil
}

// LoadDarcFromColl loads a darc which should be stored in key.
func LoadDarcFromColl(coll CollectionView, key []byte) (*darc.Darc, error) {
	rec, err := coll.Get(key).Record()
	if err != nil {
		return nil, err
	}
	vs, err := rec.Values()
	if err != nil {
		return nil, err
	}
	if len(vs) < 2 {
		return nil, errors.New("not enough records")
	}
	contractBuf, ok := vs[1].([]byte)
	if !ok {
		return nil, errors.New("can not cast value to byte slice")
	}
	if string(contractBuf) != "darc" {
		return nil, errors.New("expected contract to be darc but got: " + string(contractBuf))
	}
	darcBuf, ok := vs[0].([]byte)
	if !ok {
		return nil, errors.New("cannot cast value to byte slice")
	}
	d, err := darc.NewFromProtobuf(darcBuf)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ContractConfig can only be instantiated once per skipchain, and only for
// the genesis block.
func (s *Service) ContractConfig(cdb CollectionView, tx Instruction, coins []Coin) (sc []StateChange, c []Coin, err error) {
	c = coins
	if tx.GetType() != SpawnType {
		return nil, nil, errors.New("Config can only be spawned")
	}
	darcBuf := tx.Spawn.Args.Search("darc")
	d, err := darc.NewFromProtobuf(darcBuf)
	if err != nil {
		log.Error("couldn't decode darc")
		return
	}
	if len(d.Rules) == 0 {
		return nil, nil, errors.New("don't accept darc with empty rules")
	}
	if err = d.Verify(true); err != nil {
		log.Error("couldn't verify darc")
		return
	}

	// sanity check the block interval
	intervalBuf := tx.Spawn.Args.Search("block_interval")
	interval, _ := binary.Varint(intervalBuf)
	if interval == 0 {
		err = errors.New("block interval is zero")
		return
	}

	// create the config to be stored by state changes
	config := ChainConfig{
		BlockInterval: time.Duration(interval),
	}
	configBuf, err := protobuf.Encode(&config)
	if err != nil {
		return
	}

	return []StateChange{
		NewStateChange(Create, GenesisReferenceID, ContractConfigID, tx.InstanceID.DarcID),
		NewStateChange(Create, tx.InstanceID, ContractDarcID, darcBuf),
		NewStateChange(Create,
			InstanceID{
				DarcID: tx.InstanceID.DarcID,
				SubID:  oneSubID,
			}, ContractConfigID, configBuf),
	}, c, nil
}

// ContractDarc accepts the following instructions:
//   - Spawn - creates a new darc
//   - Invoke.Evolve - evolves an existing darc
func (s *Service) ContractDarc(coll CollectionView, tx Instruction,
	coins []Coin) ([]StateChange, []Coin, error) {
	switch {
	case tx.Spawn != nil:
		if tx.Spawn.ContractID == ContractDarcID {
			darcBuf := tx.Spawn.Args.Search("darc")
			d, err := darc.NewFromProtobuf(darcBuf)
			if err != nil {
				return nil, nil, errors.New("given darc could not be decoded: " + err.Error())
			}
			return []StateChange{
				NewStateChange(Create, InstanceID{d.GetBaseID(), ZeroSubID}, ContractDarcID, darcBuf),
			}, coins, nil
		}
		// TODO The code below will never get called because this
		// contract is used only when tx.Spawn.ContractID is "darc", so
		// the if statement above gets executed and this contract
		// returns. Why do we need this part, if we do, how should we
		// fix it?
		c, found := s.contracts[tx.Spawn.ContractID]
		if !found {
			return nil, nil, errors.New("couldn't find this contract type")
		}
		return c(coll, tx, coins)
	case tx.Invoke != nil:
		switch tx.Invoke.Command {
		case "evolve":
			darcBuf := tx.Invoke.Args.Search("darc")
			newD, err := darc.NewFromProtobuf(darcBuf)
			if err != nil {
				return nil, nil, err
			}
			oldD, err := LoadDarcFromColl(coll, InstanceID{newD.BaseID, ZeroSubID}.Slice())
			if err != nil {
				return nil, nil, err
			}
			if err := newD.SanityCheck(oldD); err != nil {
				return nil, nil, err
			}
			return []StateChange{
				NewStateChange(Update, tx.InstanceID, ContractDarcID, darcBuf),
			}, coins, nil
		default:
			return nil, nil, errors.New("invalid command: " + tx.Invoke.Command)
		}
	default:
		return nil, nil, errors.New("Only invoke and spawn are defined yet")
	}
}
