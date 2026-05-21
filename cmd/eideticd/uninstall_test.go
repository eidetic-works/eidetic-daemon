package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeFakeHomeWithEidetic creates a temp dir that the test uses as $HOME and
// returns the path to a `.eidetic` subdirectory inside it. Required for the
// new safeRemoveDataDir invariants (basename must be `.eidetic` AND inside
// user home).
func makeFakeHomeWithEidetic(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".eidetic")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

// TestUninstallPurgeDeletesDataDir verifies the purge=true path removes the
// dataDir contents. We can't easily test the service-manager side without a
// real launchd/systemd, but the data-deletion contract is the part customers
// rely on for "this leaves no trace."
func TestUninstallPurgeDeletesDataDir(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("uninstall service-manager path is platform-specific; data-dir deletion is universal")
	}
	dir := makeFakeHomeWithEidetic(t)

	// Seed the dataDir with the kinds of files a real install creates.
	for _, name := range []string{"engrams.db", "state.json", "auth-token", "sync-state.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Run uninstall with purge=true. This will also attempt service teardown,
	// which will silently no-op since nothing was actually installed.
	if err := uninstallService(dir, true); err != nil {
		// Service teardown failures should NOT propagate when there's nothing
		// to uninstall, but if they do, document it for the next read.
		t.Logf("uninstallService returned error (may be expected without real service): %v", err)
	}

	// Critical assertion: dataDir contents are gone.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dataDir still exists after purge: %v", err)
	}
}

// TestSafeRemoveRefusesEmpty rejects empty dataDir.
func TestSafeRemoveRefusesEmpty(t *testing.T) {
	if err := safeRemoveDataDir(""); err == nil {
		t.Fatal("expected error for empty dataDir")
	}
}

// TestSafeRemoveRefusesRoot rejects "/" and "." — catastrophic targets.
func TestSafeRemoveRefusesRoot(t *testing.T) {
	for _, p := range []string{"/", ".", ".."} {
		if err := safeRemoveDataDir(p); err == nil {
			t.Errorf("expected error for dangerous path %q, got nil", p)
		}
	}
}

// TestSafeRemoveRefusesHomeDir is the headline regression for the CRITICAL
// audit finding (`cmd/eideticd/uninstall.go:54`). Setting
// EIDETIC_DATA_DIR=$HOME and running `eideticd -uninstall -purge` would
// previously nuke the entire home directory. After the fix, the basename-
// must-be-`.eidetic` invariant rejects it.
func TestSafeRemoveRefusesHomeDir(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	// Seed home with a sentinel file to prove it survives.
	sentinel := filepath.Join(fakeHome, "do-not-delete.txt")
	if err := os.WriteFile(sentinel, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := safeRemoveDataDir(fakeHome)
	if err == nil {
		t.Fatal("expected safeRemoveDataDir to REFUSE $HOME, got nil error " +
			"(this is the catastrophic-deletion regression)")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("error should mention refusal, got %q", err.Error())
	}
	if _, statErr := os.Stat(sentinel); os.IsNotExist(statErr) {
		t.Fatal("sentinel file deleted — guard FAILED to protect $HOME")
	}
}

// TestSafeRemoveRefusesOutsideHome rejects a `.eidetic`-named dir that
// lives outside $HOME (e.g. someone passes `/tmp/.eidetic`).
func TestSafeRemoveRefusesOutsideHome(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	// Create a `.eidetic` directory under a different temp tree.
	outsideHome := t.TempDir()
	dataDir := filepath.Join(outsideHome, ".eidetic")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dataDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := safeRemoveDataDir(dataDir)
	if err == nil {
		t.Fatal("expected error for .eidetic path outside $HOME")
	}
	if _, statErr := os.Stat(sentinel); os.IsNotExist(statErr) {
		t.Fatal("sentinel deleted — guard failed for outside-home path")
	}
}

// TestSafeRemoveRefusesNonEideticBasename rejects a path under $HOME whose
// basename isn't `.eidetic` (e.g. `~/Documents`).
func TestSafeRemoveRefusesNonEideticBasename(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	docs := filepath.Join(fakeHome, "Documents")
	if err := os.MkdirAll(docs, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(docs, "important.txt")
	if err := os.WriteFile(sentinel, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := safeRemoveDataDir(docs)
	if err == nil {
		t.Fatal("expected error for non-.eidetic basename")
	}
	if _, statErr := os.Stat(sentinel); os.IsNotExist(statErr) {
		t.Fatal("sentinel deleted — guard failed for non-.eidetic basename")
	}
}

// TestSafeRemoveAllowsCanonical accepts the canonical $HOME/.eidetic path.
func TestSafeRemoveAllowsCanonical(t *testing.T) {
	dataDir := makeFakeHomeWithEidetic(t)
	probe := filepath.Join(dataDir, "engrams.db")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := safeRemoveDataDir(dataDir); err != nil {
		t.Fatalf("safeRemoveDataDir on canonical path: %v", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Errorf("dataDir still exists after legitimate purge: %v", err)
	}
}
