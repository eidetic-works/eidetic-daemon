// eideticd-browse is a terminal UI for browsing engrams.db. It is a separate
// binary from eideticd so the daemon stays lean (no Bubble Tea / lipgloss in
// the always-on hot path). Reads the store read-only via internal/store —
// safe to run alongside a live daemon thanks to SQLite WAL.
//
// Views (tab via 1/2/3):
//
//	1 Recent  list of last 50 engrams across surfaces
//	2 Search  FTS5 search with debounced input
//	3 Ask     natural-language question → internal/textsearch.QuestionToFTS
//
// Global keys: s cycles surface filter, j/k scroll detail, q quits.
//
// On a non-TTY stdout the binary degrades gracefully with a one-liner pointing
// at --ask / web dashboard rather than crashing the way a raw Bubble Tea
// program would (TTY detection via isatty — already an indirect dep).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
	"github.com/eidetic-works/eidetic-daemon/internal/textsearch"
)

// Version is injected by the build via -ldflags "-X main.Version=<tag>" so
// `eideticd-browse -version` self-identifies the same way the daemon does.
var Version = "dev"

const (
	viewRecent = iota
	viewSearch
	viewAsk
)

// surfaces cycled by the `s` hotkey. "" = all. Kept in the order the user
// most often filters by (claude_code dominates today's ingest).
var surfaceFilters = []string{"", "claude_code", "cursor", "cowork"}

// debounce window after the last keystroke in search before we fire FTS5.
// Short enough to feel live, long enough to avoid 1 query per character.
const searchDebounce = 250 * time.Millisecond

// model is the bubbletea state. Kept private; tests poke it via newModel +
// Update directly rather than driving a real Program.
type model struct {
	st          *store.Store
	view        int
	surfaceIdx  int
	engrams     []engram.Engram
	cursor      int
	detailOpen  bool
	detailScroll int

	// Per-view input state.
	searchInput  string
	searchTyped  time.Time
	askInput     string

	width  int
	height int
	err    error
}

// resolveDBPath mirrors the resolution eideticd uses so we look at the same
// engrams.db whether the daemon ran with EIDETIC_DATA_DIR set or not.
func resolveDBPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if dir := os.Getenv("EIDETIC_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "engrams.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".eidetic", "engrams.db"), nil
}

func main() {
	dbPath := flag.String("db", "", "path to engrams.db (default: $EIDETIC_DATA_DIR/engrams.db or ~/.eidetic/engrams.db)")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *version {
		fmt.Println("eideticd-browse", Version)
		return
	}

	resolved, err := resolveDBPath(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eideticd-browse: resolve db path: %v\n", err)
		os.Exit(1)
	}

	// Non-TTY fallback: a Bubble Tea program against a pipe renders nothing
	// useful and exits with a confusing error. Print a one-liner that points
	// users at the headless paths (--ask / web dashboard) and exit clean.
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		fmt.Println("eideticd-browse needs a TTY; use `eideticd --ask` or the web dashboard for non-TTY contexts.")
		return
	}

	st, err := store.Open(resolved)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eideticd-browse: open store at %s: %v\n", resolved, err)
		os.Exit(1)
	}
	defer st.Close()

	m := newModel(st)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "eideticd-browse: %v\n", err)
		os.Exit(1)
	}
}

// newModel constructs a model with a fresh Recent fetch. Pulled out so tests
// can construct a model against a test-fixture store without booting tea.
func newModel(st *store.Store) *model {
	m := &model{st: st, view: viewRecent, width: 80, height: 24}
	m.refreshRecent()
	return m
}

// ----- Messages -----

// engramsLoadedMsg is dispatched from the async load command. Carries either
// the result slice or an error — never both. err takes precedence in View.
type engramsLoadedMsg struct {
	engrams []engram.Engram
	err     error
}

// debouncedSearchMsg fires after searchDebounce since the last keystroke;
// the Update handler ignores it if the input has changed since the marker
// timestamp, which is the whole point of the debounce.
type debouncedSearchMsg struct {
	at time.Time
}

