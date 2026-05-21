package auth_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/auth"
)

// TestGenerateProducesUnique64HexChars: token shape contract (32 bytes
// of entropy → 64 hex chars). Two consecutive Generate() calls must not
// collide under any reasonable test budget.
func TestGenerateProducesUnique64HexChars(t *testing.T) {
	a, err := auth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 64 {
		t.Errorf("token len: got %d, want 64", len(a))
	}
	if a == b {
		t.Errorf("two Generate() calls collided: %s", a)
	}
}

// TestWriteFileSetsCorrectPermissions: 0600 owner-only enforcement
// (defense-in-depth on top of dataDir's 0700).
func TestWriteFileSetsCorrectPermissions(t *testing.T) {
	dir := t.TempDir()
	tok, _ := auth.Generate()
	path, err := auth.WriteFile(dir, tok)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != auth.TokenFileName {
		t.Errorf("path basename: got %q, want %q", filepath.Base(path), auth.TokenFileName)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != auth.TokenFilePerm {
		t.Errorf("perm: got %o, want %o", perm, auth.TokenFilePerm)
	}
}

// TestWriteFileRotatesOnRewrite: second WriteFile call replaces
// content (not appends). Models the daemon-restart token-rotation
// contract.
func TestWriteFileRotatesOnRewrite(t *testing.T) {
	dir := t.TempDir()
	tok1, _ := auth.Generate()
	tok2, _ := auth.Generate()
	if tok1 == tok2 {
		t.Skip("rare collision; re-run")
	}
	if _, err := auth.WriteFile(dir, tok1); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.WriteFile(dir, tok2); err != nil {
		t.Fatal(err)
	}
	got, err := auth.ReadFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != tok2 {
		t.Errorf("rotation failed: read %q, want %q (was rotated to)", got, tok2)
	}
}

// TestReadFileMissingReturnsError: missing token file → error (clients
// can detect "auth not enabled" vs "auth enabled but I'm unauthorized").
func TestReadFileMissingReturnsError(t *testing.T) {
	dir := t.TempDir()
	if _, err := auth.ReadFile(dir); err == nil {
		t.Fatal("ReadFile on empty dataDir: expected error, got nil")
	}
}

// TestTokenSetGetEnabled: Set/Get round-trip + Enabled() reflects state.
func TestTokenSetGetEnabled(t *testing.T) {
	tok := &auth.Token{}
	if tok.Enabled() {
		t.Error("fresh Token: Enabled() should be false")
	}
	tok.Set("abc123")
	if !tok.Enabled() {
		t.Error("after Set: Enabled() should be true")
	}
	if got := tok.Get(); got != "abc123" {
		t.Errorf("Get: got %q, want abc123", got)
	}
	tok.Set("")
	if tok.Enabled() {
		t.Error("after Set(empty): Enabled() should be false")
	}
}

// TestValidateAcceptsBearerAndBare: both header forms (Bearer-prefix
// and bare token) work. Bare convenience for shell users; Bearer is
// the standard.
func TestValidateAcceptsBearerAndBare(t *testing.T) {
	tok := &auth.Token{}
	tok.Set("good-token-aaaa-bbbb-cccc-dddd")
	cases := []string{
		"Bearer good-token-aaaa-bbbb-cccc-dddd",
		"good-token-aaaa-bbbb-cccc-dddd",
		" Bearer good-token-aaaa-bbbb-cccc-dddd ",
	}
	for _, c := range cases {
		if err := tok.Validate(c); err != nil {
			t.Errorf("Validate(%q): got error %v, want nil", c, err)
		}
	}
}

// TestValidateRejectsMismatchAndEmpty: wrong-token / empty-header /
// disabled-token cases all error.
func TestValidateRejectsMismatchAndEmpty(t *testing.T) {
	tok := &auth.Token{}
	if err := tok.Validate("Bearer anything"); err == nil {
		t.Error("Validate on disabled token: expected error")
	}
	tok.Set("good-token-aaaa-bbbb-cccc-dddd")
	cases := []string{
		"",
		"Bearer ",
		"Bearer wrong-token-aaaa-bbbb-cccc-dddd",
		"good-token-aaaa-bbbb-cccc-dddZ", // last char differs
		"short",                          // length mismatch
	}
	for _, c := range cases {
		if err := tok.Validate(c); err == nil {
			t.Errorf("Validate(%q): expected error, got nil", c)
		}
	}
}

// TestMiddlewarePassesThroughWhenDisabled: regression — disabled token
// must not break any pre-v0.0.9 caller.
func TestMiddlewarePassesThroughWhenDisabled(t *testing.T) {
	tok := &auth.Token{} // disabled
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	wrapped := tok.Middleware(inner, "/healthz")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/engrams", nil)
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("disabled middleware blocked request: got %d", rec.Code)
	}
}

