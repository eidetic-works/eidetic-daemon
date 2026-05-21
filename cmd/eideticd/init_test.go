package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	tok, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 {
		t.Errorf("token length: got %d, want 43 (32-byte base64 raw URL)", len(tok))
	}
	// Should decode cleanly
	if _, err := base64.RawURLEncoding.DecodeString(tok); err != nil {
		t.Errorf("token not valid base64-raw-url: %v", err)
	}
	// Should not contain pad or url-unsafe chars
	if strings.ContainsAny(tok, "=+/") {
		t.Errorf("token has pad or url-unsafe chars: %q", tok)
	}
}

func TestGenerateTokenUniqueness(t *testing.T) {
	seen := make(map[string]bool, 32)
	for i := 0; i < 32; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatalf("duplicate token on iteration %d: %q", i, tok)
		}
		seen[tok] = true
	}
}

// TestValidatePastedSyncJSONAccepts — well-formed config with all
// required fields passes.
func TestValidatePastedSyncJSONAccepts(t *testing.T) {
	good := `{"worker_url":"https://example.workers.dev","api_key":"k","device_id":"dev01"}`
	if err := validatePastedSyncJSON(good); err != nil {
		t.Errorf("good config rejected: %v", err)
	}
	// Optional sync_interval + team_id also accepted.
	full := `{"worker_url":"https://example.workers.dev","api_key":"k","device_id":"dev01","sync_interval":60,"team_id":"acme-eng"}`
	if err := validatePastedSyncJSON(full); err != nil {
		t.Errorf("full config rejected: %v", err)
	}
}

// TestValidatePastedSyncJSONRejectsMalformed — non-JSON, empty, partial
// braces all error.
func TestValidatePastedSyncJSONRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"not json at all",
		"{ unclosed",
		`["not an object"]`,
	}
	for _, c := range cases {
		if err := validatePastedSyncJSON(c); err == nil {
			t.Errorf("expected error for %q, got nil", c)
		}
	}
}

// TestValidatePastedSyncJSONRejectsMissingFields — the headline regression
// for the audit finding: pre-fix accepted `{}` (valid JSON but missing
// every required field) and wrote it 0600, leaving the daemon in a broken
// hot-reload state.
func TestValidatePastedSyncJSONRejectsMissingFields(t *testing.T) {
	cases := []string{
		`{}`,
		`{"worker_url":"x"}`,                                  // missing api_key + device_id
		`{"worker_url":"x","api_key":"k"}`,                    // missing device_id
		`{"api_key":"k","device_id":"d"}`,                     // missing worker_url
		`{"worker_url":"","api_key":"k","device_id":"d"}`,     // worker_url empty string
	}
	for _, c := range cases {
		if err := validatePastedSyncJSON(c); err == nil {
			t.Errorf("expected error for missing-fields case %q, got nil", c)
		}
	}
}
