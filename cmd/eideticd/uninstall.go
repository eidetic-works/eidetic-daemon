package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// uninstallService removes the eideticd login-time service and (optionally)
// the local data directory. Inverse of installService.
//
// Order matters: stop the service first (otherwise the daemon keeps re-creating
// state.json and engrams.db files), then remove the service unit, then offer
// data deletion.
//
// purge=true skips the interactive confirm and deletes <dataDir>. Use sparingly.
func uninstallService(dataDir string, purge bool) error {
	switch runtime.GOOS {
	case "darwin":
		if err := uninstallMacOS(); err != nil {
			return err
		}
	case "linux":
		if err := uninstallLinux(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported platform %q — remove the service manually", runtime.GOOS)
	}

	fmt.Println()
	fmt.Println("uninstall: service unregistered + daemon stopped.")
	fmt.Println()

	// Data deletion is destructive — gate behind a flag OR an interactive y/N.
	if !purge {
		fmt.Printf("Delete local data at %s? (engrams.db, state, auth/bridge tokens, sync-state) [y/N]: ", dataDir)
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("uninstall: data preserved at", dataDir)
			fmt.Println("uninstall: re-install any time with `eideticd -install`")
			printHomebrewHint()
			return nil
		}
	}

	if err := safeRemoveDataDir(dataDir); err != nil {
		return err
	}
	fmt.Printf("uninstall: removed %s\n", dataDir)
	printHomebrewHint()
	return nil
}

// safeRemoveDataDir validates that `dataDir` looks like a legitimate eidetic
// data directory before recursive-deleting it. Guards against catastrophic
// accidents such as `EIDETIC_DATA_DIR=$HOME eideticd -uninstall -purge`
// nuking the entire home directory.
//
// Required invariants (ALL must hold):
//   - dataDir is non-empty after cleaning
//   - dataDir is NOT "/" or "." or a single segment ("just a filename")
//   - filepath.Base(dataDir) == ".eidetic" (the canonical install dirname)
//   - dataDir resolves under the current user's home directory
//
// Any failure aborts with a non-zero exit (returned error). Tests cover
// the refusal paths.
//
// Audit ref: CRITICAL `cmd/eideticd/uninstall.go:54`.
func safeRemoveDataDir(dataDir string) error {
	if dataDir == "" {
		return fmt.Errorf("uninstall: refusing to purge: dataDir is empty")
	}
	cleaned := filepath.Clean(dataDir)
	if cleaned == "/" || cleaned == "." || cleaned == ".." {
		return fmt.Errorf("uninstall: refusing to purge dangerous path %q", cleaned)
	}
	if filepath.Base(cleaned) != ".eidetic" {
		return fmt.Errorf("uninstall: refusing to purge %q: basename must be \".eidetic\" "+
			"(set EIDETIC_DATA_DIR back to its default or rename the directory before purging)", cleaned)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("uninstall: refusing to purge: cannot resolve home dir: %w", err)
	}
	// Resolve symlinks on both sides so a symlinked tempdir under HOME still
	// passes. EvalSymlinks fails on a missing path, so fall back to the
	// cleaned path if the dir was already removed mid-flow.
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		resolvedHome = home
	}
	resolvedDir, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		resolvedDir = cleaned
	}
	// Use filepath.Rel as the containment check — robust against trailing
	// slashes and intermediate "..". A safe child path produces a rel that
	// neither starts with ".." nor is absolute.
	rel, err := filepath.Rel(resolvedHome, resolvedDir)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("uninstall: refusing to purge %q: not inside user home %q", cleaned, resolvedHome)
	}
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("remove dataDir %s: %w", dataDir, err)
	}
	return nil
}

func printHomebrewHint() {
	fmt.Println()
	fmt.Println("If installed via Homebrew, also run:")
	fmt.Println("  brew uninstall eideticd")
}

func uninstallMacOS() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	uid := strconv.Itoa(os.Getuid())

	// bootout returns non-zero when the job isn't loaded — that's fine.
	bootoutErr := exec.Command("launchctl", "bootout", "gui/"+uid+"/"+launchdLabel).Run()
	if bootoutErr == nil {
		fmt.Printf("uninstall: launchctl — stopped %s\n", launchdLabel)
	} else {
		fmt.Printf("uninstall: launchctl — job was not loaded (ok)\n")
	}

	if _, statErr := os.Stat(plist); statErr == nil {
		if err := os.Remove(plist); err != nil {
			return fmt.Errorf("remove plist: %w", err)
		}
		fmt.Printf("uninstall: removed %s\n", plist)
	}
	return nil
}

func uninstallLinux() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", systemdUnit)

	// disable --now stops + disables in one call.
	out, err := exec.Command("systemctl", "--user", "disable", "--now", systemdUnit).CombinedOutput()
	if err != nil {
		// If the unit was never installed, systemctl returns non-zero — log + continue.
		fmt.Printf("uninstall: systemctl — unit was not active or not installed: %s\n", strings.TrimSpace(string(out)))
	} else {
		fmt.Printf("uninstall: systemctl — stopped + disabled %s\n", systemdUnit)
	}

	if _, statErr := os.Stat(unitPath); statErr == nil {
		if err := os.Remove(unitPath); err != nil {
			return fmt.Errorf("remove unit file: %w", err)
		}
		fmt.Printf("uninstall: removed %s\n", unitPath)
	}

	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		// Non-fatal — daemon-reload failures don't undo what we already did.
		fmt.Printf("uninstall: warning: daemon-reload failed: %s\n", strings.TrimSpace(string(out)))
	}
	return nil
}
