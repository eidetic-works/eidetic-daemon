//go:build darwin

package capture

import "path/filepath"

// claudeRoot returns the Claude Code projects directory on macOS.
func claudeRoot(home string) string {
	return filepath.Join(home, ".claude", "projects")
}

func cursorRoot(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "workspaceStorage")
}
