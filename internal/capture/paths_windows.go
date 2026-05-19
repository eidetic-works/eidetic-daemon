//go:build windows

package capture

import (
	"os"
	"path/filepath"
)

// claudeRoot returns the Claude Code projects directory on Windows.
// Claude Code (Electron) stores session JSONLs under APPDATA\Claude\projects.
func claudeRoot(home string) string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		appdata = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appdata, "Claude", "projects")
}

// cursorRoot returns the Cursor workspace storage directory on Windows.
func cursorRoot(home string) string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		appdata = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appdata, "Cursor", "User", "workspaceStorage")
}
