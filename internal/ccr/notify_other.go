//go:build !darwin

package ccr

// Notify is a no-op stub on non-darwin platforms.
// The darwin implementation (notify_darwin.go) uses terminal-notifier /
// osascript to surface a desktop notification when the CCR broker fires a
// wake event. Other platforms do not yet have an equivalent path; the broker
// remains functional and the wake-event signal still propagates to subscribers
// via the unix-socket / websocket / MCP-stdio transports.
//
// When a non-darwin notification path is desired, replace this stub with a
// platform-specific implementation (e.g. notify-send on linux, toast on
// windows) and add the appropriate build constraints.
func Notify(title, message string) {
	// intentional no-op
	_ = title
	_ = message
}
