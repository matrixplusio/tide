// Package ci turns a build pipeline's notification into a release. A pipeline
// tells Tide once that an image is pushed and then ends; everything after
// that — waiting for Kargo to produce the matching freight, building the
// release, and either holding it for a person or starting it — happens here,
// on Tide's clock rather than the runner's.
package ci

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrEnvUnknown  = errors.New("unknown environment")
	ErrCIDisabled  = errors.New("environment does not take releases from CI")
	ErrNotDeployed = errors.New("service is not deployed to this environment")
)

// TokenPrefix marks a Tide CI token in logs and secret scanners. Pipelines
// send it whole in the Authorization header.
const TokenPrefix = "tide_ci_"

// prefixLen is how much of a token is kept in the clear, so a list can say
// which token a row refers to without being able to reconstruct it.
const prefixLen = len(TokenPrefix) + 6

// NewToken mints a token: the plaintext for the person creating it (shown
// once) and the hash to store. 32 bytes of entropy; nothing about the token
// is guessable from what is stored.
func NewToken() (plain, hash, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", err
	}
	plain = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plain, HashToken(plain), plain[:prefixLen], nil
}

// HashToken is the stored form. A plain SHA-256 is right here and bcrypt is
// not: the token has full entropy, so there is nothing to brute-force, and
// every CI request would otherwise pay for a key-stretching round.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// BearerToken pulls the token out of an Authorization header. It returns ""
// for anything that is not a bearer token, including an empty header.
func BearerToken(header string) string {
	const bearer = "bearer "
	if len(header) <= len(bearer) || !strings.EqualFold(header[:len(bearer)], bearer) {
		return ""
	}
	return strings.TrimSpace(header[len(bearer):])
}

// SameToken compares two tokens without leaking where they differ.
func SameToken(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
