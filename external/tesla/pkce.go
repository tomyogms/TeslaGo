// Package tesla — pkce.go
//
// PKCE (Proof Key for Code Exchange, pronounced "pixy") is a security extension
// to the OAuth 2.0 Authorization Code flow described in RFC 7636.
//
// Why PKCE?
// ─────────
// In a standard OAuth flow there is a window between:
//  1. The user being redirected to the auth server, and
//  2. The auth server redirecting back with an authorization code.
//
// A malicious app on the same device could intercept that redirect and steal the
// authorization code. PKCE closes this gap by making the client prove it is the
// same party that initiated the flow — without needing a client secret.
//
// How it works (S256 method):
// ───────────────────────────
//  1. Before redirecting the user, we generate a random `code_verifier` (this file).
//  2. We compute `code_challenge = BASE64URL(SHA256(code_verifier))` and send it
//     along with the authorization request.
//  3. We store the `code_verifier` server-side (in memory, keyed by `state`).
//  4. When Tesla calls our callback with the authorization code, we send BOTH
//     the code AND the original `code_verifier` to the token endpoint.
//  5. Tesla recomputes SHA256(code_verifier) and checks it matches the
//     code_challenge from step 2. Only we could have produced that verifier.
package tesla

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// PKCEChallenge bundles the two values that must be kept together during an
// OAuth PKCE flow:
//   - CodeVerifier  → stored server-side, sent to the token endpoint later
//   - CodeChallenge → sent to Tesla when building the authorization URL
type PKCEChallenge struct {
	// CodeVerifier is a high-entropy random string (86 base64url chars ≈ 64 bytes).
	// IMPORTANT: keep this secret and server-side only. Never send it to the browser.
	CodeVerifier string

	// CodeChallenge is BASE64URL(SHA256(CodeVerifier)).
	// This is safe to expose publicly — it cannot be reversed to obtain CodeVerifier.
	CodeChallenge string
}

// GeneratePKCE creates a cryptographically random PKCE code verifier and its
// derived S256 code challenge.
//
// We use 64 random bytes (512 bits) for the verifier, which is well above
// RFC 7636's minimum of 43 characters. More entropy = harder to brute force.
func GeneratePKCE() (*PKCEChallenge, error) {
	// crypto/rand.Read fills the slice with cryptographically random bytes.
	// Never use math/rand for security-sensitive values.
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generating random bytes: %w", err)
	}

	// Encode the random bytes to a URL-safe base64 string (no padding).
	// RawURLEncoding omits the '=' padding characters that are not valid in URLs.
	verifier := base64.RawURLEncoding.EncodeToString(b)

	// Compute the challenge: BASE64URL(SHA256(verifier))
	// sha256.Sum256 returns a [32]byte array; [:] converts it to a slice.
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	return &PKCEChallenge{
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
	}, nil
}

// GenerateState creates a random OAuth `state` parameter.
//
// The state parameter serves two purposes in OAuth:
//  1. CSRF protection: we check that the state in the callback matches what we
//     sent, preventing cross-site request forgery attacks.
//  2. Context carrier: we encode the admin_id into the state so when Tesla
//     calls our callback we know which admin initiated the flow.
//     (see handler: compositeState = state + "." + adminID)
//
// 16 random bytes → 128 bits of entropy, more than enough for a one-time token.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	// TrimRight removes trailing '=' padding — cleaner URLs.
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "="), nil
}
