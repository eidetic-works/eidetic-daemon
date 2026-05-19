package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// installService registers eideticd as a login-time service using the
// platform's native service manager: launchd on macOS, systemd-user on Linux.
// Returns nil on success and prints progress to stdout. On failure it returns
// an error with enough context for the user to fix the problem manually.
//
// The binary path embedded in the service unit is resolved via os.Executable()
// so this works regardless of install prefix (/usr/local/bin, Homebrew, PATH).
func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	// Follow symlinks (Homebrew wraps binaries in a shim symlink).
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	switch runtime.GOOS {
	case "darwin":
		return installMacOS(exe)
	case "linux":
		return installLinux(exe)
	default:
		return fmt.Errorf("unsupported platform %q — register the service manually", runtime.GOOS)
	}
}

const launchdLabel = "works.eidetic.eideticd"

func installMacOS(exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	agentDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("mkdir LaunchAgents: %w", err)
	}
	plist := filepath.Join(agentDir, launchdLabel+".plist")

	content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>` + launchdLabel + `</string>
    <key>ProgramArguments</key>
    <array>
        <string>` + exe + `</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/eideticd.out.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/eideticd.err.log</string>
</dict>
</plist>
`
	if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	fmt.Printf("install: wrote %s\n", plist)

	// Unload any stale job silently — ok if it errors (job not loaded yet).
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+launchdLabel).Run()

	out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, plist).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("install: launchd — started %s\n", launchdLabel)
	fmt.Println("install: eideticd will now start automatically at login")
	fmt.Println("install: check health: curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz")
	return nil
}

const systemdUnit = "eideticd.service"

func installLinux(exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("mkdir systemd user dir: %w", err)
	}
	unitPath := filepath.Join(unitDir, systemdUnit)

	content := `[Unit]
Description=Eidetic Works engram daemon
After=default.target

[Service]
Type=simple
ExecStart=` + exe + `
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`
	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	fmt.Printf("install: wrote %s\n", unitPath)

	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", systemdUnit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable --now: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("install: systemd-user — enabled and started %s\n", systemdUnit)
	fmt.Println("install: eideticd will now start automatically at login")
	fmt.Println("install: check health: curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz")
	return nil
}