// ----- Init / Update -----

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case engramsLoadedMsg:
		m.err = msg.err
		if msg.err == nil {
			m.engrams = msg.engrams
			if m.cursor >= len(m.engrams) {
				m.cursor = 0
			}
		}
		return m, nil

	case debouncedSearchMsg:
		// If the user kept typing past this debounce window we drop the fire.
		if !msg.at.Equal(m.searchTyped) {
			return m, nil
		}
		return m, m.runSearchCmd()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey routes by view + global hotkeys. Split out so tests can drive
// keys without going through a real Program (which needs a TTY).
func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Detail modal eats everything except its own close + scroll keys.
	if m.detailOpen {
		switch msg.String() {
		case "esc", "enter", "q":
			m.detailOpen = false
			m.detailScroll = 0
		case "j", "down":
			m.detailScroll++
		case "k", "up":
			if m.detailScroll > 0 {
				m.detailScroll--
			}
		}
		return m, nil
	}

	key := msg.String()

	// Global shortcuts (work in any view, even with focus in the input box).
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "1":
		m.view = viewRecent
		return m, m.refreshRecentCmd()
	case "2":
		m.view = viewSearch
		return m, nil
	case "3":
		m.view = viewAsk
		return m, nil
	}

	// Per-view keys.
	switch m.view {
	case viewRecent:
		return m.handleRecentKey(key)
	case viewSearch:
		return m.handleSearchKey(msg, key)
	case viewAsk:
		return m.handleAskKey(msg, key)
	}
	return m, nil
}

func (m *model) handleRecentKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.engrams)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter":
		if len(m.engrams) > 0 {
			m.detailOpen = true
		}
	case "s":
		m.cycleSurface()
		return m, m.refreshRecentCmd()
	case "/":
		m.view = viewSearch
	case "?":
		m.view = viewAsk
	}
	return m, nil
}

func (m *model) handleSearchKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	// In Search the input box has focus, so q is a literal character — only
	// esc / ctrl+c leave. Up/down still navigate results.
	switch key {
	case "esc":
		m.view = viewRecent
		return m, nil
	case "enter":
		if len(m.engrams) > 0 {
			m.detailOpen = true
		}
		return m, nil
	case "j", "down":
		if m.cursor < len(m.engrams)-1 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "backspace":
		if len(m.searchInput) > 0 {
			m.searchInput = m.searchInput[:len(m.searchInput)-1]
		}
	default:
		// Treat any single rune as input. Bubble Tea exposes the literal
		// runes on KeyRunes when the key is a printable.
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			if msg.Type == tea.KeySpace {
				m.searchInput += " "
			} else {
				m.searchInput += string(msg.Runes)
			}
		} else {
			return m, nil
		}
	}

	// Mark the typing timestamp and schedule a debounce fire.
	m.searchTyped = time.Now()
	at := m.searchTyped
	return m, tea.Tick(searchDebounce, func(time.Time) tea.Msg {
		return debouncedSearchMsg{at: at}
	})
}

func (m *model) handleAskKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.view = viewRecent
		return m, nil
	case "enter":
		// Fire the canonical question→FTS path the daemon's --ask uses.
		return m, m.runAskCmd()
	case "backspace":
		if len(m.askInput) > 0 {
			m.askInput = m.askInput[:len(m.askInput)-1]
		}
	case "j", "down":
		if m.cursor < len(m.engrams)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			if msg.Type == tea.KeySpace {
				m.askInput += " "
			} else {
				m.askInput += string(msg.Runes)
			}
		}
	}
	return m, nil
}

// cycleSurface advances surfaceIdx and resets cursor — done together so a
// surface flip doesn't leave the cursor pointing past the new result set.
func (m *model) cycleSurface() {
	m.surfaceIdx = (m.surfaceIdx + 1) % len(surfaceFilters)
	m.cursor = 0
}

// currentSurface returns the active filter value ("" = all surfaces).
func (m *model) currentSurface() string { return surfaceFilters[m.surfaceIdx] }

// refreshRecent loads Recent synchronously at startup. The Cmd-flavored
// version is used after a surface flip so the UI thread stays responsive.
func (m *model) refreshRecent() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	surf := m.currentSurface()
	var es []engram.Engram
	var err error
	if surf == "" {
		es, err = m.st.Recent(ctx, 0, 0, 50)
	} else {
		es, err = m.st.Retrieve(ctx, surf, 0, 0, 50, false)
	}
	if err != nil {
		m.err = err
		return
	}
	m.engrams = es
	m.err = nil
	if m.cursor >= len(m.engrams) {
		m.cursor = 0
	}
}

