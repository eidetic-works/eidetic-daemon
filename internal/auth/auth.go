// Package auth implements opt-in HTTP Bearer-token authentication for the
// daemon API. Defense-in-depth on top of the UDS 0600 trust boundary —
// prevents other-process-on-same-uid impersonation when EIDETIC_AUTH=1
// (or -auth flag) is set. v0.0.9+.
//
// Token model (per ADR follow-on for v0.0.9 caller authentication):
//   - Random 32-byte token generated at daemon startup (crypto/rand)
//   - Hex-encoded, 64 chars
//   - Written to <dataDir>/auth-token with 0600 permissions
//   - Rotates on every daemon restart (no persistence across restarts —
//     prevents stale-token replay if dataDir is ever world-readable
//     between sessions)
//   - Clients read the token file + send as Authorization: Bearer <token>
//   - /healthz stays OPEN even with auth enabled (liveness probe; no
//     sensitive data; service managers + load-balancers expect this)
//   - /engrams + /metrics require Bearer when auth enabled
//
// Off by default — preserves the W1 single-user trust model documented
// in SECURITY.md. Opt-in: set EIDETIC_AUTH=1 env var OR pass -auth flag.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// TokenLen is the byte-length of generated tokens before hex encoding.
// 32 bytes = 256 bits of entropy = 64 hex chars on disk + wire.
const TokenLen = 32

// TokenFileName is the basename of the token file inside dataDir. Full
// path is filepath.Join(dataDir, TokenFileName).
const TokenFileName = "auth-token"

// TokenFilePerm is the required permission mode for the token file.
// 0600 = owner read+write only. Defense-in-depth on top of dataDir's
// own 0700 mode.
const TokenFilePerm = 0o600

// Token holds an active auth token. Empty Token = auth disabled.
// Compare via constant-time match (Equal); never use == directly.
type Token struct {
	value atomic.Value // string (hex-encoded); empty = disabled
}

// Generate creates a new random token using crypto/rand. Returns the
// hex-encoded string; caller persists via WriteFile + assigns via Set.
func Generate() (string, error) {
	buf := make([]byte, TokenLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: rand read: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// WriteFile persists the token to dataDir/auth-token with 0600. Removes
// any pre-existing file first (rotates cleanly on restart). Returns the
// full path written.
func WriteFile(dataDir, token string) (string, error) {
	if dataDir == "" {
		return "", errors.New("auth: dataDir required")
	}
	if token == "" {
		return "", errors.New("auth: empty token")
	}
	path := filepath.Join(dataDir, TokenFileName)
	// Remove any stale file first — write would clobber but explicit
	// remove makes the rotation intent obvious in the audit log.
	_ = os.Remove(path)
	if err := os.WriteFile(path, []byte(token), TokenFilePerm); err != nil {
		return "", fmt.Errorf("auth: write %s: %w", path, err)
	}
	// Verify perms (some filesystems / umask masks may downgrade).
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("auth: stat %s: %w", path, err)
	}
	if fi.Mode().Perm() != TokenFilePerm {
		_ = os.Chmod(path, TokenFilePerm)
	}
	return path, nil
}

// ReadFile reads the token from dataDir/auth-token. Returns the token
// string + nil on success; "" + error on missing/unreadable. Used by
// clients (bridge, scripts) to discover the active token.
func ReadFile(dataDir string) (string, error) {
	path := filepath.Join(dataDir, TokenFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("auth: read %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Set installs an active token. Empty string disables auth. Atomic so
// future tier-2 work (rotation-mid-flight) doesn't race readers.
func (t *Token) Set(value string) {
	t.value.Store(value)
}

// Get returns the current token value. Empty string = auth disabled.
func (t *Token) Get() string {
	v, _ := t.value.Load().(string)
	return v
}

// Enabled returns true when an active token is set (auth on).
func (t *Token) Enabled() bool {
	return t.Get() != ""
}

// Validate checks an incoming Authorization header value against the
// active token. Returns nil on match, an error otherwise. Constant-time
// comparison via subtle (avoids timing oracle on prefix matches).
//
// Accepted header forms:
//   Authorization: Bearer <token>
//   Authorization: <token>            (bare; convenience for shell users)
func (t *Token) Validate(header string) error {
	want := t.Get()
	if want == "" {
		return errors.New("auth: token not set")
	}
	got := strings.TrimSpace(header)
	got = strings.TrimPrefix(got, "Bearer ")
	got = strings.TrimSpace(got)
	if got == "" {
		return errors.New("auth: missing token")
	}
	// Constant-time compare to avoid timing oracle. Equal-length-mismatch
	// fast-fails to a fixed-length compare to keep the side-channel narrow.
	if len(got) != len(want) {
		return errors.New("auth: token mismatch")
	}
	if subtleConstantTimeEqual(got, want) {
		return nil
	}
	return errors.New("auth: token mismatch")
}

// subtleConstantTimeEqual compares two equal-length strings in constant
// time. Inlined to avoid the crypto/subtle import for one call.
func subtleConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// Middleware returns an http.Handler that gates `next` behind Token
// validation. Open paths (passed in `openPaths`) bypass the gate —
// /healthz is the only open path in v0.0.9. Returns 401 with a
// WWW-Authenticate hint on rejection.
//
// When the Token is disabled (empty), the middleware passes through
// transparently — preserves backward-compatibility for callers that
// don't set EIDETIC_AUTH=1.
func (t *Token) Middleware(next http.Handler, openPaths ...string) http.Handler {
	open := make(map[string]struct{}, len(openPaths))
	for _, p := range openPaths {
		open[p] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !t.Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := open[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		if err := t.Validate(r.Header.Get("Authorization")); err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="eidetic-daemon"`)
			http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
