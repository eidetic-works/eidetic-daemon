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

	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("remove dataDir %s: %w", dataDir, err)
	}
	fmt.Printf("uninstall: removed %s\n", dataDir)
	printHomebrewHint()
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
