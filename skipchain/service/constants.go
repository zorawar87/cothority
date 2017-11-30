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
		&Service{},
		// - Internal calls
		// Propagation
		&PropagateSkipBlocks{},
		// Request forward-signature
		&ForwardSignature{},
	)
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

// PropagateSkipBlocks sends a newly signed SkipBlock to all members of
// the Cothority
type PropagateSkipBlocks struct {
	SkipBlocks []*skipchain.SkipBlock
}

// GetBlockReply returns the requested block.
type GetBlockReply struct {
	SkipBlock *skipchain.SkipBlock
}

// ForwardSignature is called once a new skipblock has been accepted by
// signing the forward-link, and then the older skipblocks need to
// update their forward-links. Each cothority needs to get the necessary
// blocks and propagate the skipblocks itself.
type ForwardSignature struct {
	// TargetHeight is the index in the backlink-slice of the skipblock
	// to update
	TargetHeight int
	// Previous is the second-newest skipblock
	Previous skipchain.SkipBlockID
	// Newest is the newest skipblock, signed by previous
	Newest *skipchain.SkipBlock
	// ForwardLink is the signature from Previous to Newest
	ForwardLink *skipchain.BlockLink
}
