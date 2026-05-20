package textsearch

import "testing"

func TestQuestionToFTS_StripsStopwords(t *testing.T) {
	got := QuestionToFTS("What was that Postgres trick I learned?")
	want := "postgres OR trick OR learned"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestQuestionToFTS_KeepsOrder(t *testing.T) {
	got := QuestionToFTS("Find anything about React Suspense boundaries")
	want := "react OR suspense OR boundaries"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestQuestionToFTS_UsesORForRecall(t *testing.T) {
	got := QuestionToFTS("decide auth middleware refactor scope")
	if want := " OR "; !contains(got, want) {
		t.Errorf("got %q, want OR-joined", got)
	}
	if contains(got, " AND ") {
		t.Errorf("got %q, should not contain AND", got)
	}
}

func TestQuestionToFTS_DropsShortTokens(t *testing.T) {
	got := QuestionToFTS("Is React an alternative")
	want := "react OR alternative"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestQuestionToFTS_AllStopwordsFallsBack(t *testing.T) {
	in := "what is the why"
	got := QuestionToFTS(in)
	if got != in {
		t.Errorf("all-stopword input: got %q, want fallback %q", got, in)
	}
}

func TestQuestionToFTS_Lowercases(t *testing.T) {
	got := QuestionToFTS("POSTGRES tuning")
	if !contains(got, "postgres") || contains(got, "POSTGRES") {
		t.Errorf("got %q, want lowercased", got)
	}
}

func TestQuestionToFTS_SplitsOnPunctuation(t *testing.T) {
	got := QuestionToFTS("middleware.auth, refactor!")
	for _, w := range []string{"middleware", "auth", "refactor"} {
		if !contains(got, w) {
			t.Errorf("missing %q in %q", w, got)
		}
	}
	if contains(got, "middleware.auth") {
		t.Errorf("punctuation not stripped: %q", got)
	}
}

func TestQuestionToFTS_PreservesUnderscores(t *testing.T) {
	got := QuestionToFTS("the snake_case_name I used")
	if !contains(got, "snake_case_name") {
		t.Errorf("underscores should be kept as word chars; got %q", got)
	}
}

func TestQuestionToFTS_Empty(t *testing.T) {
	got := QuestionToFTS("")
	if got != "" {
		t.Errorf("empty input → empty (or original); got %q", got)
	}
}

func TestStopwords_NoCommonContentWords(t *testing.T) {
	// Sanity check: these words MUST NOT be in the stopword set or recall breaks
	mustNotStop := []string{"postgres", "react", "auth", "kubernetes", "python"}
	for _, w := range mustNotStop {
		if _, stop := Stopwords[w]; stop {
			t.Errorf("Stopwords contains content word %q — recall would break", w)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
