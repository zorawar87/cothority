package service

import (
	"errors"

	"github.com/dedis/cothority/skipchain"
	"github.com/dedis/onet"
	"github.com/dedis/onet/network"
)

// How many msec to wait before a timeout is generated in the propagation.
const propagateTimeout = 10000

// How often we save the skipchains - in seconds.
const timeBetweenSave = 0

func init() {
	network.RegisterMessages(
		// Request updated block
		&GetBlock{},
		// Reply with updated block
		&GetBlockReply{},
		// Own service
		&Service{})
}

// GetService makes it possible to give either an `onet.Context` or
// `onet.Server` to `RegisterVerification`.
type GetService interface {
	Service(name string) onet.Service
}

// RegisterVerification stores the verification in a map and will
// call it whenever a verification needs to be done.
func RegisterVerification(s GetService, v skipchain.VerifierID, f skipchain.SkipBlockVerifier) error {
	scs := s.Service(ServiceName)
	if scs == nil {
		return errors.New("Didn't find our service: " + ServiceName)
	}
	return scs.(*Service).RegisterVerification(v, f)
}

// Internal calls

// GetBlock asks for an updated block, in case for a conode that is not
// in the roster-list of that block.
type GetBlock struct {
	ID skipchain.SkipBlockID
}

// PropagateSkipBlock sends a newly signed SkipBlock to all members of
// the Cothority
type PropagateSkipBlock struct {
	SkipBlock *skipchain.SkipBlock
}

// GetBlockReply returns the requested block.
type GetBlockReply struct {
	SkipBlock *skipchain.SkipBlock
}