func (m *model) refreshRecentCmd() tea.Cmd {
	surf := m.currentSurface()
	st := m.st
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var (
			es  []engram.Engram
			err error
		)
		if surf == "" {
			es, err = st.Recent(ctx, 0, 0, 50)
		} else {
			es, err = st.Retrieve(ctx, surf, 0, 0, 50, false)
		}
		return engramsLoadedMsg{engrams: es, err: err}
	}
}

func (m *model) runSearchCmd() tea.Cmd {
	q := strings.TrimSpace(m.searchInput)
	surf := m.currentSurface()
	st := m.st
	if q == "" {
		// Empty query falls back to Recent so the panel never blanks out.
		return m.refreshRecentCmd()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		es, err := st.Search(ctx, q, surf, 50)
		return engramsLoadedMsg{engrams: es, err: err}
	}
}

func (m *model) runAskCmd() tea.Cmd {
	q := strings.TrimSpace(m.askInput)
	surf := m.currentSurface()
	st := m.st
	if q == "" {
		return nil
	}
	fts := textsearch.QuestionToFTS(q)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		es, err := st.Search(ctx, fts, surf, 50)
		return engramsLoadedMsg{engrams: es, err: err}
	}
}

// ----- Styles -----

// Palette matches the landing page (teal accent on near-black). Kept here
// rather than a styles.go since the surface is small and one file is easier
// to grep when palette drift inevitably happens.
var (
	colAccent = lipgloss.Color("#5eead4")
	colBg     = lipgloss.Color("#0a0a0a")
	colDim    = lipgloss.Color("#888888")
	colFg     = lipgloss.Color("#e5e7eb")
	colWarn   = lipgloss.Color("#fbbf24")

	styleTitle = lipgloss.NewStyle().Foreground(colAccent).Bold(true).Padding(0, 1)
	styleDim   = lipgloss.NewStyle().Foreground(colDim)
	styleBadge = lipgloss.NewStyle().Foreground(colBg).Background(colAccent).Padding(0, 1).Bold(true)
	styleRow   = lipgloss.NewStyle().Foreground(colFg).Padding(0, 1)
	styleRowOn = lipgloss.NewStyle().Foreground(colBg).Background(colAccent).Padding(0, 1).Bold(true)
	styleErr   = lipgloss.NewStyle().Foreground(colWarn).Padding(0, 1)
	styleInput = lipgloss.NewStyle().Foreground(colFg).Border(lipgloss.NormalBorder()).
			BorderForeground(colAccent).Padding(0, 1)
	styleDetail = lipgloss.NewStyle().Foreground(colFg).Border(lipgloss.RoundedBorder()).
			BorderForeground(colAccent).Padding(1, 2)
	styleBar = lipgloss.NewStyle().Foreground(colDim).Background(lipgloss.Color("#111827")).Padding(0, 1)
)

// ----- View -----

