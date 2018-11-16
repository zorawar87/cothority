package ch.epfl.dedis.calypso;

import ch.epfl.dedis.lib.Roster;
import ch.epfl.dedis.lib.SkipblockId;
import ch.epfl.dedis.byzcoin.ByzCoinRPC;
import ch.epfl.dedis.byzcoin.Proof;
import ch.epfl.dedis.lib.crypto.Point;
import ch.epfl.dedis.lib.darc.Darc;
import ch.epfl.dedis.lib.darc.Signer;
import ch.epfl.dedis.lib.exception.CothorityCommunicationException;
import ch.epfl.dedis.lib.exception.CothorityException;
import ch.epfl.dedis.lib.proto.Calypso;
import com.google.protobuf.ByteString;
import com.google.protobuf.InvalidProtocolBufferException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Duration;
import java.util.List;

/**
 * CalypsoRPC is the entry point for all the RPC calls to the Calypso service, which acts as the secret-management cothority.
 */
public class CalypsoRPC extends ByzCoinRPC {
    private CreateLTSReply lts;

    private final Logger logger = LoggerFactory.getLogger(ch.epfl.dedis.calypso.CalypsoRPC.class);

    /**
     * Creates a new Long Term Secret on an existing ByzCoin ledger.
     *
     * @param byzcoin the existing byzcoin ledger.
     * @throws CothorityException if something goes wrong
     */
    public CalypsoRPC(ByzCoinRPC byzcoin) throws CothorityException {
        super(byzcoin);
    }

    public CalypsoRPC(Roster roster, Darc genesis, Duration blockInterval) throws CothorityException {
        super(roster, genesis, blockInterval);
    }

    /**
     * Creates a new ByzCoin ledger and a new Long Term Secret. The roster that holds the long-term secret is the same
     * one as the ByzCoin roster.
     *
     * @param roster        the nodes participating in the ledger and holds shares to the LTS
     * @param genesis       the first darc
     * @param blockInterval how often a new block is created
     * @param signers
     * @param signerCtrs
     * @throws CothorityException if something goes wrong
     */
    public CalypsoRPC(Roster roster, Darc genesis, Duration blockInterval, List<Signer> signers, List<Long> signerCtrs) throws CothorityException {
        this(roster, roster, genesis, blockInterval, signers, signerCtrs);
    }

    /**
     *
     * @param byzcoinRoster
     * @param ltsRoster
     * @param genesis
     * @param blockInterval
     * @param signers
     * @param signerCtrs
     * @throws CothorityException
     */
    public CalypsoRPC(Roster byzcoinRoster, Roster ltsRoster, Darc genesis, Duration blockInterval, List<Signer> signers, List<Long> signerCtrs) throws CothorityException {
        this(byzcoinRoster, genesis, blockInterval);
        if (genesis.getExpression("spawn:" + LTSInstance.ContractId) == null || genesis.getExpression("invoke:" + LTSInstance.InvokeCommand) == null) {
            throw new CothorityException("darc must contain permissions for LTS and resharing");
        }
        // Send a transaction to store the LTS roster in ByzCoin
        LTSInstance inst = new LTSInstance(this, genesis.getBaseId(), ltsRoster, signers, signerCtrs);
        Proof proof = inst.getProof();
        // Start the LTS/DKG protocol.
        CreateLTSReply lts = createLTS(proof);
        this.lts = lts;
    }

    /**
     * Private constructor to keep reconnections to static methods called fromCalypso.
     * @param bc existing byzcoin service
     * @param ltsId id of the Long Term Secret
     * @throws CothorityCommunicationException
     */
    private CalypsoRPC(ByzCoinRPC bc, LTSId ltsId) throws CothorityCommunicationException{
        super(bc);
        lts = getLTSReply(ltsId);
    }

    /**
     * returns the shared symmetricKey of the DKG that must be used to encrypt the
     * symmetric encryption symmetricKey. This will be the same as LTS.X
     * stored when creating Calypso.
     *
     * @param ltsId the long term secret ID
     * @return the aggregate public symmetricKey of the ocs-shard
     * @throws CothorityCommunicationException in case of communication difficulties
     */
    public CreateLTSReply getLTSReply(LTSId ltsId) throws CothorityCommunicationException {
        Calypso.GetLTSReply.Builder request =
                Calypso.GetLTSReply.newBuilder();
        request.setLtsid(ltsId.toProto());

        ByteString msg = getRoster().sendMessage("Calypso/GetLTSReply", request.build());

        try {
            Calypso.CreateLTSReply reply = Calypso.CreateLTSReply.parseFrom(msg);
            return new CreateLTSReply(reply);
        } catch (InvalidProtocolBufferException e) {
            throw new CothorityCommunicationException(e);
        }
    }

    /**
     * Create a long-term-secret (LTS) and retrieve its configuration.
     *
     * @return The LTS configuration that is needed to execute the write contract.
     * @throws CothorityCommunicationException if something went wrong
     */
    public CreateLTSReply createLTS(Proof proof) throws CothorityCommunicationException {
        Calypso.CreateLTS.Builder b = Calypso.CreateLTS.newBuilder();
        b.setProof(proof.toProto());

        ByteString msg = getRoster().sendMessage("Calypso/CreateLTS", b.build());

        try {
            Calypso.CreateLTSReply resp = Calypso.CreateLTSReply.parseFrom(msg);
            return new CreateLTSReply(resp);
        } catch (InvalidProtocolBufferException e) {
            throw new CothorityCommunicationException(e);
        }
    }

    /**
     * Ask the secret-manageemnt cothority for the decryption shares.
     *
     * @param writeProof The proof of the write request.
     * @param readProof  The proof of the read request.
     * @return All the decryption shares that can be used to reconstruct the decryption key.
     * @throws CothorityCommunicationException if something went wrong
     */
    public DecryptKeyReply tryDecrypt(Proof writeProof, Proof readProof) throws CothorityCommunicationException {
        Calypso.DecryptKey.Builder b = Calypso.DecryptKey.newBuilder();
        b.setRead(readProof.toProto());
        b.setWrite(writeProof.toProto());

        ByteString msg = getRoster().sendMessage("Calypso/DecryptKey", b.build());

        try {
            Calypso.DecryptKeyReply resp = Calypso.DecryptKeyReply.parseFrom(msg);
            return new DecryptKeyReply(resp);
        } catch (InvalidProtocolBufferException e) {
            throw new CothorityCommunicationException(e);
        }
    }

    /**
     * @return the id of the Long Term Secret
     */
    public LTSId getLTSId() {
        return lts.hash();
    }

    /**
     * @return the shared public key of the Long Term Secret
     */
    public Point getLTSX() {
        return lts.getX();
    }

    /**
     * @return the Long Term Secret.
     */
    public CreateLTSReply getLTS(){
        return lts;
    }

    /**
     * Connects to an existing byzcoin and an existing Long Term Secret.
     *
     * @param roster    the nodes handling the byzcoin ledger
     * @param byzcoinId the id of the byzcoin ledger to connect to
     * @param ltsId     the id of the Long Term Secret to use
     * @return CalypsoRPC if everything was found
     * @throws CothorityException if something goes wrong
     */
    public static CalypsoRPC fromCalypso(Roster roster, SkipblockId byzcoinId, LTSId ltsId) throws CothorityException {
        return new CalypsoRPC(ByzCoinRPC.fromByzCoin(roster, byzcoinId), ltsId);
    }
}
