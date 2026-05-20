package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestUninstallPurgeDeletesDataDir verifies the purge=true path removes the
// dataDir contents. We can't easily test the service-manager side without a
// real launchd/systemd, but the data-deletion contract is the part customers
// rely on for "this leaves no trace."
func TestUninstallPurgeDeletesDataDir(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("uninstall service-manager path is platform-specific; data-dir deletion is universal")
	}
	dir := t.TempDir()

	// Seed the dataDir with the kinds of files a real install creates.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
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
