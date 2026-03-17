// Package tesla — crypto.go
//
// This file provides AES-256-GCM encryption and decryption helpers used to
// protect Tesla OAuth tokens before storing them in the database.
//
// Why encrypt tokens at rest?
// ───────────────────────────
// Tesla access tokens and refresh tokens are essentially passwords — anyone who
// holds them can control a Tesla vehicle. If our database is ever compromised,
// we do NOT want those tokens to be readable in plain text.
//
// By encrypting every token with a secret key that lives only in an environment
// variable (TESLA_TOKEN_SECRET), a database dump alone is useless to an attacker.
// They would also need the secret key.
//
// Algorithm choice — AES-256-GCM:
// ────────────────────────────────
// AES-256-GCM is an authenticated encryption scheme. It provides:
//   - Confidentiality  → nobody can read the plaintext without the key
//   - Integrity        → any tampering with the ciphertext is detected
//   - Authentication   → confirms the ciphertext was produced by someone with the key
//
// A fresh random nonce (IV) is generated for every encryption call, so
// encrypting the same token twice produces different ciphertexts. This prevents
// attackers from detecting when two admins share the same token.
package tesla

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// Encrypt encrypts a plaintext string with AES-256-GCM and returns a
// base64url-encoded string that is safe to store in the database.
//
// The output format is:  base64url( nonce || ciphertext || auth-tag )
//
// The nonce is prepended to the ciphertext so Decrypt can extract it.
// GCM's Seal() appends the authentication tag automatically at the end.
//
// Parameters:
//   - plaintext: The raw token string to encrypt (e.g. "eyJhbGci...")
//   - key:       The secret key from TESLA_TOKEN_SECRET env var. It can be any
//     string; it is SHA-256 hashed internally to produce exactly 32 bytes.
func Encrypt(plaintext, key string) (string, error) {
	// Step 1: Create the AES cipher block using our derived 32-byte key.
	block, err := newCipherBlock(key)
	if err != nil {
		return "", err
	}

	// Step 2: Wrap the block cipher in GCM (Galois/Counter Mode).
	// GCM turns the block cipher into a stream cipher with authentication.
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	// Step 3: Generate a cryptographically random nonce (number used once).
	// GCM requires a unique nonce for every encryption. Reusing a nonce with
	// the same key is catastrophic — it breaks both confidentiality and integrity.
	// Using crypto/rand (not math/rand) guarantees unpredictability.
	nonce := make([]byte, gcm.NonceSize()) // NonceSize() is 12 bytes for GCM
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}

	// Step 4: Encrypt.
	// gcm.Seal(dst, nonce, plaintext, additionalData)
	//   - dst=nonce  → the nonce is used as the prefix of the output buffer
	//     so the result is: nonce || ciphertext || auth-tag
	//   - additionalData=nil → we don't need AAD for our use case
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Step 5: Base64url-encode so it is a safe, printable string for the DB.
	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

// Decrypt reverses Encrypt: it base64url-decodes the stored ciphertext, splits
// off the nonce, then decrypts and authenticates with AES-256-GCM.
//
// Returns an error if:
//   - The ciphertext was tampered with (auth tag mismatch).
//   - The wrong key is provided.
//   - The base64 encoding is invalid.
func Decrypt(ciphertext, key string) (string, error) {
	// Step 1: Recreate the same cipher block from the same key.
	block, err := newCipherBlock(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	// Step 2: Decode from base64url back to raw bytes.
	decoded, err := base64.URLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decoding ciphertext: %w", err)
	}

	// Step 3: Extract the nonce from the front of the decoded bytes.
	// The nonce was prepended during Encrypt so we know exactly how long it is.
	nonceSize := gcm.NonceSize()
	if len(decoded) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, data := decoded[:nonceSize], decoded[nonceSize:]

	// Step 4: Decrypt and verify the authentication tag.
	// Open() will return an error if the tag doesn't match, meaning the
	// data was either tampered with or decrypted with the wrong key.
	plaintext, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}

	return string(plaintext), nil
}

// newCipherBlock derives a 32-byte AES key from an arbitrary string by
// computing its SHA-256 hash, then creates an AES cipher block.
//
// Why hash the key?
//   - AES-256 requires exactly 32 bytes.
//   - Our TESLA_TOKEN_SECRET can be any string (passphrase, UUID, etc.).
//   - SHA-256 always produces exactly 32 bytes regardless of input length.
//   - It is deterministic: same input always gives same output.
func newCipherBlock(key string) (cipher.Block, error) {
	// SHA-256 the key string to get a fixed 32-byte slice.
	hash := sha256.Sum256([]byte(key))

	// aes.NewCipher accepts 16, 24, or 32 bytes for AES-128, AES-192, AES-256.
	// hash[:] is 32 bytes → AES-256.
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}
	return block, nil
}
