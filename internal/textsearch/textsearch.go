// Package textsearch is the single source of truth for natural-language →
// FTS5-query rewriting used by both internal/api (HTTP /ask endpoint) and
// cmd/eideticd (CLI --ask flag).
//
// Before this package, the same stop-word strip + OR-join logic existed in
// three places (Go internal/api, Go cmd/, Python eidetic-mcp) and drifted
// silently. Now Go's two callers consume one canonical implementation;
// Python is mirrored in bridge/python/eidetic_mcp/server.py::_question_to_fts
// and kept in sync by review.
//
// Algorithm:
//   1. Tokenize on non-alphanumeric (word-boundary split)
//   2. Lowercase each token
//   3. Drop tokens < 3 chars
//   4. Drop stop-words (50+-entry English list, sourced from common
//      query stop-word lists tuned for recall over precision)
//   5. Join survivors with " OR " for permissive FTS5 matching
//   6. If nothing survives stripping, return the original question (best-effort
//      recall — better an over-broad FTS query than zero results)
package textsearch

import "strings"

// Stopwords are filtered out before FTS5 evaluation. Intentionally short list:
// each entry must be common enough that excluding it can't lose meaning and
// frequent enough that including it would dominate ranking.
var Stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "is": {}, "was": {}, "were": {}, "are": {},
	"be": {}, "been": {}, "being": {}, "i": {}, "me": {}, "my": {}, "we": {},
	"our": {}, "you": {}, "your": {}, "they": {}, "them": {}, "their": {},
	"it": {}, "its": {}, "this": {}, "that": {}, "these": {}, "those": {},
	"what": {}, "when": {}, "where": {}, "why": {}, "how": {}, "who": {},
	"which": {}, "do": {}, "did": {}, "does": {}, "have": {}, "had": {},
	"has": {}, "having": {}, "and": {}, "or": {}, "but": {}, "if": {},
	"then": {}, "else": {}, "of": {}, "to": {}, "in": {}, "on": {}, "for": {},
	"with": {}, "from": {}, "by": {}, "at": {}, "as": {}, "about": {},
	"into": {}, "over": {}, "out": {}, "up": {}, "down": {}, "again": {},
	"anything": {}, "something": {}, "find": {}, "tell": {}, "show": {},
	"give": {}, "ask": {}, "any": {}, "some": {}, "all": {}, "each": {},
	"every": {},
}

// QuestionToFTS turns a natural-language question into a permissive FTS5
// OR-query (best-recall semantics). Returns the original question verbatim
// if stripping leaves nothing (e.g. "is it?" → fall back to "is it?").
func QuestionToFTS(question string) string {
	var keywords []string
	var cur []rune

	flush := func() {
		if len(cur) == 0 {
			return
		}
		t := strings.ToLower(string(cur))
		cur = cur[:0]
		if len(t) < 3 {
			return
		}
		if _, stop := Stopwords[t]; stop {
			return
		}
		keywords = append(keywords, t)
	}

	for _, r := range question {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()

	if len(keywords) == 0 {
		return question
	}
	return strings.Join(keywords, " OR ")
}
