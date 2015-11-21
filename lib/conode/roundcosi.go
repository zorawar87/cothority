package conode

import (
	"strconv"

	log "github.com/Sirupsen/logrus"
	dbg "github.com/dedis/cothority/lib/debug_lvl"

	"errors"
	"github.com/dedis/cothority/lib/coconet"
	"github.com/dedis/cothority/lib/hashid"
	"github.com/dedis/cothority/lib/proof"
	"github.com/dedis/cothority/lib/sign"
	"github.com/dedis/crypto/abstract"
	"fmt"
)

// The name type of this round implementation
const RoundCosiType = "cosi"

type RoundCosi struct {
	*RoundStructure
							   // Leaves, Root and Proof for a round
	CosiLeaves []hashid.HashId // can be removed after we verify protocol
	CosiRoot   hashid.HashId
	CosiProofs []proof.Proof
							   // Timestamp message for this Round
	Timestamp  int64

	peer       *Peer
	Merkle     *sign.MerkleStruct
	Node       *sign.Node

	Queue      []ReplyMessage
}

type ReplyMessage struct {
	Val   []byte
	To    string
	ReqNo byte
}

func RegisterRoundCosi(p *Peer) {
	sign.RegisterRoundFactory(RoundCosiType,
		func(s *sign.Node) sign.Round {
			return NewRoundCosi(p)
		})
}

func NewRoundCosi(peer *Peer) *RoundCosi {
	round := &RoundCosi{}
	round.peer = peer
	round.Node = peer.Node
	return round
}

// AnnounceFunc will keep the timestamp generated for this round
func (round *RoundCosi) Announcement(viewNbr, roundNbr int, in *sign.SigningMessage, out []*sign.SigningMessage) error {
	round.RoundStructure = NewRoundStructure(round.Node, viewNbr, roundNbr)
	if err := round.Node.TryFailure(round.Node.ViewNo, roundNbr); err != nil {
		return err
	}

	if err := sign.MerkleSetup(round.Node, round.Node.ViewNo, roundNbr, in.Am); err != nil {
		return err
	}
	// Store the message for the round
	round.Merkle = round.Node.MerkleStructs[roundNbr]
	round.Merkle.Msg = in.Am.Message

	// Inform all children of announcement - just copy the one that came in
	for i := range out {
		*out[i].Am = *in.Am
	}
	return nil
}

func (round *RoundCosi) Commitment(in []*sign.SigningMessage, out *sign.SigningMessage) error {
	// prepare to handle exceptions
	merkle := round.Merkle
	merkle.Commits = in
	merkle.ExceptionList = make([]abstract.Point, 0)

	// Create the mapping between children and their respective public key + commitment
	// V for commitment
	children := merkle.Children
	merkle.ChildV_hat = make(map[string]abstract.Point, len(children))
	// X for public key
	merkle.ChildX_hat = make(map[string]abstract.Point, len(children))

	for key := range children {
		merkle.ChildX_hat[key] = merkle.Suite.Point().Null()
		merkle.ChildV_hat[key] = merkle.Suite.Point().Null()
	}

	// Commits from children are the first Merkle Tree leaves for the round
	merkle.Leaves = make([]hashid.HashId, 0)
	merkle.LeavesFrom = make([]string, 0)
	for _, sm := range merkle.Commits {
		from := sm.From
		// MTR ==> root of sub-merkle tree
		merkle.Leaves = append(merkle.Leaves, sm.Com.MTRoot)
		merkle.LeavesFrom = append(merkle.LeavesFrom, from)
		merkle.ChildV_hat[from] = sm.Com.V_hat
		merkle.ChildX_hat[from] = sm.Com.X_hat
		merkle.ExceptionList = append(merkle.ExceptionList, sm.Com.ExceptionList...)

		// Aggregation
		// add good child server to combined public key, and point commit
		merkle.Add(merkle.X_hat, sm.Com.X_hat)
		merkle.Add(merkle.Log.V_hat, sm.Com.V_hat)
		//dbg.Lvl4("Adding aggregate public key from ", from, " : ", sm.Com.X_hat)
	}

	dbg.Lvl4("sign.Node.Commit using Merkle")
	merkle.MerkleAddChildren()
	// compute the local Merkle root

	// give up if nothing to process
	if len(round.Queue) == 0 {
		round.CosiRoot = make([]byte, hashid.Size)
		round.CosiProofs = make([]proof.Proof, 1)
	} else {
		// pull out to be Merkle Tree leaves
		round.CosiLeaves = make([]hashid.HashId, 0)
		for _, msg := range round.Queue {
			round.CosiLeaves = append(round.CosiLeaves, hashid.HashId(msg.Val))
		}

		// create Merkle tree for this round's messages and check corectness
		round.CosiRoot, round.CosiProofs = proof.ProofTree(round.Merkle.Suite.Hash, round.CosiLeaves)
		if dbg.DebugVisible > 2 {
			if proof.CheckLocalProofs(round.Merkle.Suite.Hash, round.CosiRoot, round.CosiLeaves, round.CosiProofs) == true {
				dbg.Lvl4("Local Proofs of", round.Node.Name(), "successful for round " + strconv.Itoa(int(round.Node.LastRound())))
			} else {
				panic("Local Proofs" + round.Node.Name() + " unsuccessful for round " + strconv.Itoa(int(round.Node.LastRound())))
			}
		}
	}

	round.Merkle.MerkleAddLocal(round.CosiRoot)
	round.Merkle.MerkleHashLog()
	round.Merkle.ComputeCombinedMerkleRoot()

	out.Com.V = round.Merkle.Log.V
	out.Com.V_hat = round.Merkle.Log.V_hat
	out.Com.X_hat = round.Merkle.X_hat
	out.Com.MTRoot = round.Merkle.MTRoot
	out.Com.ExceptionList = round.Merkle.ExceptionList
	out.Com.Vote = round.Merkle.Vote
	out.Com.Messages = round.Node.Messages

	// Reset message counter for statistics
	round.Node.Messages = 0
	return nil

}

