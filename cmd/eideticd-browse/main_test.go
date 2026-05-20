package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// newTestModel boots a fresh on-disk store under t.TempDir() and seeds it
// with three engrams across two surfaces. Avoids any t.Skip path so the
// test fails loud if the store API drifts under us.
func newTestModel(t *testing.T) *model {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "engrams.db"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Now().UnixNano()
	fixtures := []engram.Engram{
		{Surface: "claude_code", TS: now - int64(3*time.Minute), Payload: "alpha planning notes"},
		{Surface: "claude_code", TS: now - int64(2*time.Minute), Payload: "beta refactor draft"},
		{Surface: "cursor", TS: now - int64(time.Minute), Payload: "gamma cursor session"},
	}
	for _, e := range fixtures {
		if _, err := st.Insert(context.Background(), e); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}
	return newModel(st)
}

// TestUpdateTabSwitch covers the 1/2/3 routing — a regression here would
// silently strand the user on Recent and the only way to notice is to
// dogfood the binary, so the cheap unit test is worth it.
func TestUpdateTabSwitch(t *testing.T) {
	m := newTestModel(t)
	if m.view != viewRecent {
		t.Fatalf("default view = %d, want viewRecent (%d)", m.view, viewRecent)
	}

	cases := []struct {
		key  string
		want int
	}{
		{"2", viewSearch},
		{"3", viewAsk},
		{"1", viewRecent},
	}
	for _, tc := range cases {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
		m = mm.(*model)
		if m.view != tc.want {
			t.Errorf("after key %q: view = %d, want %d", tc.key, m.view, tc.want)
		}
	}
}

// TestCycleSurface walks the surface filter wheel and asserts the cursor
// resets — the cursor-reset bug (cursor lingering past a smaller result
// set) is the actual user-visible failure mode that motivated this test.
func TestCycleSurface(t *testing.T) {
	m := newTestModel(t)
	m.cursor = 2 // pretend the user navigated down before filtering

	want := []string{"claude_code", "cursor", "cowork", ""}
	for i, w := range want {
		m.cycleSurface()
		if got := m.currentSurface(); got != w {
			t.Errorf("cycle %d: surface = %q, want %q", i, got, w)
		}
		if m.cursor != 0 {
			t.Errorf("cycle %d: cursor = %d, want 0 (cycleSurface must reset)", i, m.cursor)
		}
	}
}

// TestRecentLoaded verifies the startup refreshRecent path actually queried
// the seeded store. Without this it's possible to ship a model with an
// empty engrams slice and a green test suite.
func TestRecentLoaded(t *testing.T) {
	m := newTestModel(t)
	if len(m.engrams) != 3 {
		t.Fatalf("loaded %d engrams, want 3", len(m.engrams))
	}
}

// TestEngramsLoadedMsg checks the async-load reducer: an error path leaves
// engrams untouched (so a flaky read doesn't blank the panel), and a clean
// payload replaces them.
func TestEngramsLoadedMsg(t *testing.T) {
	m := newTestModel(t)
	original := m.engrams

	// Error path: list untouched.
	mm, _ := m.Update(engramsLoadedMsg{err: context.DeadlineExceeded})
	m = mm.(*model)
	if len(m.engrams) != len(original) {
		t.Errorf("error path mutated engrams: now %d, want %d", len(m.engrams), len(original))
	}
	if m.err == nil {
		t.Errorf("error path did not record err")
	}

	// Success path: list replaced.
	replacement := []engram.Engram{{ID: 99, Surface: "cowork", TS: time.Now().UnixNano(), Payload: "x"}}
	mm, _ = m.Update(engramsLoadedMsg{engrams: replacement})
	m = mm.(*model)
	if len(m.engrams) != 1 || m.engrams[0].ID != 99 {
		t.Errorf("success path failed to replace engrams: %+v", m.engrams)
	}
	if m.err != nil {
		t.Errorf("success path left err set: %v", m.err)
	}
}

// TestDetailToggle exercises the modal: enter opens it, esc closes it,
// and j/k scroll only while open. The detail pane has been the most
// fragile piece in dogfooding so far — keep this test honest.
func TestDetailToggle(t *testing.T) {
	m := newTestModel(t)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(*model)
	if !m.detailOpen {
		t.Fatal("enter should open detail")
	}

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = mm.(*model)
	if m.detailScroll != 1 {
		t.Errorf("j should scroll detail: got %d", m.detailScroll)
	}

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(*model)
	if m.detailOpen {
		t.Fatal("esc should close detail")
	}
	if m.detailScroll != 0 {
		t.Errorf("close should reset scroll, got %d", m.detailScroll)
	}
}

// TestHumanizeTS sanity-checks the relative timestamp formatter. Locale-
// independent thresholds keep this from flaking on CI runners with weird
// TZ settings.
func TestHumanizeTS(t *testing.T) {
	now := time.Now().UnixNano()
	cases := []struct {
		ts     int64
		prefix string
	}{
		{now - int64(5*time.Second), "5s ago"},
		{now - int64(2*time.Minute), "2m ago"},
		{now - int64(3*time.Hour), "3h ago"},
		{0, "—"},
	}
	for _, tc := range cases {
		got := humanizeTS(tc.ts)
		if got != tc.prefix {
			t.Errorf("humanizeTS(%d) = %q, want %q", tc.ts, got, tc.prefix)
		}
	}
}
