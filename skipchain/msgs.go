package skipchain

import "github.com/dedis/onet/network"

func init() {
	for _, m := range []interface{}{
		// - API calls
		// Store new skipblock
		&StoreSkipBlock{},
		&StoreSkipBlockReply{},
		// Requests for data
		&GetUpdateChain{},
		&GetUpdateChainReply{},
		// Request updated block
		&GetSingleBlock{},
		// Fetch all skipchains
		&GetAllSkipchains{},
		&GetAllSkipchainsReply{},
		// - Data structures
		&SkipBlockFix{},
		&SkipBlock{},
	} {
		network.RegisterMessage(m)
	}
}

// This file holds all messages that can be sent to the SkipChain,
// both from the outside and between instances of this service

// External calls

// StoreSkipBlock - Requests a new skipblock to be appended to
// the given SkipBlock. If the given SkipBlock has Index 0 (which
// is invalid), a new SkipChain will be created.
type StoreSkipBlock struct {
	LatestID SkipBlockID
	NewBlock *SkipBlock
}

// StoreSkipBlockReply - returns the signed SkipBlock with updated backlinks
type StoreSkipBlockReply struct {
	Previous *SkipBlock
	Latest   *SkipBlock
}

// GetUpdateChain - the client sends the hash of the last known
// Skipblock and will get back a list of all necessary SkipBlocks
// to get to the latest.
type GetUpdateChain struct {
	LatestID SkipBlockID
}

// GetUpdateChainReply - returns the shortest chain to the current SkipBlock,
// starting from the SkipBlock the client sent
type GetUpdateChainReply struct {
	Update []*SkipBlock
}

// GetAllSkipchains - returns all known last blocks of skipchains.
type GetAllSkipchains struct {
}

// GetAllSkipchainsReply - returns all known last blocks of skipchains.
type GetAllSkipchainsReply struct {
	SkipChains []*SkipBlock
}

// GetSingleBlock asks for a single block.
type GetSingleBlock struct {
	ID SkipBlockID
}

// GetSingleBlockByIndex asks for a single block.
type GetSingleBlockByIndex struct {
	Genesis SkipBlockID
	Index   int
}