func (round *RoundCosi) QueueSet(Queue [][]MustReplyMessage) {
	// messages read will now be processed
	Queue[READING], Queue[PROCESSING] = Queue[PROCESSING], Queue[READING]
	Queue[READING] = Queue[READING][:0]
	round.Queue = make([]ReplyMessage, len(Queue[PROCESSING]))
	for i, q := range (Queue[PROCESSING]) {
		round.Queue[i] = ReplyMessage{
			Val: q.Tsm.Sreq.Val,
			To: q.To,
			ReqNo: byte(q.Tsm.ReqNo),
		}
	}
}

func (round *RoundCosi) Challenge(in *sign.SigningMessage, out []*sign.SigningMessage) error {

	merkle := round.Merkle
	// we are root
	if round.isRoot {
		msg := merkle.Msg
		msg = append(msg, []byte(merkle.MTRoot)...)
		merkle.C = sign.HashElGamal(merkle.Suite, msg, merkle.Log.V_hat)
		//proof := make([]hashid.HashId, 0)

		in.Chm.C = merkle.C
		in.Chm.MTRoot = merkle.MTRoot
		in.Chm.Proof = merkle.Proof
		in.Chm.Vote = merkle.Vote
	} else { // we are a leaf
		// register challenge
		merkle.C = in.Chm.C
	}
	// compute response share already + localmerkle proof
	merkle.InitResponseCrypto()
	// messages from clients, proofs computed
	if merkle.Log.Getv() != nil {
		if err := merkle.StoreLocalMerkleProof(in.Chm); err != nil {
			return err
		}
	}

	// proof from big root to our root will be sent to all children
	baseProof := make(proof.Proof, len(in.Chm.Proof))
	copy(baseProof, in.Chm.Proof)

	if len(merkle.Children) != len(out) {
		return fmt.Errorf("Children and output are of different length")
	}
	// for each child, create personalized part of proof
	// embed it in SigningMessage, and send it
	var i = 0
	for name, _ := range merkle.Children {
		out[i].Chm.C = in.Chm.C
		out[i].Chm.MTRoot = in.Chm.MTRoot
		out[i].Chm.Proof = append(baseProof, merkle.Proofs[name]...)
		out[i].To = name
		i++
	}
	return nil
}

