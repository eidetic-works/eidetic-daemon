// Package autotag classifies engrams using heuristic regex matching.
// No LLM dependency — works offline, fast, deterministic.
//
// Tags applied (multi-label; each engram can have 0+ tags):
//   - question  — payload ends with "?" or starts with WH-word + has "?"
//   - decision  — contains "decided to", "going with", "won't", "let's"
//   - error     — contains stack trace marker, "Error:", "Exception:", "panic:", or HTTP 4xx/5xx
//   - code      — contains 4+ lines of code-looking content (indent or backticks)
//   - link      — contains http(s):// URL
//   - command   — payload looks like a shell command (starts with $, %, >, # or known binary)
//
// Result: meta field gets a `tags` array merged in. Existing meta keys preserved.
// Run via `eideticd --auto-tag [--since 7d]` — scans engrams in window, computes
// tags, writes back to meta. v0.0.60+.
package autotag

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Tag is one classification label.
type Tag string

const (
	TagQuestion Tag = "question"
	TagDecision Tag = "decision"
	TagError    Tag = "error"
	TagCode     Tag = "code"
	TagLink     Tag = "link"
	TagCommand  Tag = "command"
)

var (
	urlRe        = regexp.MustCompile(`https?://[^\s)>"']+`)
	httpStatusRe = regexp.MustCompile(`\b[45]\d{2}\b\s+(error|forbidden|unauthorized|not found|server error|bad)`)
	stackTraceRe = regexp.MustCompile(`(?i)(traceback|at\s+\w+\.\w+\(|panic:|exception:)`)
	whWordRe     = regexp.MustCompile(`^(what|when|where|why|how|who|which)\b`)
	codeyLineRe  = regexp.MustCompile(`(?m)^[ \t]{2,}\S+`)
	commandRe    = regexp.MustCompile(`^[$%>#]\s+\w+`)
	knownCmdRe   = regexp.MustCompile(`^(git|kubectl|docker|brew|npm|pip|cargo|go|wrangler|curl|ls|cd|grep|cat)\b`)
)

// Decision phrases — matched as substrings (case-insensitive).
var decisionPhrases = []string{
	"decided to", "decided not to", "going with", "won't ", "wont ",
	"let's go with", "going to use", "settled on", "picked ",
}

// Classify returns the set of tags that match the engram payload. Multi-label.
// Empty payload → empty tag set.
func Classify(payload string) []Tag {
	if payload == "" {
		return nil
	}
	var tags []Tag

	trimmed := strings.TrimSpace(payload)
	lower := strings.ToLower(trimmed)

	// question — ends in ? OR starts with WH-word and has ? somewhere
	if strings.HasSuffix(trimmed, "?") {
		tags = append(tags, TagQuestion)
	} else if whWordRe.MatchString(lower) && strings.Contains(trimmed, "?") {
		tags = append(tags, TagQuestion)
	}

	// decision
	for _, phrase := range decisionPhrases {
		if strings.Contains(lower, phrase) {
			tags = append(tags, TagDecision)
			break
		}
	}

	// error
	if stackTraceRe.MatchString(payload) ||
		strings.Contains(payload, "Error:") ||
		strings.Contains(payload, "Exception:") ||
		httpStatusRe.MatchString(lower) {
		tags = append(tags, TagError)
	}

	// code — 4+ indented lines OR triple-backtick fence
	if strings.Count(payload, "```") >= 2 {
		tags = append(tags, TagCode)
	} else {
		indentedLines := len(codeyLineRe.FindAllString(payload, -1))
		if indentedLines >= 4 {
			tags = append(tags, TagCode)
		}
	}

	// link
	if urlRe.MatchString(payload) {
		tags = append(tags, TagLink)
	}

	// command
	if commandRe.MatchString(trimmed) || knownCmdRe.MatchString(trimmed) {
		tags = append(tags, TagCommand)
	}

	return tags
}

// MergeMeta takes an existing meta JSON string (may be empty/"") and merges
// new tags into a top-level `tags` key. Preserves all other keys.
// Returns the new meta JSON. Idempotent: tags are deduplicated.
func MergeMeta(existingMeta string, tags []Tag) (string, error) {
	merged := make(map[string]any)
	if existingMeta != "" {
		if err := json.Unmarshal([]byte(existingMeta), &merged); err != nil {
			// Non-JSON existing meta: wrap it under "raw_meta" to preserve it
			merged = map[string]any{"raw_meta": existingMeta}
		}
	}

	// Merge with existing tags (dedupe)
	seen := make(map[string]bool)
	var combined []string
	if existing, ok := merged["tags"].([]any); ok {
		for _, t := range existing {
			if s, ok := t.(string); ok && !seen[s] {
				seen[s] = true
				combined = append(combined, s)
			}
		}
	}
	for _, t := range tags {
		s := string(t)
		if !seen[s] {
			seen[s] = true
			combined = append(combined, s)
		}
	}
	merged["tags"] = combined

	out, err := json.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("autotag: marshal meta: %w", err)
	}
	return string(out), nil
}
