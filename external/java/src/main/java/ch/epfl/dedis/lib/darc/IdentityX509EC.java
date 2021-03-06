package ch.epfl.dedis.lib.darc;

import ch.epfl.dedis.lib.Hex;
import ch.epfl.dedis.lib.exception.CothorityCryptoException;
import ch.epfl.dedis.lib.proto.DarcProto;
import com.google.protobuf.ByteString;

import java.security.*;
import java.security.Signature;
import java.security.spec.InvalidKeySpecException;
import java.security.spec.X509EncodedKeySpec;
import java.util.Arrays;

/**
 * IdentityX509EC represents a keycard that holds its private key and can only be used to sign
 * but which will not reveal its private key.
 */
public class IdentityX509EC implements Identity {
    private final PublicKey pubKey;

    /**
     * Creates an IdentityX509EC from a protobuf representation.
     * @param proto a protobuf representation
     */
    public IdentityX509EC(DarcProto.IdentityX509EC proto) {
        try {
            KeyFactory keyFactory = KeyFactory.getInstance("EC");
            X509EncodedKeySpec pubSpec = new X509EncodedKeySpec(proto.getPublic().toByteArray());
            pubKey = keyFactory.generatePublic(pubSpec);
        } catch (InvalidKeySpecException | NoSuchAlgorithmException e) {
            throw new RuntimeException("Unable to deserialise IdentityX509EC identity", e);
        }
    }

    /**
     * Creates an IdentityEd25519 from a SignerEd25519.
     * @param signer the input signer
     */
    public IdentityX509EC(Signer signer) {
        if (SignerX509EC.class.isInstance(signer)) {
            pubKey = ((SignerX509EC) signer).getPublicKey();
        } else {
            throw new RuntimeException("Wrong signer type: " + signer.toString());
        }
    }

    /**
     * Returns true if the verification of signature on the sha-256 of msg is
     * successful or false if not.
     * @param msg the message
     * @param signature the signature
     * @return true if the signature is valid
     */
    public boolean verify(byte[] msg, byte[] signature) {
        // TODO: it is interesting why client code need to verify keycard singature ?
        // TODO It should be verified at server side.
        // TODO: verify the signature given the msg, the signature and our public byte-array.
        try {
            final Signature signature2 = Signature.getInstance("SHA384withECDSA");
            signature2.initVerify(pubKey);
            signature2.update(msg);
            return signature2.verify(signature);
        } catch (InvalidKeyException | SignatureException | NoSuchAlgorithmException e) {
            // TODO: consider throwing some exception
            return false;
        }
    }

    /**
     * Creates a protobuf-representation of the implementation. The protobuf
     * representation has to hold all necessary fields to represent any of the
     * identity implementations.
     * @return a protobuf-representation of the Identity
     */
    public DarcProto.Identity toProto(){
        DarcProto.Identity.Builder bid = DarcProto.Identity.newBuilder();
        DarcProto.IdentityX509EC.Builder bed = DarcProto.IdentityX509EC.newBuilder();
        bed.setPublic(ByteString.copyFrom(pubKey.getEncoded()));
        bid.setX509Ec(bed);
        return bid.build();
    }

    public String toString(){
        return String.format("%s:%s", this.typeString(), Hex.printHexBinary(this.pubKey.getEncoded()).toLowerCase());
    }

    @Override
    public boolean equals(Object other) {
        if (other == null) return false;
        if (other == this) return true;
        if (!(other instanceof IdentityX509EC))return false;
        IdentityX509EC otherEd = (IdentityX509EC) other;
        return Arrays.equals(pubKey.getEncoded(), otherEd.pubKey.getEncoded());
    }

    public String typeString() {
        return "x509ec";
    }

    public byte[] getPublicBytes() {
        return pubKey.getEncoded();
    }
}