// TODO make that sms == nil in case we are a leaf to stay consistent with
// others calls
func (round *RoundCosi) Response(sms []*sign.SigningMessage, out *sign.SigningMessage) error {
	// initialize exception handling
	exceptionV_hat := round.Merkle.Suite.Point().Null()
	exceptionX_hat := round.Merkle.Suite.Point().Null()
	round.Merkle.ExceptionList = make([]abstract.Point, 0)
	nullPoint := round.Merkle.Suite.Point().Null()

	children := round.Merkle.Children
	for _, sm := range sms {
		from := sm.From
		switch sm.Type {
		default:
			// default == no response from child
			// dbg.Lvl4(sn.Name(), "default in respose for child", from, sm)
			if children[from] != nil {
				round.Merkle.ExceptionList = append(round.Merkle.ExceptionList, children[from].PubKey())

				// remove public keys and point commits from subtree of failed child
				round.Merkle.Add(exceptionX_hat, round.Merkle.ChildX_hat[from])
				round.Merkle.Add(exceptionV_hat, round.Merkle.ChildV_hat[from])
			}
			continue
		case sign.Response:
			// disregard response from children who did not commit
			_, ok := round.Merkle.ChildV_hat[from]
			if ok == true && round.Merkle.ChildV_hat[from].Equal(nullPoint) {
				continue
			}

			// dbg.Lvl4(sn.Name(), "accepts response from", from, sm.Type)
			round.Merkle.R_hat.Add(round.Merkle.R_hat, sm.Rm.R_hat)

			round.Merkle.Add(exceptionV_hat, sm.Rm.ExceptionV_hat)

			round.Merkle.Add(exceptionX_hat, sm.Rm.ExceptionX_hat)
			round.Merkle.ExceptionList = append(round.Merkle.ExceptionList, sm.Rm.ExceptionList...)

		case sign.Error:
			if sm.Err == nil {
				dbg.Lvl2("Error message with no error")
				continue
			}

			// Report up non-networking error, probably signature failure
			dbg.Lvl2(round.Merkle.Name, "Error in respose for child", from, sm)
			err := errors.New(sm.Err.Err)
			return err
		}
	}

	// remove exceptions from subtree that failed
	round.Merkle.Sub(round.Merkle.X_hat, exceptionX_hat)
	round.Merkle.ExceptionV_hat = exceptionV_hat
	round.Merkle.ExceptionX_hat = exceptionX_hat

	dbg.Lvl4(round.Merkle.Name, "got all responses")
	err := round.Merkle.VerifyResponses()
	if err != nil {
		dbg.Lvl3(round.Node.Name(), "Could not verify responses..")
		return err
	}
	rm := &sign.ResponseMessage{
		R_hat:          round.Merkle.R_hat,
		ExceptionList:  round.Merkle.ExceptionList,
		ExceptionV_hat: round.Merkle.ExceptionV_hat,
		ExceptionX_hat: round.Merkle.ExceptionX_hat,
	}
	out.Rm = rm
	return nil
}

func (round *RoundCosi) SignatureBroadcast(in *sign.SigningMessage, out []*sign.SigningMessage) error {
	// Root is creating the sig broadcast
	sb := in.SBm
	if round.isRoot {
		dbg.Lvl2(round.Node.Name(), ": sending number of messages:", round.Node.Messages)
		in.SBm.R0_hat = round.Merkle.R_hat
		in.SBm.C = round.Merkle.C
		in.SBm.X0_hat = round.Merkle.X_hat
		in.SBm.V0_hat = round.Merkle.Log.V_hat
		in.SBm.Messages = round.Node.Messages
	} else {
		round.Node.Messages = sb.Messages
	}
	// Inform all children of broadcast  - just copy the one that came in
	for i := range out {
		*out[i].SBm = *sb
	}
	// Send back signature to clients
	for i, msg := range round.Queue {
		// proof to get from s.Root to big root
		combProof := make(proof.Proof, len(round.Merkle.Proof))
		copy(combProof, round.Merkle.Proof)

		// add my proof to get from a leaf message to my root s.Root
		combProof = append(combProof, round.CosiProofs[i]...)

		// proof that I can get from a leaf message to the big root
		if proof.CheckProof(round.Merkle.Suite.Hash, round.Merkle.MTRoot,
			round.CosiLeaves[i], combProof) {
			dbg.Lvl2("Proof is OK for msg", msg)
		} else {
			dbg.Lvl2("Inclusion-proof failed")
		}

		respMessg := &TimeStampMessage{
			Type:  StampSignatureType,
			ReqNo: SeqNo(msg.ReqNo),
			Srep: &StampSignature{
				SuiteStr:   round.Merkle.Suite.String(),
				Timestamp:  round.Timestamp,
				MerkleRoot: round.Merkle.MTRoot,
				Prf:        combProof,
				Response:   sb.R0_hat,
				Challenge:  sb.C,
				AggCommit:  sb.V0_hat,
				AggPublic:  sb.X0_hat,
			}}
		round.PutToClient(msg.To, respMessg)
		dbg.Lvl2("Sent signature response back to client", msg.To)
	}
	round.Timestamp = 0
	return nil
}

// Send message to client given by name
func (round *RoundCosi) PutToClient(name string, data coconet.BinaryMarshaler) {
	err := round.peer.Clients[name].PutData(data)
	if err == coconet.ErrClosed {
		round.peer.Clients[name].Close()
		return
	}
	if err != nil && err != coconet.ErrNotEstablished {
		log.Warnf("%p error putting to client: %v", round, err)
	}
}
