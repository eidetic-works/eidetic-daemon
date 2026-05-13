// Package capture watches per-surface filesystem locations, tails new bytes
// as they arrive, and produces engram rows for the writer pool.
//
// A Parser owns the bytes-on-disk → []Engram conversion for ONE surface.
// A Watcher multiplexes fsnotify events across all configured parsers.
//
// State (last-known offset per file) is persisted to ~/.eidetic/state.json
// so a daemon restart resumes from the last committed offset rather than
// re-ingesting the whole file.
package capture

import "github.com/eidetic-works/eidetic-daemon/internal/engram"

// Parser converts bytes-on-disk for ONE surface into engram rows.
//
// Parse reads from `path` starting at `fromOffset` (inclusive) and returns
// the engrams produced + the new offset to record (i.e., bytes consumed).
//
// Parse must be:
//   - idempotent at offset boundaries (calling twice with the same fromOffset
//     yields the same engrams)
//   - safe against partial writes (if last record is incomplete, do NOT consume
//     it; leave it for the next Parse call)
//   - tolerant of file truncation / replacement (if file size < fromOffset,
//     reset to 0 and emit the whole file)
type Parser interface {
	Surface() string
	Parse(path string, fromOffset int64) (engrams []engram.Engram, newOffset int64, err error)
}

// SurfaceConfig pairs a parser with the directory + glob pattern fsnotify
// watches for that surface.
type SurfaceConfig struct {
	Surface string
	Root    string
	Glob    string
	Parser  Parser
}
