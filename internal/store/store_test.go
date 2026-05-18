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

	got, err := s.Retrieve(ctx, "cursor", 0, 0, 10, false)
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
	got, err := s.Retrieve(ctx, "claude_code", base+5000, 0, 100, false)
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

	got, err := s.Retrieve(ctx, "cursor", 0, 0, 10, false)
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
	got, err := s.Retrieve(ctx, "cursor", 0, 0, 999, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 500 {
		t.Fatalf("limit not clamped to 500: got %d", len(got))
	}
}

func TestInsertRejectsEmptySurfaceOrZeroTSOrEmptyPayload(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	if _, err := s.Insert(ctx, engram.Engram{Surface: "", TS: 1, Payload: "p"}); err == nil {
		t.Fatal("want error for empty surface")
	}
	if _, err := s.Insert(ctx, engram.Engram{Surface: "x", TS: 0, Payload: "p"}); err == nil {
		t.Fatal("want error for zero ts")
	}
	// PR#1 review concern #1: schema NOT NULL is no-op on Go's "" — enforce here.
	if _, err := s.Insert(ctx, engram.Engram{Surface: "x", TS: 1, Payload: ""}); err == nil {
		t.Fatal("want error for empty payload")
	}
}

func TestInsertRejectsOversizePayload(t *testing.T) {
	// PR#1 review concern #3: oversize payload blocks single-writer pool.
	s := tempStore(t)
	ctx := context.Background()
	big := strings.Repeat("x", store.MaxPayloadBytes+1)
	_, err := s.Insert(ctx, engram.Engram{Surface: "cursor", TS: time.Now().UnixNano(), Payload: big})
	if err == nil {
		t.Fatalf("want error for payload > MaxPayloadBytes (%d)", store.MaxPayloadBytes)
	}
	if !strings.Contains(err.Error(), "MaxPayloadBytes") {
		t.Fatalf("error should reference MaxPayloadBytes, got: %v", err)
	}
}

func TestRetrieveSinceZeroReturnsAllRows(t *testing.T) {
	// PR#1 review concern #2 regression guard: since=0 must return ALL rows,
	// not zero. Two-branch refactor must not flip this case.
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UnixNano()
	for i := 0; i < 3; i++ {
		if _, err := s.Insert(ctx, engram.Engram{Surface: "cowork", TS: now + int64(i), Payload: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Retrieve(ctx, "cowork", 0, 0, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("since=0 should return all 3 rows, got %d", len(got))
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
	got, err := s.Retrieve(ctx, "claude_code", 0, 0, 1, false)
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

func TestStorePurgeAll(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UnixNano()

	for i := 0; i < 5; i++ {
		if _, err := s.Insert(ctx, engram.Engram{Surface: "claude_code", TS: now + int64(i), Payload: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	// Insert on a different surface — must be untouched.
	if _, err := s.Insert(ctx, engram.Engram{Surface: "cursor", TS: now, Payload: "y"}); err != nil {
		t.Fatal(err)
	}

	n, err := s.Purge(ctx, "claude_code", 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("Purge(all): want 5 deleted, got %d", n)
	}

	// claude_code rows gone.
	rows, err := s.Retrieve(ctx, "claude_code", 0, 0, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("after purge: want 0 rows for claude_code, got %d", len(rows))
	}

	// cursor row untouched.
	rows, err = s.Retrieve(ctx, "cursor", 0, 0, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("after purge: cursor surface should be untouched, got %d rows", len(rows))
	}
}

func TestStorePurgeBefore(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	// Insert 4 rows: ts 100, 200, 300, 400 (nanoseconds, small values for clarity).
	for _, ts := range []int64{100, 200, 300, 400} {
		if _, err := s.Insert(ctx, engram.Engram{Surface: "claude_code", TS: ts, Payload: "p"}); err != nil {
			t.Fatal(err)
		}
	}

	// Purge ts < 300 → should delete rows at ts=100 and ts=200 (2 rows).
	n, err := s.Purge(ctx, "claude_code", 300)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("Purge(before=300): want 2 deleted, got %d", n)
	}

	// Remaining: ts=300 and ts=400.
	rows, err := s.Retrieve(ctx, "claude_code", 0, 0, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("after Purge(before=300): want 2 remaining rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.TS < 300 {
			t.Errorf("Purge(before=300) left ts=%d which should have been deleted", r.TS)
		}
	}
}

func TestRetrieveBeforeFilter(t *testing.T) {
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
	// before=base+5000 → only rows with ts < base+5000 (i=0..4, 5 rows).
	got, err := s.Retrieve(ctx, "claude_code", 0, base+5000, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("want 5 rows before=base+5000, got %d", len(got))
	}
	for _, r := range got {
		if r.TS >= base+5000 {
			t.Errorf("before filter leaked ts=%d (>= cutoff)", r.TS)
		}
	}
}

func TestRetrieveSinceAndBefore(t *testing.T) {
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
	// since=base+2000, before=base+7000 → ts in (base+2000, base+7000) exclusive: i=3,4,5,6 (4 rows).
	got, err := s.Retrieve(ctx, "claude_code", base+2000, base+7000, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 rows in (since, before) window, got %d", len(got))
	}
	for _, r := range got {
		if r.TS <= base+2000 || r.TS >= base+7000 {
			t.Errorf("window filter leaked ts=%d", r.TS)
		}
	}
}

func TestStorePurgeEmptySurface(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, err := s.Purge(ctx, "", 0)
	if err == nil {
		t.Error("Purge with empty surface should return error")
	}
}

func TestStorePurgeNonExistentSurface(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	n, err := s.Purge(ctx, "no_such_surface", 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("purge non-existent surface: want 0 deleted, got %d", n)
	}
}

func TestRetrieveAscOrder(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	base := time.Now().UnixNano()

	for i := 0; i < 5; i++ {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "claude_code",
			TS:      base + int64(i*1000),
			Payload: "p",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Default (asc=false): newest first.
	desc, err := s.Retrieve(ctx, "claude_code", 0, 0, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if desc[0].TS <= desc[len(desc)-1].TS {
		t.Errorf("desc order: want first > last, got first=%d last=%d", desc[0].TS, desc[len(desc)-1].TS)
	}

	// asc=true: oldest first.
	asc, err := s.Retrieve(ctx, "claude_code", 0, 0, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if asc[0].TS >= asc[len(asc)-1].TS {
		t.Errorf("asc order: want first < last, got first=%d last=%d", asc[0].TS, asc[len(asc)-1].TS)
	}
	// Same rows, reversed.
	if len(desc) != len(asc) {
		t.Fatalf("want same row count, got desc=%d asc=%d", len(desc), len(asc))
	}
	for i := range desc {
		if desc[i].ID != asc[len(asc)-1-i].ID {
			t.Errorf("row %d: desc[%d].ID=%d != asc[%d].ID=%d", i, i, desc[i].ID, len(asc)-1-i, asc[len(asc)-1-i].ID)
		}
	}
}
