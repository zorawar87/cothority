package contracts

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/dedis/cothority/byzcoin"
	"github.com/dedis/cothority/byzcoin/trie"
	"github.com/dedis/cothority/darc"
	"github.com/dedis/cothority/darc/expression"
	"github.com/dedis/onet/log"
	"github.com/dedis/protobuf"
	"github.com/stretchr/testify/require"
)

var ciZero, ciOne, ciTwo []byte
var coinZero, coinOne, coinTwo []byte

func init() {
	ci := byzcoin.Coin{
		Name: CoinName,
	}
	var err error
	ciZero, err = protobuf.Encode(&ci)
	log.ErrFatal(err)
	ci.Value = 1
	ciOne, err = protobuf.Encode(&ci)
	log.ErrFatal(err)
	ci.Value = 2
	ciTwo, err = protobuf.Encode(&ci)
	log.ErrFatal(err)

	coinZero = make([]byte, 8)
	coinOne = make([]byte, 8)
	coinOne[0] = byte(1)
	coinTwo = make([]byte, 8)
	coinTwo[0] = byte(2)
}

func TestCoin_Spawn(t *testing.T) {
	// Testing spawning of a new coin and checking it has zero coins in it.
	ct := newCT("spawn:coin")
	ct.setSignatureCounter(gsigner.Identity().String(), 0)

	inst := byzcoin.Instruction{
		InstanceID: byzcoin.NewInstanceID(gdarc.GetBaseID()),
		Spawn: &byzcoin.Spawn{
			ContractID: ContractCoinID,
		},
		SignerCounter: []uint64{1},
	}
	dummyCtxHash := []byte("dummy_ctx_hash")
	err := inst.SignWith(dummyCtxHash, gsigner)
	require.Nil(t, err)

	// Spawn cannot use getContract because the instance ID is the genesis darc.
	// We need a zero contractCoin instead.
	c, _ := contractCoinFromBytes(nil)
	sc, co, err := c.Spawn(ct, inst, []byzcoin.Coin{})

	require.Nil(t, err)
	require.Equal(t, 1, len(sc))

	ca := inst.DeriveID("")
	require.Equal(t, byzcoin.NewStateChange(byzcoin.Create, ca,
		ContractCoinID, ciZero, gdarc.GetBaseID()), sc[0])
	require.Equal(t, 0, len(co))
}

func TestCoin_InvokeMint(t *testing.T) {
	// Test that a coin can be minted
	ct := newCT("invoke:mint")
	ct.setSignatureCounter(gsigner.Identity().String(), 0)

	coAddr := byzcoin.InstanceID{}
	ct.Store(coAddr, ciZero, ContractCoinID, gdarc.GetBaseID())

	inst := byzcoin.Instruction{
		InstanceID: coAddr,
		Invoke: &byzcoin.Invoke{
			Command: "mint",
			Args:    byzcoin.Arguments{{Name: "coins", Value: coinOne}},
		},
		SignerCounter: []uint64{1},
	}
	dummyCtxHash := []byte("dummy_ctx_hash")
	err := inst.SignWith(dummyCtxHash, gsigner)
	require.Nil(t, err)

	sc, co, err := ct.getContract(inst.InstanceID).Invoke(ct, inst, []byzcoin.Coin{})
	require.Nil(t, err)
	require.Equal(t, 0, len(co))
	require.Equal(t, 1, len(sc))
	require.Equal(t, byzcoin.NewStateChange(byzcoin.Update, coAddr, ContractCoinID, ciOne, gdarc.GetBaseID()),
		sc[0])
}

