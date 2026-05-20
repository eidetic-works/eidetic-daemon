package autotag

import (
	"encoding/json"
	"testing"
)

func TestClassify_Question(t *testing.T) {
	tags := Classify("What's the best way to do this?")
	if !hasTag(tags, TagQuestion) {
		t.Errorf("WH-word + ? should tag question; got %v", tags)
	}
}

func TestClassify_QuestionEndingMark(t *testing.T) {
	tags := Classify("is this working?")
	if !hasTag(tags, TagQuestion) {
		t.Errorf("trailing ? should tag question; got %v", tags)
	}
}

func TestClassify_Decision(t *testing.T) {
	cases := []string{
		"I decided to use Postgres",
		"Going with redis for the cache",
		"won't add tests for this",
		"let's go with kubernetes",
	}
	for _, c := range cases {
		if !hasTag(Classify(c), TagDecision) {
			t.Errorf("not tagged as decision: %q", c)
		}
	}
}

func TestClassify_Error(t *testing.T) {
	cases := []string{
		"Error: connection refused",
		"Exception: NoSuchElement",
		`Traceback (most recent call last):`,
		"panic: runtime error",
		"got 500 server error",
	}
	for _, c := range cases {
		if !hasTag(Classify(c), TagError) {
			t.Errorf("not tagged as error: %q", c)
		}
	}
}

func TestClassify_Code_Backtick(t *testing.T) {
	payload := "Here is some code:\n```\nfunc main() {}\n```"
	if !hasTag(Classify(payload), TagCode) {
		t.Errorf("triple-backtick fence should tag code")
	}
}

func TestClassify_Code_Indented(t *testing.T) {
	payload := "the func body:\n  if x {\n    y()\n    z()\n    return\n  }"
	if !hasTag(Classify(payload), TagCode) {
		t.Errorf("4+ indented lines should tag code")
	}
}

func TestClassify_Link(t *testing.T) {
	cases := []string{
		"see https://github.com/example",
		"http://example.org for info",
	}
	for _, c := range cases {
		if !hasTag(Classify(c), TagLink) {
			t.Errorf("URL not tagged as link: %q", c)
		}
	}
}

func TestClassify_Command(t *testing.T) {
	cases := []string{
		"$ git status",
		"# kubectl get pods",
		"docker compose up",
		"npm install foo",
	}
	for _, c := range cases {
		if !hasTag(Classify(c), TagCommand) {
			t.Errorf("not tagged as command: %q", c)
		}
	}
}

func TestClassify_EmptyPayload(t *testing.T) {
	if tags := Classify(""); tags != nil {
		t.Errorf("empty payload: want nil tags, got %v", tags)
	}
}

func TestClassify_MultipleTags(t *testing.T) {
	payload := "Hit Error: 500 on https://api.example.com — going with retry"
	tags := Classify(payload)
	if !hasTag(tags, TagError) || !hasTag(tags, TagLink) || !hasTag(tags, TagDecision) {
		t.Errorf("multi-label failed: %v", tags)
	}
}

func TestMergeMeta_EmptyExisting(t *testing.T) {
	out, err := MergeMeta("", []Tag{TagQuestion, TagCode})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	tags := m["tags"].([]any)
	if len(tags) != 2 {
		t.Errorf("want 2 tags, got %d", len(tags))
	}
}

func TestMergeMeta_PreservesExistingKeys(t *testing.T) {
	existing := `{"source":"cli-capture","host":"x"}`
	out, err := MergeMeta(existing, []Tag{TagCode})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	if m["source"] != "cli-capture" || m["host"] != "x" {
		t.Errorf("lost existing keys: %v", m)
	}
}

func TestMergeMeta_DedupesTags(t *testing.T) {
	existing := `{"tags":["code","link"]}`
	out, err := MergeMeta(existing, []Tag{TagCode, TagError})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	tags := m["tags"].([]any)
	if len(tags) != 3 {
		t.Errorf("want 3 unique tags (code+link+error), got %d: %v", len(tags), tags)
	}
}

func TestMergeMeta_NonJSONExisting(t *testing.T) {
	// Non-JSON existing meta gets wrapped under raw_meta
	out, err := MergeMeta("not json", []Tag{TagQuestion})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	if m["raw_meta"] != "not json" {
		t.Errorf("non-JSON existing not preserved under raw_meta: %v", m)
	}
}

func hasTag(tags []Tag, t Tag) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}
