// Package engram defines the canonical record shape captured + retrieved by
// the daemon. Surface tags name the upstream tool (cursor, cowork, claude_code, ...).
// Timestamps are unix epoch nanoseconds for monotonic ordering. Meta is a free-form
// JSON blob owned by per-surface parsers (source path, file offset, parser version).
package engram

type Engram struct {
	ID      int64  `json:"id"`
	Surface string `json:"surface"`
	TS      int64  `json:"ts"`
	Payload string `json:"payload"`
	Meta    string `json:"meta,omitempty"`
	// Snippet is populated only by Search — ~200-char FTS5 context window
	// around the match. Empty for all other retrieval paths.
	Snippet string `json:"snippet,omitempty"`
}