func TestCoin_InvokeOverflow(t *testing.T) {
	ci := byzcoin.Coin{
		Value: ^uint64(0),
	}
	ciBuf, err := protobuf.Encode(&ci)
	require.Nil(t, err)

	ct := newCT("invoke:mint")
	ct.setSignatureCounter(gsigner.Identity().String(), 0)

	coAddr := byzcoin.InstanceID{}
	ct.Store(coAddr, ciBuf, ContractCoinID, gdarc.GetBaseID())

	inst := byzcoin.Instruction{
		InstanceID: coAddr,
		Invoke: &byzcoin.Invoke{
			Command: "mint",
			Args:    byzcoin.Arguments{{Name: "coins", Value: coinOne}},
		},
		SignerCounter: []uint64{1},
	}
	dummyCtxHash := []byte("dummy_ctx_hash")
	require.Nil(t, inst.SignWith(dummyCtxHash, gsigner))

	sc, co, err := ct.getContract(inst.InstanceID).Invoke(ct, inst, []byzcoin.Coin{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "overflow")
	require.Equal(t, 0, len(co))
	require.Equal(t, 0, len(sc))
}

func TestCoin_InvokeStoreFetch(t *testing.T) {
	ct := newCT("invoke:store", "invoke:fetch")
	ct.setSignatureCounter(gsigner.Identity().String(), 0)

	coAddr := byzcoin.InstanceID{}
	ct.Store(coAddr, ciZero, ContractCoinID, gdarc.GetBaseID())

	inst := byzcoin.Instruction{
		InstanceID: coAddr,
		Invoke: &byzcoin.Invoke{
			Command: "store",
			Args:    nil,
		},
		SignerCounter: []uint64{1},
	}
	dummyCtxHash := []byte("dummy_ctx_hash")
	require.Nil(t, inst.SignWith(dummyCtxHash, gsigner))

	c1 := byzcoin.Coin{Name: CoinName, Value: 1}
	notOlCoin := iid("notOlCoin")
	c2 := byzcoin.Coin{Name: notOlCoin, Value: 1}

	sc, co, err := ct.getContract(inst.InstanceID).Invoke(ct, inst, []byzcoin.Coin{c1, c2})
	require.Nil(t, err)
	require.Equal(t, 1, len(co))
	require.Equal(t, co[0].Name, notOlCoin)
	require.Equal(t, 1, len(sc))
	require.Equal(t, byzcoin.NewStateChange(byzcoin.Update, coAddr, ContractCoinID, ciOne, gdarc.GetBaseID()),
		sc[0])

	inst = byzcoin.Instruction{
		InstanceID: coAddr,
		Invoke: &byzcoin.Invoke{
			Command: "fetch",
			Args:    byzcoin.Arguments{{Name: "coins", Value: coinOne}},
		},
		SignerCounter: []uint64{1},
	}
	require.Nil(t, inst.SignWith(dummyCtxHash, gsigner))

	// Try once with not enough coins available.
	sc, co, err = ct.getContract(inst.InstanceID).Invoke(ct, inst, nil)
	require.Error(t, err)

	// Apply the changes to the mock trie.
	ct.Store(coAddr, ciOne, ContractCoinID, gdarc.GetBaseID())

	sc, co, err = ct.getContract(inst.InstanceID).Invoke(ct, inst, nil)
	require.Nil(t, err)
	require.Equal(t, 1, len(co))
	require.Equal(t, co[0].Name, CoinName)
	require.Equal(t, uint64(1), co[0].Value)
	require.Equal(t, 1, len(sc))
	require.Equal(t, byzcoin.NewStateChange(byzcoin.Update, coAddr, ContractCoinID, ciZero, gdarc.GetBaseID()),
		sc[0])
}

func TestCoin_InvokeTransfer(t *testing.T) {
	// Test that a coin can be transferred
	ct := newCT("invoke:transfer")
	ct.setSignatureCounter(gsigner.Identity().String(), 0)

	coAddr1 := byzcoin.InstanceID{}
	one := make([]byte, 32)
	one[31] = 1
	coAddr2 := byzcoin.NewInstanceID(one)

	ct.Store(coAddr1, ciOne, ContractCoinID, gdarc.GetBaseID())
	ct.Store(coAddr2, ciZero, ContractCoinID, gdarc.GetBaseID())

	// First create an instruction where the transfer should fail
	inst := byzcoin.Instruction{
		InstanceID: coAddr2,
		Invoke: &byzcoin.Invoke{
			Command: "transfer",
			Args: byzcoin.Arguments{
				{Name: "coins", Value: coinOne},
				{Name: "destination", Value: coAddr1.Slice()},
			},
		},
		SignerCounter: []uint64{1},
	}

	dummyCtxHash := []byte("dummy_ctx_hash")
	require.Nil(t, inst.SignWith(dummyCtxHash, gsigner))
	sc, co, err := ct.getContract(inst.InstanceID).Invoke(ct, inst, []byzcoin.Coin{})
	require.Error(t, err)

	inst = byzcoin.Instruction{
		InstanceID: coAddr1,
		Invoke: &byzcoin.Invoke{
			Command: "transfer",
			Args: byzcoin.Arguments{
				{Name: "coins", Value: coinOne},
				{Name: "destination", Value: coAddr2.Slice()},
			},
		},
		SignerCounter: []uint64{1},
	}

	require.Nil(t, inst.SignWith(dummyCtxHash, gsigner))
	sc, co, err = ct.getContract(inst.InstanceID).Invoke(ct, inst, []byzcoin.Coin{})
	require.Nil(t, err)
	require.Equal(t, 0, len(co))
	require.Equal(t, 2, len(sc))
	require.Equal(t, byzcoin.NewStateChange(byzcoin.Update, coAddr2, ContractCoinID, ciOne, gdarc.GetBaseID()), sc[0])
	require.Equal(t, byzcoin.NewStateChange(byzcoin.Update, coAddr1, ContractCoinID, ciZero, gdarc.GetBaseID()), sc[1])
}

type cvTest struct {
	values      map[string][]byte
	contractIDs map[string]string
	darcIDs     map[string]darc.ID
	index       int
}

var gdarc *darc.Darc
var gsigner darc.Signer

func newCT(rStr ...string) *cvTest {
	ct := &cvTest{
		make(map[string][]byte),
		make(map[string]string),
		make(map[string]darc.ID),
		0,
	}
	gsigner = darc.NewSignerEd25519(nil, nil)
	rules := darc.InitRules([]darc.Identity{gsigner.Identity()},
		[]darc.Identity{gsigner.Identity()})
	for _, r := range rStr {
		rules.AddRule(darc.Action(r), expression.Expr(gsigner.Identity().String()))
	}
	gdarc = darc.NewDarc(rules, []byte{})
	dBuf, err := gdarc.ToProto()
	log.ErrFatal(err)
	ct.Store(byzcoin.NewInstanceID(gdarc.GetBaseID()), dBuf, "darc", gdarc.GetBaseID())
	return ct
}

func (ct *cvTest) Store(key byzcoin.InstanceID, value []byte, contractID string, darcID darc.ID) {
	k := string(key.Slice())
	ct.values[k] = value
	ct.contractIDs[k] = contractID
	ct.darcIDs[k] = darcID
	ct.index++
}
func (ct cvTest) GetValues(key []byte) (value []byte, version uint64, contractID string, darcID darc.ID, err error) {
	return ct.values[string(key)], 0, ct.contractIDs[string(key)], ct.darcIDs[string(key)], nil
}
func (ct cvTest) GetValue(key []byte) ([]byte, error) {
	return ct.values[string(key)], nil
}
func (ct cvTest) GetContractID(key []byte) (string, error) {
	return ct.contractIDs[string(key)], nil
}
func (ct cvTest) GetProof(key []byte) (*trie.Proof, error) {
	return nil, errors.New("not implemented")
}

func (ct cvTest) GetIndex() int {
	return ct.index
}

func (ct cvTest) setSignatureCounter(id string, v uint64) {
	key := sha256.Sum256([]byte("signercounter_" + id))
	verBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(verBuf, v)
	ct.values[string(key[:])] = verBuf
	ct.contractIDs[string(key[:])] = ""
	ct.darcIDs[string(key[:])] = darc.ID([]byte{})
}

func (ct cvTest) getContract(i byzcoin.InstanceID) byzcoin.Contract {
	c, err := contractCoinFromBytes(ct.values[string(i.Slice())])
	if err != nil {
		panic(err.Error())
	}
	return c
}
