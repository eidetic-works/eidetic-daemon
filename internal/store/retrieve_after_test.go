package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

// TestRetrieveAfterCoversSharedTsRows is the regression test for the CRITICAL
// audit finding (`internal/api/routes.go:537`): export pagination used
// `WHERE ts > cursor` which dropped every row sharing the boundary ts.
// Combined with handleEngramsBatch stamping one `time.Now()` on a whole
// batch, exporting after a batch insert lost N-1 rows.
//
// This test seeds 5 rows at one ts, 5 rows at a second ts. With pageSize=5,
// the page boundary lands exactly between the two clusters. The compound
// (ts, id) cursor must NOT drop any row.
func TestRetrieveAfterCoversSharedTsRows(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	const tsA = int64(1_700_000_000_000_000_000)
	const tsB = int64(1_700_000_000_000_000_001)
	batch := []engram.Engram{
		{Surface: "claude_code", TS: tsA, Payload: "a-1"},
		{Surface: "claude_code", TS: tsA, Payload: "a-2"},
		{Surface: "claude_code", TS: tsA, Payload: "a-3"},
		{Surface: "claude_code", TS: tsA, Payload: "a-4"},
		{Surface: "claude_code", TS: tsA, Payload: "a-5"},
		{Surface: "claude_code", TS: tsB, Payload: "b-1"},
		{Surface: "claude_code", TS: tsB, Payload: "b-2"},
		{Surface: "claude_code", TS: tsB, Payload: "b-3"},
		{Surface: "claude_code", TS: tsB, Payload: "b-4"},
		{Surface: "claude_code", TS: tsB, Payload: "b-5"},
	}
	if err := s.InsertBatch(ctx, batch); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Paginate with pageSize=5; verify all 10 rows surface across pages.
	const pageSize = 5
	var (
		cursorTS int64
		cursorID int64
		got      []string
	)
	for {
		rows, err := s.RetrieveAfter(ctx, "", cursorTS, cursorID, 0, pageSize)
		if err != nil {
			t.Fatalf("RetrieveAfter: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			got = append(got, r.Payload)
		}
		last := rows[len(rows)-1]
		cursorTS = last.TS
		cursorID = last.ID
		if len(rows) < pageSize {
			break
		}
	}

	if len(got) != 10 {
		t.Fatalf("paginated count = %d, want 10 (got payloads: %v)", len(got), got)
	}
	// Must include every seeded payload exactly once.
	for _, want := range []string{"a-1", "a-2", "a-3", "a-4", "a-5",
		"b-1", "b-2", "b-3", "b-4", "b-5"} {
		var n int
		for _, p := range got {
			if p == want {
				n++
			}
		}
		if n != 1 {
			t.Errorf("payload %q appeared %d times, want 1", want, n)
		}
	}
}

// TestRetrieveAfterChunkedRecordRoundTrip verifies that chunked-capture
// records — where splitOversized() emits N chunks at identical ts — survive
// the export round-trip without losing chunks.
func TestRetrieveAfterChunkedRecordRoundTrip(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	// 4 chunks all at ts=42 (simulating splitOversized output).
	const sharedTS = int64(42)
	for i := 0; i < 4; i++ {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "claude_code",
			TS:      sharedTS,
			Payload: "chunk-" + string(rune('A'+i)),
			Meta:    `{"chunk_id":"abc","chunk_seq":` + string(rune('0'+i)) + `,"chunk_total":4}`,
		})
		if err != nil {
			t.Fatalf("insert chunk %d: %v", i, err)
		}
	}

	// Paginate with pageSize=2 → boundary lands inside the chunk group.
	var (
		cursorTS int64
		cursorID int64
		got      []engram.Engram
	)
	for {
		rows, err := s.RetrieveAfter(ctx, "", cursorTS, cursorID, 0, 2)
		if err != nil {
			t.Fatalf("RetrieveAfter: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		got = append(got, rows...)
		last := rows[len(rows)-1]
		cursorTS = last.TS
		cursorID = last.ID
		if len(rows) < 2 {
			break
		}
	}

	if len(got) != 4 {
		t.Fatalf("chunked round-trip: got %d chunks, want 4 (boundary-row drop regression)", len(got))
	}
	// All chunks share the same chunk_id in meta.
	for i, e := range got {
		if !strings.Contains(e.Meta, `"chunk_id":"abc"`) {
			t.Errorf("chunk %d lost chunk_id: meta=%q", i, e.Meta)
		}
	}
}

// TestRetrieveAfterSurfaceFilter confirms the optional surface filter.
func TestRetrieveAfterSurfaceFilter(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	batch := []engram.Engram{
		{Surface: "cursor", TS: 1, Payload: "cursor-a"},
		{Surface: "claude_code", TS: 2, Payload: "cc-a"},
		{Surface: "cursor", TS: 3, Payload: "cursor-b"},
	}
	if err := s.InsertBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	got, err := s.RetrieveAfter(ctx, "cursor", 0, 0, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("surface=cursor: got %d, want 2 (cursor-only)", len(got))
	}
	for _, e := range got {
		if e.Surface != "cursor" {
			t.Errorf("got surface %q, want cursor", e.Surface)
		}
	}
}

// TestRetrieveAfterBeforeBound confirms the optional `before` upper bound.
func TestRetrieveAfterBeforeBound(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	batch := []engram.Engram{
		{Surface: "cursor", TS: 100, Payload: "early"},
		{Surface: "cursor", TS: 200, Payload: "mid"},
		{Surface: "cursor", TS: 300, Payload: "late"},
	}
	if err := s.InsertBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	got, err := s.RetrieveAfter(ctx, "", 0, 0, 250, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("before=250: got %d, want 2 (early+mid)", len(got))
	}
}

// TestRetrieveAfterEmptyStore returns no rows + no error.
func TestRetrieveAfterEmptyStore(t *testing.T) {
	s := tempStore(t)
	got, err := s.RetrieveAfter(context.Background(), "", 0, 0, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty store: got %d rows, want 0", len(got))
	}
}
