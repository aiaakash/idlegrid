// Package e2e implements the end-to-end encryption between the gateway and
// provider daemons:
//
//	X25519 ephemeral↔static ECDH → HKDF-SHA256 → XChaCha20-Poly1305
//	(+ Ed25519 signatures for engine-reported usage)
//
// Properties: the coordinator relays prompt ciphertext without being able to
// read it; each request uses a fresh ephemeral key (forward secrecy); each
// response leg uses a gateway-generated response key. This is the modern
// equivalent of Darkbloom's NaCl Box scheme (XSalsa20 is unavailable in
// Apple's Swift Crypto, ChaChaPoly is native in both stacks).
package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	// KeySize is the X25519/Ed25519 raw key size (32 bytes).
	KeySize = 32
	// NonceSize is the ChaCha20-Poly1305 nonce size (12 bytes; fresh ECDH
	// per payload makes random nonces safe).
	NonceSize = 12
	// Info is the HKDF info string binding keys to this protocol.
	Info = "idlegrid-e2e-v1"
)

// XKeyPair is an X25519 key pair (raw 32-byte keys).
type XKeyPair struct {
	Priv, Pub [32]byte
}

// GenerateX25519 creates a fresh X25519 key pair.
func GenerateX25519() XKeyPair {
	var kp XKeyPair
	if _, err := rand.Read(kp.Priv[:]); err != nil {
		panic(err)
	}
	pub, err := curve25519.X25519(kp.Priv[:], curve25519.Basepoint)
	if err != nil {
		panic(err)
	}
	copy(kp.Pub[:], pub)
	return kp
}

// EdKeyPair is an Ed25519 signing key pair.
type EdKeyPair struct {
	Pub  ed25519.PublicKey
	Priv ed25519.PrivateKey
}

// GenerateEd25519 creates a fresh signing key pair.
func GenerateEd25519() EdKeyPair {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return EdKeyPair{Pub: priv.Public().(ed25519.PublicKey), Priv: priv}
}

func deriveKey(shared, salt []byte) ([]byte, error) {
	r := hkdf.New(sha256.New, shared, salt, []byte(Info))
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := r.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// Seal encrypts plaintext to the recipient's X25519 public key using a fresh
// ephemeral sender key. Returns the base64 eph pubkey, nonce, and ciphertext.
func Seal(plaintext []byte, recipientPub *[32]byte) (ephPubB64, nonceB64, ctB64 string, err error) {
	eph := GenerateX25519()
	shared, err := curve25519.X25519(eph.Priv[:], recipientPub[:])
	if err != nil {
		return "", "", "", err
	}
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", "", "", err
	}
	key, err := deriveKey(shared, nonce)
	if err != nil {
		return "", "", "", err
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return "", "", "", err
	}
	ct := aead.Seal(nil, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(eph.Pub[:]),
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(ct), nil
}

// Open decrypts a sealed payload with the recipient's private key.
func Open(ephPubB64, nonceB64, ctB64 string, recipientPriv *[32]byte) ([]byte, error) {
	ephPub, err := base64.StdEncoding.DecodeString(ephPubB64)
	if err != nil || len(ephPub) != KeySize {
		return nil, fmt.Errorf("bad eph pubkey")
	}
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil || len(nonce) != NonceSize {
		return nil, fmt.Errorf("bad nonce")
	}
	ct, err := base64.StdEncoding.DecodeString(ctB64)
	if err != nil {
		return nil, fmt.Errorf("bad ciphertext")
	}
	shared, err := curve25519.X25519(recipientPriv[:], ephPub)
	if err != nil {
		return nil, err
	}
	key, err := deriveKey(shared, nonce)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ct, nil)
}

// Sign returns the base64 Ed25519 signature of message.
func Sign(k EdKeyPair, message []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(k.Priv, message))
}

// Verify checks a base64 Ed25519 signature over message.
func Verify(pubB64 string, message []byte, sigB64 string) bool {
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), message, sig)
}

// PubKeyFromB64 parses a base64 X25519 public key.
func PubKeyFromB64(b64 string) (*[32]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != KeySize {
		return nil, fmt.Errorf("bad public key")
	}
	var out [32]byte
	copy(out[:], raw)
	return &out, nil
}

// PubKeyToB64 encodes an X25519 public key.
func PubKeyToB64(pub *[32]byte) string {
	return base64.StdEncoding.EncodeToString(pub[:])
}
