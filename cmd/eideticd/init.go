package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	eidetic_sync "github.com/eidetic-works/eidetic-daemon/internal/sync"
)

// udsDialer returns a DialContext that forces every connection to the given
// Unix-domain socket — lets http.Client speak to the daemon via UDS while
// still using http://localhost/ URLs.
func udsDialer(path string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, _ string, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
}

// initWizard walks a fresh user through first-run setup:
//   1. Confirm + create dataDir (0700)
//   2. Register the service (or print one-liner)
//   3. Detect installed AI tools (Claude Code, Cursor, Cowork dirs) and report
//   4. Generate a sample auth token if EIDETIC_AUTH wanted
//   5. Optionally accept Pro sync.json from input (paste-in flow)
//   6. Smoke-test: hit /healthz on the running daemon
//
// Non-interactive mode (-yes flag) skips prompts and uses sensible defaults.
//
// The wizard is idempotent — re-running on an already-initialized install just
// re-prints status without overwriting anything.
func initWizard(dataDir string, nonInteractive bool) error {
	reader := bufio.NewReader(os.Stdin)
	prompt := func(question, dflt string) string {
		if nonInteractive {
			return dflt
		}
		if dflt != "" {
			fmt.Printf("%s [%s]: ", question, dflt)
		} else {
			fmt.Printf("%s: ", question)
		}
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(ans)
		if ans == "" {
			return dflt
		}
		return ans
	}
	yesNo := func(question string, dfltYes bool) bool {
		if nonInteractive {
			return dfltYes
		}
		hint := "[Y/n]"
		if !dfltYes {
			hint = "[y/N]"
		}
		fmt.Printf("%s %s: ", question, hint)
		ans, _ := reader.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans == "" {
			return dfltYes
		}
		return ans == "y" || ans == "yes"
	}

	fmt.Printf("eideticd %s — first-run setup\n\n", Version)

	// Step 1: dataDir
	fmt.Printf("Step 1/6: data directory\n")
	dataDir = prompt("Where should engrams.db + state live?", dataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("mkdir dataDir: %w", err)
	}
	if fi, err := os.Stat(dataDir); err == nil {
		fmt.Printf("  ✓ %s (mode %o)\n\n", dataDir, fi.Mode().Perm())
	}

	// Step 2: detect surfaces
	fmt.Printf("Step 2/6: detecting AI tool surfaces\n")
	home, _ := os.UserHomeDir()
	surfaces := []struct {
		name string
		path string
	}{
		{"claude_code", filepath.Join(home, ".claude", "projects")},
		{"cursor", filepath.Join(home, "Library", "Application Support", "Cursor", "User", "workspaceStorage")},
		{"cowork", filepath.Join(home, ".cowork", "sessions")},
	}
	for _, s := range surfaces {
		if _, err := os.Stat(s.path); err == nil {
			fmt.Printf("  ✓ %-15s %s\n", s.name, s.path)
		} else {
			fmt.Printf("  − %-15s (not found — skipped)\n", s.name)
		}
	}
	fmt.Println()

	// Step 3: register service
	fmt.Printf("Step 3/6: register as login-time service?\n")
	if yesNo("Run `eideticd -install` now?", true) {
		if err := installService(); err != nil {
			fmt.Printf("  ⚠ install failed: %v\n", err)
			fmt.Printf("  → you can re-run manually: eideticd -install\n")
		}
	} else {
		fmt.Printf("  skipped — register later with: eideticd -install\n")
	}
	fmt.Println()

	// Step 4: optional Bearer auth
	fmt.Printf("Step 4/6: caller authentication (optional)\n")
	if yesNo("Enable Bearer-token auth on the daemon's UDS API?", false) {
		token, err := generateToken()
		if err != nil {
			fmt.Printf("  ⚠ token generation failed: %v\n", err)
		} else {
			tokenPath := filepath.Join(dataDir, "auth-token")
			if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
				fmt.Printf("  ⚠ writing token failed: %v\n", err)
			} else {
				fmt.Printf("  ✓ token at %s (0600)\n", tokenPath)
				fmt.Printf("  → start the daemon with EIDETIC_AUTH=1 or -auth flag\n")
			}
		}
	} else {
		fmt.Printf("  skipped — UDS-trust model active (single-user, single-host)\n")
	}
	fmt.Println()

	// Step 5: Pro sync.json
	fmt.Printf("Step 5/6: Cloud sync (Pro)\n")
	syncPath := filepath.Join(dataDir, "sync.json")
	if _, err := os.Stat(syncPath); err == nil {
		fmt.Printf("  ✓ sync.json already present — daemon will pick it up via hot-reload\n")
	} else if yesNo("Do you have a Pro sync.json to paste in?", false) {
		fmt.Println("  Paste the full sync.json contents below, then a blank line:")
		var content strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			if strings.TrimSpace(line) == "" && content.Len() > 0 {
				break
			}
			content.WriteString(line)
		}
		// Audit ref: MEDIUM `cmd/eideticd/init.go:158-169` — pre-fix
		// accepted any valid JSON (including `{}`) and wrote it 0600;
		// daemon hot-reloaded into a broken state. Validate via the
		// sync package's own LoadConfig round-trip: write to a temp
		// file, ask sync.LoadConfig to parse it, only promote to the
		// real path on success.
		if err := validatePastedSyncJSON(content.String()); err != nil {
			fmt.Printf("  ⚠ pasted content invalid: %v\n", err)
			fmt.Printf("  → save corrected sync.json manually at %s\n", syncPath)
		} else {
			if err := os.WriteFile(syncPath, []byte(content.String()), 0o600); err != nil {
				fmt.Printf("  ⚠ write failed: %v\n", err)
			} else {
				fmt.Printf("  ✓ sync.json saved (0600). Daemon will start syncing within ~1s of next start.\n")
			}
		}
	} else {
		fmt.Printf("  skipped — get Pro at https://eideticworks.gumroad.com/l/eidetic-pro\n")
	}
	fmt.Println()

	// Step 6: smoke test
	fmt.Printf("Step 6/6: smoke test\n")
	// Give launchd a moment to actually start the daemon if step 3 just registered it.
	time.Sleep(2 * time.Second)
	healthURL := "http://localhost/healthz"
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: nil, // default
		},
		Timeout: 3 * time.Second,
	}
	// Use the platform default socket
	sock := defaultUDSPath
	tr := &http.Transport{
		DialContext: udsDialer(sock),
	}
	client.Transport = tr
	resp, err := client.Get(healthURL)
	if err != nil {
		fmt.Printf("  ⚠ daemon not reachable yet at %s: %v\n", sock, err)
		fmt.Printf("  → try again in a few seconds: curl --unix-socket %s http://localhost/healthz\n", sock)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			fmt.Printf("  ✓ %s → 200 OK %s\n", sock, strings.TrimSpace(string(body)))
		} else {
			fmt.Printf("  ⚠ %s → %d\n", sock, resp.StatusCode)
		}
	}

	fmt.Println()
	fmt.Println("Setup complete. Useful next commands:")
	fmt.Println("  eideticd --stats          # what's been captured")
	fmt.Println("  eideticd --check          # diagnose cloud sync (if Pro)")
	fmt.Println("  eideticd --backups        # cloud upload history")
	fmt.Println("  pip install eidetic-mcp   # MCP bridge for Claude Code / Cursor")
	fmt.Println()
	fmt.Println("Docs: https://eidetic.works/dashboard · https://github.com/eidetic-works/eidetic-daemon")
	return nil
}

// generateToken returns 32 random bytes URL-base64-encoded (43 chars).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// validatePastedSyncJSON parses the wizard-pasted sync.json content via
// the sync package's own LoadConfig contract (round-trip through a temp
// dir). Required: well-formed JSON + the three mandatory fields
// (worker_url, api_key, device_id). Returns nil iff sync.LoadConfig
// would accept it.
//
// Audit ref: MEDIUM `cmd/eideticd/init.go:158-169`.
func validatePastedSyncJSON(content string) error {
	// Fast JSON-parse first so a totally garbled paste produces a tight
	// error rather than triggering temp-file IO.
	var probe map[string]any
	if err := json.Unmarshal([]byte(content), &probe); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	tmp, err := os.MkdirTemp("", "eideticd-sync-validate-*")
	if err != nil {
		return fmt.Errorf("create validation tmpdir: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "sync.json"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("write tmp sync.json: %w", err)
	}
	if _, err := eidetic_sync.LoadConfig(tmp); err != nil {
		return err
	}
	return nil
}
