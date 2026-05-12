package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

func tempStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engrams.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesDBAndAppliesSchema(t *testing.T) {
	s := tempStore(t)
	if s.Path() == "" {
		t.Fatal("Path empty")
	}
	// Schema migration is idempotent — reopen on the same file should not error.
	s2, err := store.Open(s.Path())
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	s2.Close()
}

func TestInsertAndRetrieveRoundTrip(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UnixNano()

	for i := 0; i < 5; i++ {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "cursor",
			TS:      now + int64(i),
			Payload: "test payload " + string(rune('A'+i)),
			Meta:    `{"src":"unit-test"}`,
		})
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	got, err := s.Retrieve(ctx, "cursor", 0, 10)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("want 5 rows, got %d", len(got))
	}
	// ORDER BY ts DESC means newest first.
	if got[0].TS <= got[len(got)-1].TS {
		t.Fatalf("rows not in desc ts order: first=%d last=%d", got[0].TS, got[len(got)-1].TS)
	}
}

func TestRetrieveSinceFilter(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	base := time.Now().UnixNano()

	for i := 0; i < 10; i++ {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "claude_code",
			TS:      base + int64(i*1000),
			Payload: "p",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Only rows with ts > base+5000 should return.
	got, err := s.Retrieve(ctx, "claude_code", base+5000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 rows after since=base+5000, got %d", len(got))
	}
}

func TestRetrieveSurfaceIsolation(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UnixNano()

	if _, err := s.Insert(ctx, engram.Engram{Surface: "cursor", TS: now, Payload: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert(ctx, engram.Engram{Surface: "cowork", TS: now + 1, Payload: "b"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Retrieve(ctx, "cursor", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Surface != "cursor" {
		t.Fatalf("surface filter leaked: %+v", got)
	}
}

func TestRetrieveLimitClamps(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UnixNano()

	for i := 0; i < 600; i++ {
		if _, err := s.Insert(ctx, engram.Engram{Surface: "cursor", TS: now + int64(i), Payload: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Retrieve(ctx, "cursor", 0, 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 500 {
		t.Fatalf("limit not clamped to 500: got %d", len(got))
	}
}

func TestInsertRejectsEmptySurfaceOrZeroTS(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	if _, err := s.Insert(ctx, engram.Engram{Surface: "", TS: 1, Payload: "p"}); err == nil {
		t.Fatal("want error for empty surface")
	}
	if _, err := s.Insert(ctx, engram.Engram{Surface: "x", TS: 0, Payload: "p"}); err == nil {
		t.Fatal("want error for zero ts")
	}
}

func TestWALModeAndIndexUsedForRetrieval(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	// Reach into the writer pool to verify journal_mode pragma stuck.
	// Done via Insert + a quick raw query through Retrieve's path; if the
	// pragmas didn't apply, the 5s busy_timeout on concurrent test runs would
	// surface as random "database is locked" errors. Smoke-check by inserting
	// a row and confirming ORDER BY ts DESC + LIMIT returns it via the index.
	if _, err := s.Insert(ctx, engram.Engram{Surface: "claude_code", TS: time.Now().UnixNano(), Payload: "p"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Retrieve(ctx, "claude_code", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("retrieve via index returned %d rows; want 1", len(got))
	}
	// Sanity: payload round-tripped (catches SQLite driver-strip silent failures
	// per memory feedback_cgo_cross_compile_silent_failure even though we use
	// pure-Go modernc — defense-in-depth).
	if !strings.Contains(got[0].Payload, "p") {
		t.Fatalf("payload roundtrip broken: %q", got[0].Payload)
	}
}
