package main

import (
	"os"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCaptureMeta_AlwaysHasSource(t *testing.T) {
	meta := buildCaptureMeta()
	var parsed map[string]string
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, meta)
	}
	if parsed["source"] != "cli-capture" {
		t.Errorf("source: got %q, want cli-capture", parsed["source"])
	}
}

func TestBuildCaptureMeta_IncludesHostWhenAvailable(t *testing.T) {
	meta := buildCaptureMeta()
	if !strings.Contains(meta, `"host":`) {
		t.Errorf("expected host in meta: %s", meta)
	}
}

func TestBuildCaptureMeta_ValidJSON(t *testing.T) {
	meta := buildCaptureMeta()
	if !strings.HasPrefix(meta, "{") || !strings.HasSuffix(meta, "}") {
		t.Errorf("not a JSON object: %s", meta)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, meta)
	}
}

func TestGitBranch_NotInRepoReturnsEmpty(t *testing.T) {
	// Force CWD to /tmp which isn't a git repo
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	if err := os.Chdir(os.TempDir()); err != nil {
		t.Skipf("can't chdir to tempdir: %v", err)
	}
	got := gitBranch()
	if got != "" {
		t.Errorf("non-git dir: got %q, want empty", got)
	}
}
