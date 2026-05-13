//go:build darwin

package capture

import "path/filepath"

func cursorRoot(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "workspaceStorage")
}
