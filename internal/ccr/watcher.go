package ccr

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

func RunRelayWatcher(broker *Broker, brainDir string) {
	if brainDir == "" {
		if envBrain := os.Getenv("CCR_BRAIN_PATH"); envBrain != "" {
			brainDir = envBrain
		} else {
			home, _ := os.UserHomeDir()
			brainDir = filepath.Join(home, "ai-mvp-backend", ".brain")
		}
	}

	relayDir := filepath.Join(brainDir, "relay")
	os.MkdirAll(relayDir, 0755)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("ccr serve: failed to start relay watcher: %v", err)
		return
	}
	defer watcher.Close()

	// Watch the relay dir and its immediate subdirectories
	if err := watcher.Add(relayDir); err != nil {
		log.Printf("ccr serve: failed to watch %s: %v", relayDir, err)
	}

	entries, _ := os.ReadDir(relayDir)
	for _, entry := range entries {
		if entry.IsDir() {
			subPath := filepath.Join(relayDir, entry.Name())
			watcher.Add(subPath)
		}
	}

	log.Printf("ccr serve: watching %s for relay events", relayDir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) {
				// E.g. event.Name = /Users/.../.brain/relay/claude_code_test_hold/2026...json
				dir := filepath.Dir(event.Name)
				role := filepath.Base(dir)

				// Optional: only trigger for JSON files
				if strings.HasSuffix(event.Name, ".json") {
					log.Printf("ccr serve: wake event triggered for role %s by file %s", role, event.Name)

					// Re-map common roles to match subscribe payload if necessary
					canonicalRole := role
					if strings.HasPrefix(role, "claude_code_") {
						canonicalRole = strings.TrimPrefix(role, "claude_code_")
					}

					broker.WakeActive(canonicalRole, "relay_arrival", event.Name)

					// Also wake exact match just in case
					broker.WakeActive(role, "relay_arrival", event.Name)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("ccr serve: relay watcher error: %v", err)
		}
	}
}
