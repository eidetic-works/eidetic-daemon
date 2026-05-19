//go:build linux

package capture

import "path/filepath"

// claudeRoot returns the Claude Code projects directory on Linux.
func claudeRoot(home string) string {
	return filepath.Join(home, ".claude", "projects")
}

// cursorRoot for Linux. Cursor's Linux storage layout.
func cursorRoot(home string) string {
	return filepath.Join(home, ".config", "Cursor", "User", "workspaceStorage")
}
