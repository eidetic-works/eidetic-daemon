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
