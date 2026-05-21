// Package bundle parses heterogeneous import bundles into engrams.
// Three formats are supported:
//
//   - ndjson:   one JSON object per line, each a (possibly partial) Engram
//   - markdown: split on top-level (`# `) and second-level (`## `) headers
//   - text:     split on blank lines (paragraphs)
//
// Auto-detection inspects the first non-empty line:
//
//	"{"  -> ndjson
//	"# " / "## " -> markdown
//	anything else -> text
//
// All parsers fill missing fields from the caller-supplied defaults
// (surface, base timestamp). Empty payloads are skipped with a warning
// returned in the result's Skipped slice — bundle.Parse never fails on
// a single bad row, but the caller can choose to abort on Skipped > 0.
//
// Used by `eideticd --import-bundle <path>` (v0.0.61+).
package bundle

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

// Format names a recognized bundle file format.
type Format string

const (
	FormatAuto     Format = "auto"
	FormatNDJSON   Format = "ndjson"
	FormatMarkdown Format = "markdown"
	FormatText     Format = "text"
)

// Defaults are applied to every row that doesn't carry its own value.
// Surface is required; BaseTS is required (typically time.Now().UnixNano()).
type Defaults struct {
	Surface string
	BaseTS  int64
}

// Result is what Parse returns. Engrams are in source order. Skipped lists
// human-readable reasons for any row dropped (empty payload, bad JSON, etc.).
type Result struct {
	Engrams  []engram.Engram
	Skipped  []string
	Detected Format
}

// Parse reads r, applies defaults, and returns engrams ready for InsertBatch.
// Format=FormatAuto sniffs the first non-empty line.
// BaseTS gets a 1-ns offset per row so monotonic ordering is preserved.
func Parse(r io.Reader, format Format, def Defaults) (*Result, error) {
	if def.Surface == "" {
		return nil, errors.New("bundle: Defaults.Surface required")
	}
	if def.BaseTS == 0 {
		def.BaseTS = time.Now().UnixNano()
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("bundle: read: %w", err)
	}
	if len(body) == 0 {
		return &Result{Detected: FormatAuto}, nil
	}
	if format == "" || format == FormatAuto {
		format = sniff(body)
	}
	switch format {
	case FormatNDJSON:
		return parseNDJSON(body, def), nil
	case FormatMarkdown:
		return parseMarkdown(body, def), nil
	case FormatText:
		return parseText(body, def), nil
	default:
		return nil, fmt.Errorf("bundle: unknown format %q (want ndjson|markdown|text|auto)", format)
	}
}

func sniff(body []byte) Format {
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "{"):
			return FormatNDJSON
		case strings.HasPrefix(line, "# "), strings.HasPrefix(line, "## "):
			return FormatMarkdown
		default:
			return FormatText
		}
	}
	return FormatText
}

func parseNDJSON(body []byte, def Defaults) *Result {
	res := &Result{Detected: FormatNDJSON}
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var i int
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e engram.Engram
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("ndjson line %d: bad JSON: %v", i+1, err))
			i++
			continue
		}
		if strings.TrimSpace(e.Payload) == "" {
			res.Skipped = append(res.Skipped, fmt.Sprintf("ndjson line %d: empty payload", i+1))
			i++
			continue
		}
		if e.Surface == "" {
			e.Surface = def.Surface
		}
		if e.TS == 0 {
			e.TS = def.BaseTS + int64(i)
		}
		res.Engrams = append(res.Engrams, e)
		i++
	}
	if err := sc.Err(); err != nil {
		res.Skipped = append(res.Skipped, fmt.Sprintf("ndjson scan: %v", err))
	}
	return res
}

func parseMarkdown(body []byte, def Defaults) *Result {
	res := &Result{Detected: FormatMarkdown}
	// Split on lines that start with "# " or "## ". The header line itself
	// becomes the first line of its section's payload so context is preserved.
	lines := strings.Split(string(body), "\n")
	var sections []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			sections = append(sections, s)
		}
		cur.Reset()
	}
	for _, ln := range lines {
		if strings.HasPrefix(ln, "# ") || strings.HasPrefix(ln, "## ") {
			flush()
		}
		cur.WriteString(ln)
		cur.WriteString("\n")
	}
	flush()
	// If the file has no headers at all, fall through to paragraph split so
	// markdown-without-headers still produces multiple engrams.
	if len(sections) <= 1 {
		return parseText(body, def)
	}
	for i, s := range sections {
		if s == "" {
			res.Skipped = append(res.Skipped, fmt.Sprintf("markdown section %d: empty", i+1))
			continue
		}
		res.Engrams = append(res.Engrams, engram.Engram{
			Surface: def.Surface,
			TS:      def.BaseTS + int64(i),
			Payload: s,
		})
	}
	return res
}

func parseText(body []byte, def Defaults) *Result {
	res := &Result{Detected: FormatText}
	// Paragraphs are separated by one-or-more blank lines.
	paras := splitParagraphs(string(body))
	for i, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			res.Skipped = append(res.Skipped, fmt.Sprintf("text paragraph %d: empty", i+1))
			continue
		}
		res.Engrams = append(res.Engrams, engram.Engram{
			Surface: def.Surface,
			TS:      def.BaseTS + int64(i),
			Payload: p,
		})
	}
	return res
}

func splitParagraphs(s string) []string {
	// Normalize CRLF -> LF so Windows files work.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Collapse 3+ newlines to 2 so "section break" patterns still yield one split.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.Split(s, "\n\n")
}
