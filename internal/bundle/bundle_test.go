package bundle

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSniffNDJSON(t *testing.T) {
	if got := sniff([]byte(`  {"payload":"x"}` + "\n")); got != FormatNDJSON {
		t.Fatalf("ndjson sniff: want %q got %q", FormatNDJSON, got)
	}
}

func TestSniffMarkdown(t *testing.T) {
	if got := sniff([]byte("\n# title\nbody")); got != FormatMarkdown {
		t.Fatalf("markdown sniff: want %q got %q", FormatMarkdown, got)
	}
	if got := sniff([]byte("## section\nbody")); got != FormatMarkdown {
		t.Fatalf("h2 markdown sniff: want %q got %q", FormatMarkdown, got)
	}
}

func TestSniffText(t *testing.T) {
	if got := sniff([]byte("hello world\n\nanother line")); got != FormatText {
		t.Fatalf("text sniff: want %q got %q", FormatText, got)
	}
}

func TestParseNDJSONHappyPath(t *testing.T) {
	in := `{"payload":"first row","surface":"override"}
{"payload":"second row"}
{"payload":"third","ts":1234567890}
`
	r := strings.NewReader(in)
	res, err := Parse(r, FormatAuto, Defaults{Surface: "default-surface", BaseTS: 1000})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Detected != FormatNDJSON {
		t.Fatalf("detected: want ndjson got %q", res.Detected)
	}
	if len(res.Engrams) != 3 {
		t.Fatalf("engrams: want 3 got %d (skipped=%v)", len(res.Engrams), res.Skipped)
	}
	if res.Engrams[0].Surface != "override" {
		t.Errorf("row[0] surface override: want override got %q", res.Engrams[0].Surface)
	}
	if res.Engrams[1].Surface != "default-surface" {
		t.Errorf("row[1] surface default fallback: want default-surface got %q", res.Engrams[1].Surface)
	}
	if res.Engrams[2].TS != 1234567890 {
		t.Errorf("row[2] explicit ts preserved: want 1234567890 got %d", res.Engrams[2].TS)
	}
	if res.Engrams[0].TS != 1000 || res.Engrams[1].TS != 1001 {
		t.Errorf("monotonic TS expected 1000,1001 got %d,%d", res.Engrams[0].TS, res.Engrams[1].TS)
	}
}

func TestParseNDJSONSkipsBadRows(t *testing.T) {
	in := `{"payload":"good"}
not json at all
{"payload":""}
{"payload":"another good"}
`
	res, err := Parse(strings.NewReader(in), FormatNDJSON, Defaults{Surface: "s", BaseTS: 1})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Engrams) != 2 {
		t.Fatalf("want 2 good rows got %d", len(res.Engrams))
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("want 2 skipped got %d (%v)", len(res.Skipped), res.Skipped)
	}
}

func TestParseMarkdownH1Split(t *testing.T) {
	in := `# Section One
content for first section
spanning multiple lines

# Section Two
content for second
`
	res, err := Parse(strings.NewReader(in), FormatAuto, Defaults{Surface: "docs", BaseTS: 100})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Detected != FormatMarkdown {
		t.Fatalf("detected: want markdown got %q", res.Detected)
	}
	if len(res.Engrams) != 2 {
		t.Fatalf("want 2 sections got %d", len(res.Engrams))
	}
	if !strings.HasPrefix(res.Engrams[0].Payload, "# Section One") {
		t.Errorf("section[0] should retain header, got %q", res.Engrams[0].Payload[:30])
	}
	if !strings.HasPrefix(res.Engrams[1].Payload, "# Section Two") {
		t.Errorf("section[1] should retain header, got %q", res.Engrams[1].Payload[:30])
	}
}

func TestParseMarkdownMixedHeaders(t *testing.T) {
	in := `# Top
intro
## Sub A
detail A
## Sub B
detail B
`
	res, _ := Parse(strings.NewReader(in), FormatMarkdown, Defaults{Surface: "docs", BaseTS: 1})
	if len(res.Engrams) != 3 {
		t.Fatalf("want 3 sections (h1 + 2 h2s) got %d", len(res.Engrams))
	}
}

func TestParseMarkdownNoHeadersFallsBackToText(t *testing.T) {
	in := "paragraph one\nmore line one\n\nparagraph two\n"
	res, _ := Parse(strings.NewReader(in), FormatMarkdown, Defaults{Surface: "s", BaseTS: 1})
	// No headers → fallback to paragraph split → 2 engrams.
	if len(res.Engrams) != 2 {
		t.Fatalf("markdown-no-headers fallback: want 2 paragraphs got %d", len(res.Engrams))
	}
}

func TestParseTextParagraphs(t *testing.T) {
	in := "first paragraph\nstill first\n\nsecond para\n\n\nthird\n"
	res, err := Parse(strings.NewReader(in), FormatAuto, Defaults{Surface: "notes", BaseTS: 1000})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Detected != FormatText {
		t.Fatalf("detected: want text got %q", res.Detected)
	}
	if len(res.Engrams) != 3 {
		t.Fatalf("want 3 paragraphs got %d (skipped=%v)", len(res.Engrams), res.Skipped)
	}
	if res.Engrams[0].Surface != "notes" {
		t.Errorf("surface default: want notes got %q", res.Engrams[0].Surface)
	}
	if res.Engrams[1].TS != 1001 {
		t.Errorf("monotonic TS row[1]: want 1001 got %d", res.Engrams[1].TS)
	}
}

func TestParseEmptyInput(t *testing.T) {
	res, err := Parse(strings.NewReader(""), FormatAuto, Defaults{Surface: "s", BaseTS: 1})
	if err != nil {
		t.Fatalf("empty input should not error: %v", err)
	}
	if len(res.Engrams) != 0 {
		t.Fatalf("empty input should produce 0 engrams, got %d", len(res.Engrams))
	}
}

func TestParseRejectsEmptySurface(t *testing.T) {
	_, err := Parse(strings.NewReader("x"), FormatText, Defaults{})
	if err == nil {
		t.Fatal("want error for missing Surface")
	}
}

func TestParseRejectsUnknownFormat(t *testing.T) {
	_, err := Parse(strings.NewReader("x"), Format("xml"), Defaults{Surface: "s"})
	if err == nil {
		t.Fatal("want error for unknown format")
	}
}

func TestParseHandlesCRLF(t *testing.T) {
	in := "p1\r\nstill p1\r\n\r\np2\r\n"
	res, _ := Parse(strings.NewReader(in), FormatText, Defaults{Surface: "s", BaseTS: 1})
	if len(res.Engrams) != 2 {
		t.Fatalf("CRLF should split into 2 paragraphs, got %d", len(res.Engrams))
	}
}

func TestNDJSONPreservesMeta(t *testing.T) {
	meta := `{"source":"chatgpt","conversation_id":"abc123"}`
	row, _ := json.Marshal(map[string]any{"payload": "hi", "meta": meta})
	res, _ := Parse(strings.NewReader(string(row)+"\n"), FormatNDJSON, Defaults{Surface: "s", BaseTS: 1})
	if len(res.Engrams) != 1 {
		t.Fatalf("want 1 engram got %d", len(res.Engrams))
	}
	if res.Engrams[0].Meta != meta {
		t.Errorf("meta not preserved: want %q got %q", meta, res.Engrams[0].Meta)
	}
}
