package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"github.com/dedis/cothority"
	"github.com/dedis/cothority/byzcoin"
	"github.com/dedis/cothority/byzcoin/bcadmin/lib"
	"github.com/dedis/cothority/byzcoin/contracts"
	"github.com/dedis/cothority/darc"
	"github.com/dedis/kyber"
	"github.com/dedis/kyber/util/key"
	"github.com/dedis/onet/cfgpath"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/dedis/protobuf"

	cli "gopkg.in/urfave/cli.v1"
)

func init() {
	network.RegisterMessages(&darc.Darc{}, &darc.Identity{}, &darc.Signer{})
}

var cmds = cli.Commands{
	{
		Name:      "join",
		Usage:     "joins a given byzcoin instance",
		ArgsUsage: "bc-xxx.cfg",
		Action:    join,
	},
	{
		Name:    "show",
		Usage:   "shows the account address and the balance",
		Aliases: []string{"s"},
		Action:  show,
	},
	{
		Name:      "transfer",
		Usage:     "transfer coins from your account to another one",
		ArgsUsage: "coins account",
		Action:    transfer,
	},
}

type Config struct {
	BCConfig lib.Config
	KeyPair  key.Pair
}

var cliApp = cli.NewApp()
var ConfigPath string

// getDataPath is a function pointer so that tests can hook and modify this.
var getDataPath = cfgpath.GetDataPath

func init() {
	cliApp.Name = "wallet"
	cliApp.Usage = "Handle wallet data"
	cliApp.Version = "0.1"
	cliApp.Commands = cmds
	cliApp.Flags = []cli.Flag{
		cli.IntFlag{
			Name:  "debug, d",
			Value: 0,
			Usage: "debug-level: 1 for terse, 5 for maximal",
		},
		cli.StringFlag{
			Name:  "config, c",
			Value: getDataPath(cliApp.Name),
			Usage: "path to configuration-directory",
		},
	}
	cliApp.Before = func(c *cli.Context) error {
		log.SetDebugVisible(c.Int("debug"))
		ConfigPath = c.String("config")
		return nil
	}
}

func main() {
	log.ErrFatal(cliApp.Run(os.Args))
}

func join(c *cli.Context) error {
	if c.NArg() < 1 {
		return errors.New("please give bc-xxx.cfg")
	}

	bcCfg, _, err := lib.LoadConfig(c.Args().First())
	if err != nil {
		return err
	}

	cfg := Config{
		BCConfig: bcCfg,
		KeyPair:  *key.NewKeyPair(cothority.Suite),
	}

	err = cfg.Save()
	if err != nil {
		return err
	}

	return show(c)
}

func show(c *cli.Context) error {
	cfg, cl, err := LoadConfig()
	if err != nil {
		return err
	}

	iid, err := coinHash(cfg.KeyPair.Public)
	if err != nil {
		return err
	}
	resp, err := cl.GetProof(iid.Slice())
	if err != nil {
		return err
	}
	var balance uint64
	if resp.Proof.InclusionProof.Match(iid.Slice()) {
		_, value, _, _, err := resp.Proof.KeyValue()
		if err != nil {
			return err
		}
		var coin byzcoin.Coin
		err = protobuf.Decode(value, &coin)
		if err != nil {
			return err
		}
		balance = coin.Value
	}
	log.Info("Public key is:", cfg.KeyPair.Public)
	log.Info("Coin-address is:", iid)
	log.Info("Balance is:", balance)
	return nil
}

func transfer(c *cli.Context) error {
	if c.NArg() < 2 {
		return errors.New("please give the following arguments: balance address")
	}
	amount, err := strconv.ParseUint(c.Args().First(), 10, 64)
	if err != nil {
		return err
	}

	target, err := hex.DecodeString(c.Args().Get(1))
	if err != nil {
		return err
	}

	cfg, cl, err := LoadConfig()
	if err != nil {
		return err
	}

	iid, err := coinHash(cfg.KeyPair.Public)
	if err != nil {
		return err
	}
	resp, err := cl.GetProof(iid.Slice())
	if err != nil {
		return err
	}
	var balance uint64
	if resp.Proof.InclusionProof.Match(iid.Slice()) {
		_, value, _, _, err := resp.Proof.KeyValue()
		if err != nil {
			return err
		}
		var coin byzcoin.Coin
		err = protobuf.Decode(value, &coin)
		if err != nil {
			return err
		}
		balance = coin.Value
	}
	if amount > balance {
		return errors.New("your account doesn't have enough coins in it")
	}

	signer := darc.NewSignerEd25519(cfg.KeyPair.Public, cfg.KeyPair.Private)
	counters, err := cl.GetSignerCounters(signer.Identity().String())
	counters.Counters[0]++
	amountBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(amountBuf, amount)
	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{
			{
				InstanceID: iid,
				Invoke: &byzcoin.Invoke{
					Command: "transfer",
					Args: byzcoin.Arguments{
						{
							Name:  "coins",
							Value: amountBuf,
						},
						{
							Name:  "destination",
							Value: target,
						},
					},
				},
				SignerCounter: counters.Counters,
			},
		},
	}
	ctx.SignWith(signer)
	ctx.InstructionsHash = ctx.Instructions.Hash()

	log.Info("Sending transaction of", amount, "coins to address", c.Args().Get(1))
	_, err = cl.AddTransactionAndWait(ctx, 10)
	if err != nil {
		return err
	}

	log.Info("Transaction succeeded")

	return nil
}

func coinHash(pub kyber.Point) (iid byzcoin.InstanceID, err error) {
	h := sha256.New()
	h.Write([]byte(contracts.ContractCoinID))
	buf, err := pub.MarshalBinary()
	if err != nil {
		return
	}
	h.Write(buf)
	iid = byzcoin.NewInstanceID(h.Sum(nil))
	return
}

const configName = "wallet.json"

// TODO: make json
func LoadConfig() (cfg Config, cl *byzcoin.Client, err error) {
	buf, err := ioutil.ReadFile(filepath.Join(ConfigPath, configName))
	if err != nil {
		return
	}
	err = protobuf.DecodeWithConstructors(buf, &cfg, network.DefaultConstructors(cothority.Suite))
	if err != nil {
		return
	}
	cl = byzcoin.NewClient(cfg.BCConfig.ByzCoinID, cfg.BCConfig.Roster)
	return
}

func (cfg Config) Save() error {
	buf, err := protobuf.Encode(&cfg)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filepath.Join(ConfigPath, configName), buf, 0600)
}
