//go:build race

package capture

// isRaceMode is set by build-tag split. When true, latency-sensitive tests
// use a relaxed margin to absorb -race detector overhead (typically 2-20×).
// See race_off.go.
const isRaceMode = true
