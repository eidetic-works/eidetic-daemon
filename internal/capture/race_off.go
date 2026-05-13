//go:build !race

package capture

// isRaceMode is set by build-tag split. When false, latency-sensitive tests
// use a tighter margin closer to the spec's 50ms target. See race_on.go.
const isRaceMode = false
