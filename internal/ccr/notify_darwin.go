package ccr

import (
	"log"
	"os/exec"
)

// Notify sends a macOS desktop notification.
// It attempts to use terminal-notifier (UNUserNotification) if available,
// and falls back to osascript.
func Notify(title, message string) {
	// Try terminal-notifier first (UNUserNotification)
	if _, err := exec.LookPath("terminal-notifier"); err == nil {
		cmd := exec.Command("terminal-notifier", "-title", title, "-message", message)
		if err := cmd.Run(); err == nil {
			return
		}
		log.Printf("ccr notify: terminal-notifier failed, falling back to osascript")
	}

	// Fallback to osascript
	script := `display notification "` + message + `" with title "` + title + `"`
	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		log.Printf("ccr notify: osascript failed: %v", err)
	}
}
