package service

import (
	"testing"

	"github.com/dedis/cothority"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
)

func TestRandHoundService(t *testing.T) {
	local := onet.NewTCPTest(cothority.Suite)
	num := 10
	groups := 2
	purpose := "service test run"
	interval := 5000
	nodes, roster, _ := local.GenTree(num, true)
	defer local.CloseAll()

	setupRequest := &SetupRequest{
		Roster:   roster,
		Groups:   groups,
		Purpose:  purpose,
		Interval: interval,
	}
	service := local.GetServices(nodes, randhoundService)[0].(*Service)

	_, err := service.Setup(setupRequest)
	log.ErrFatal(err, "service setup failed")

	randRequest := &RandRequest{}
	reply, err := service.Random(randRequest)
	log.ErrFatal(err, "service randomness request failed")

	log.Lvl1("randomness:", reply.R)
	log.Lvl1("transcript:", reply.T)
}
