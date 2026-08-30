import Crypto
import Foundation

/// E2E crypto identity for the provider daemon.
///
/// Request leg:  gateway ephemeral X25519 ↔ provider static X25519 → HKDF-SHA256 → ChaChaPoly
/// Response leg: provider ephemeral X25519 ↔ gateway response key
/// Usage auth:   Ed25519 signature over the usage JSON (provider's signing key)
///
/// Matches coordinator/internal/e2e (Go) byte-for-byte.
struct E2EIdentity {
    let x25519: Curve25519.KeyAgreement.PrivateKey
    let signing: Curve25519.Signing.PrivateKey

    init() {
        x25519 = Curve25519.KeyAgreement.PrivateKey()
        signing = Curve25519.Signing.PrivateKey()
    }

    var publicKeyB64: String { Data(x25519.publicKey.rawRepresentation).base64EncodedString() }
    var signingKeyB64: String { Data(signing.publicKey.rawRepresentation).base64EncodedString() }
}

enum E2E {
    static let info = Data("idlegrid-e2e-v1".utf8)

    static func b64(_ data: Data) -> String { data.base64EncodedString() }
    static func unb64(_ s: String) -> Data? { Data(base64Encoded: s) }

    private static func deriveKey(_ shared: SharedSecret, salt: Data) -> SymmetricKey {
        shared.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: salt,
            sharedInfo: info,
            outputByteCount: 32
        )
    }

    /// Seals plaintext to the peer's X25519 public key with a fresh ephemeral
    /// key + random nonce. Returns the SealedPayload fields (base64).
    static func seal(_ plaintext: Data, peerPubB64: String,
                     identity: E2EIdentity) throws -> (ephPub: String, nonce: String, ciphertext: String) {
        try sealWithEphemeral(plaintext, peerPubB64: peerPubB64, eph: identity.x25519)
    }

    /// Seals with an explicit ephemeral key (response leg: one per request).
    static func sealWithEphemeral(_ plaintext: Data, peerPubB64: String,
                                  eph: Curve25519.KeyAgreement.PrivateKey) throws -> (ephPub: String, nonce: String, ciphertext: String) {
        guard let peerRaw = unb64(peerPubB64), peerRaw.count == 32 else {
            throw E2EError.badPeerKey
        }
        let peer = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: peerRaw)
        let shared = try eph.sharedSecretFromKeyAgreement(with: peer)
        let nonceData = (0..<12).map { _ in UInt8.random(in: 0...255) }
        let key = deriveKey(shared, salt: Data(nonceData))
        let sealed = try ChaChaPoly.seal(plaintext, using: key, nonce: ChaChaPoly.Nonce(data: Data(nonceData)))
        // Go's AEAD output is ciphertext||tag — concatenate both.
        let ctWithTag = sealed.ciphertext + sealed.tag
        return (b64(Data(eph.publicKey.rawRepresentation)), b64(Data(nonceData)), b64(ctWithTag))
    }

    /// Opens a payload sealed to our static key by the peer's ephemeral key.
    static func open(ephPubB64: String, nonceB64: String, ctB64: String,
                     identity: E2EIdentity) throws -> Data {
        guard let eph = unb64(ephPubB64), eph.count == 32,
              let nonce = unb64(nonceB64), nonce.count == 12,
              let ct = unb64(ctB64), ct.count > 16
        else { throw E2EError.badPayload }
        let peer = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: eph)
        let shared = try identity.x25519.sharedSecretFromKeyAgreement(with: peer)
        let key = deriveKey(shared, salt: nonce)
        // Go sends nonce separately; AEAD output is ciphertext||tag.
        // Reconstruct CryptoKit's combined format: nonce || ciphertext || tag.
        let sealedBox = try ChaChaPoly.SealedBox(combined: nonce + ct)
        return try ChaChaPoly.open(sealedBox, using: key)
    }

    /// Signs usage JSON with the provider's Ed25519 key.
    static func sign(_ data: Data, identity: E2EIdentity) -> String? {
        (try? identity.signing.signature(for: data))?.base64EncodedString()
    }

    /// Verifies a base64 Ed25519 signature over data.
    static func verify(pubB64: String, data: Data, sigB64: String) -> Bool {
        guard let pub = unb64(pubB64), pub.count == 32,
              let sig = unb64(sigB64) else { return false }
        return identityVerify(pub, data, sig)
    }

    private static func identityVerify(_ pub: Data, _ data: Data, _ sig: Data) -> Bool {
        guard let key = try? Curve25519.Signing.PublicKey(rawRepresentation: pub) else { return false }
        return key.isValidSignature(sig, for: data)
    }
}

enum E2EError: Error {
    case badPeerKey
    case badPayload
    case decryptFailed
}