// TestMiddlewareGatesProtectedRoutes: enabled + missing token → 401.
func TestMiddlewareGatesProtectedRoutes(t *testing.T) {
	tok := &auth.Token{}
	tok.Set("active-token-aaaa-bbbb-cccc-dddd")
	wrapped := tok.Middleware(http.NotFoundHandler(), "/healthz")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("protected route w/o token: got %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("missing WWW-Authenticate hint on 401")
	}
}

// TestMiddlewareOpenPathBypasses: /healthz must stay open even with
// auth enabled (liveness probe contract).
func TestMiddlewareOpenPathBypasses(t *testing.T) {
	tok := &auth.Token{}
	tok.Set("active-token-aaaa-bbbb-cccc-dddd")
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	wrapped := tok.Middleware(inner, "/healthz")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil) // no Authorization header
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("open path blocked: got %d, want 200", rec.Code)
	}
}

// TestMiddlewareValidTokenPassesThrough: positive case.
func TestMiddlewareValidTokenPassesThrough(t *testing.T) {
	tok := &auth.Token{}
	tok.Set("active-token-aaaa-bbbb-cccc-dddd")
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	wrapped := tok.Middleware(inner, "/healthz")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/engrams?surface=claude_code", nil)
	req.Header.Set("Authorization", "Bearer active-token-aaaa-bbbb-cccc-dddd")
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("valid token blocked: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestValidateAcceptsBearerCaseInsensitive — RFC 7235 §2.1 requires
// case-insensitive scheme matching. curl and many client libraries send
// `bearer` lower-case; the pre-fix `strings.TrimPrefix(got, "Bearer ")`
// was case-sensitive and rejected them.
//
// Audit ref: HIGH `internal/auth/auth.go:133`.
func TestValidateAcceptsBearerCaseInsensitive(t *testing.T) {
	tok := &auth.Token{}
	tok.Set("good-token-aaaa-bbbb-cccc-dddd")
	cases := []string{
		"Bearer good-token-aaaa-bbbb-cccc-dddd",
		"bearer good-token-aaaa-bbbb-cccc-dddd",
		"BEARER good-token-aaaa-bbbb-cccc-dddd",
		"BeArEr good-token-aaaa-bbbb-cccc-dddd",
	}
	for _, c := range cases {
		if err := tok.Validate(c); err != nil {
			t.Errorf("Validate(%q) RFC 7235 §2.1 case-insensitive: got %v, want nil", c, err)
		}
	}
}

// TestMiddlewareReturnsGenericUnauthorized — the 401 response body MUST
// be a generic "unauthorized" (no leaking which validation arm failed:
// `token mismatch` vs `missing token` vs `token not set`). Pre-fix
// leakage told an attacker whether auth was even configured.
//
// Audit ref: HIGH `internal/auth/auth.go:186`.
func TestMiddlewareReturnsGenericUnauthorized(t *testing.T) {
	tok := &auth.Token{}
	tok.Set("active-token-aaaa-bbbb-cccc-dddd")
	wrapped := tok.Middleware(http.NotFoundHandler(), "/healthz")

	for _, header := range []string{
		"",                                          // missing
		"Bearer ",                                   // empty bearer
		"Bearer wrong-token-aaaa-bbbb-cccc-dddd",    // mismatch (same length)
		"short",                                     // length mismatch
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/metrics", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("header=%q: got code %d, want 401", header, rec.Code)
		}
		body := rec.Body.String()
		if !contains(body, "unauthorized") {
			t.Errorf("header=%q: body=%q does not contain \"unauthorized\"", header, body)
		}
		// Headline assertion: must NOT leak internal failure mode.
		for _, leak := range []string{"token mismatch", "missing token",
			"token not set", "auth:"} {
			if contains(body, leak) {
				t.Errorf("header=%q: response body leaks internal detail %q: %q",
					header, leak, body)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