func (m *model) View() string {
	if m.detailOpen {
		return m.renderDetail()
	}
	header := m.renderHeader()
	body := ""
	switch m.view {
	case viewRecent:
		body = m.renderEngramList()
	case viewSearch:
		body = m.renderSearch()
	case viewAsk:
		body = m.renderAsk()
	}
	footer := m.renderFooter()
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m *model) renderHeader() string {
	tabs := []string{}
	for i, name := range []string{"Recent", "Search", "Ask"} {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == m.view {
			tabs = append(tabs, styleBadge.Render(label))
		} else {
			tabs = append(tabs, styleDim.Render(label))
		}
	}
	title := styleTitle.Render("eideticd-browse")
	surf := m.currentSurface()
	if surf == "" {
		surf = "all surfaces"
	}
	right := styleDim.Render("surface: " + surf)
	left := title + " " + strings.Join(tabs, " ")
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

func (m *model) renderEngramList() string {
	if m.err != nil {
		return styleErr.Render("error: " + m.err.Error())
	}
	if len(m.engrams) == 0 {
		return styleDim.Render("  no engrams — capture some first via the daemon")
	}
	rows := make([]string, 0, len(m.engrams))
	maxRows := m.height - 6
	if maxRows < 1 {
		maxRows = 1
	}
	start := 0
	if m.cursor >= maxRows {
		start = m.cursor - maxRows + 1
	}
	end := start + maxRows
	if end > len(m.engrams) {
		end = len(m.engrams)
	}
	for i := start; i < end; i++ {
		e := m.engrams[i]
		row := formatRow(e, m.width)
		if i == m.cursor {
			rows = append(rows, styleRowOn.Render(row))
		} else {
			rows = append(rows, styleRow.Render(row))
		}
	}
	return strings.Join(rows, "\n")
}

func (m *model) renderSearch() string {
	input := styleInput.Width(m.width - 4).Render("search: " + m.searchInput + "_")
	return input + "\n" + m.renderEngramList()
}

func (m *model) renderAsk() string {
	input := styleInput.Width(m.width - 4).Render("ask: " + m.askInput + "_")
	hint := styleDim.Render("  (Enter to fire — uses internal/textsearch.QuestionToFTS, same as `eideticd --ask`)")
	return input + "\n" + hint + "\n" + m.renderEngramList()
}

func (m *model) renderDetail() string {
	if m.cursor >= len(m.engrams) {
		return styleErr.Render("no engram selected")
	}
	e := m.engrams[m.cursor]
	badge := styleBadge.Render(e.Surface)
	ts := styleDim.Render(humanizeTS(e.TS))
	header := badge + " " + ts + "  " + styleDim.Render(fmt.Sprintf("id=%d", e.ID))

	// Pretty-print meta JSON when it parses; fall back to the raw string
	// (Phase-3 parsers occasionally write non-JSON meta and we shouldn't
	// hide that from the user staring at the detail pane to debug it).
	meta := strings.TrimSpace(e.Meta)
	if meta != "" {
		var pretty interface{}
		if err := json.Unmarshal([]byte(meta), &pretty); err == nil {
			if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
				meta = string(b)
			}
		}
	}

	body := "PAYLOAD\n" + scrollText(e.Payload, m.detailScroll, m.height-12)
	if meta != "" {
		body += "\n\nMETA\n" + meta
	}
	help := styleDim.Render("j/k scroll · esc/enter close")
	return styleDetail.Width(m.width - 4).Render(header + "\n\n" + body + "\n\n" + help)
}

// scrollText drops `offset` lines off the top so j/k can walk a long
// payload. Kept naive (line-split, no wrap) — payloads are JSONL chunks and
// users coming to detail want to see structure, not a re-flowed paragraph.
func scrollText(s string, offset, max int) string {
	lines := strings.Split(s, "\n")
	if offset >= len(lines) {
		offset = len(lines) - 1
	}
	if offset < 0 {
		offset = 0
	}
	end := offset + max
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[offset:end], "\n")
}

func (m *model) renderFooter() string {
	hint := "1 Recent | 2 Search | 3 Ask | / search | ? ask | s surface | enter open | q quit"
	bar := fmt.Sprintf(" engrams: %d   view: %s   %s", len(m.engrams), viewName(m.view), hint)
	return styleBar.Width(m.width).Render(bar)
}

func viewName(v int) string {
	switch v {
	case viewRecent:
		return "Recent"
	case viewSearch:
		return "Search"
	case viewAsk:
		return "Ask"
	default:
		return "?"
	}
}

// formatRow renders a single list row: badge + relative time + payload prefix.
// width-aware so a 200-char payload doesn't wrap the row and break alignment.
func formatRow(e engram.Engram, width int) string {
	surface := e.Surface
	if surface == "" {
		surface = "?"
	}
	ts := humanizeTS(e.TS)
	preview := strings.ReplaceAll(e.Payload, "\n", " ")
	prefix := fmt.Sprintf("[%s] %s  ", surface, ts)
	avail := width - len(prefix) - 4
	if avail < 10 {
		avail = 10
	}
	if len(preview) > avail {
		preview = preview[:avail-1] + "…"
	}
	return prefix + preview
}

// humanizeTS produces a compact relative timestamp ("5m ago", "2h ago",
// "3d ago"). Local helper instead of go-humanize because pulling that into
// the direct dependency surface just for this is unjustified.
func humanizeTS(nanos int64) string {
	if nanos == 0 {
		return "—"
	}
	t := time.Unix(0, nanos)
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
