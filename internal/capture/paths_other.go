//go:build !darwin

package capture

import "path/filepath"

// cursorRoot for non-mac hosts. Cursor's Linux/Windows storage layout differs
// per their packaging; W1 ships a best-guess Linux path and falls through
// gracefully when the dir is absent.
func cursorRoot(home string) string {
	return filepath.Join(home, ".config", "Cursor", "User", "workspaceStorage")
}
