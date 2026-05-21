package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/api"
	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

// TestExportNoDropsOnSharedTsBoundary is the regression test for the CRITICAL
// audit finding (`internal/api/routes.go:537`): the prior export cursor was
// `cursor = rows[last].TS` combined with a `ts > since` next-page filter.
// When a page boundary fell on a shared-ts cluster (e.g. handleEngramsBatch
// stamping one time.Now() on every batch item, or splitOversized() emitting
// N chunks at the same ts), the boundary row's ts-mates were silently
// dropped on the next page.
//
// Setup: 1500 rows, the middle 200 of which share one identical ts. With
// the default 1000-row export pageSize, the page boundary lands inside that
// shared-ts cluster (rows 1000+ are split). Pre-fix: ≤1300 rows exported;
// the post-boundary shared-ts rows are lost. Post-fix: all 1500.
func TestExportNoDropsOnSharedTsBoundary(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	const (
		preCount  = 900  // rows with unique ts
		midCount  = 200  // rows sharing one identical ts (boundary cluster)
		postCount = 400  // rows after the cluster
		sharedTS  = int64(1_700_000_000_500_000_000)
	)

	// Pre-cluster rows: unique ts ascending.
	pre := make([]engram.Engram, 0, preCount)
	for i := 0; i < preCount; i++ {
		pre = append(pre, engram.Engram{
			Surface: "cursor",
			TS:      int64(1_700_000_000_000_000_000 + i),
			Payload: fmt.Sprintf("pre-%d", i),
		})
	}
	if err := st.InsertBatch(ctx, pre); err != nil {
		t.Fatalf("seed pre: %v", err)
	}

	// Middle cluster — all at sharedTS. Straddles the 1000-row page boundary.
	mid := make([]engram.Engram, 0, midCount)
	for i := 0; i < midCount; i++ {
		mid = append(mid, engram.Engram{
			Surface: "cursor",
			TS:      sharedTS,
			Payload: fmt.Sprintf("mid-%d", i),
		})
	}
	if err := st.InsertBatch(ctx, mid); err != nil {
		t.Fatalf("seed mid: %v", err)
	}

	// Post-cluster: unique ts after sharedTS.
	post := make([]engram.Engram, 0, postCount)
	for i := 0; i < postCount; i++ {
		post = append(post, engram.Engram{
			Surface: "cursor",
			TS:      sharedTS + int64(1+i),
			Payload: fmt.Sprintf("post-%d", i),
		})
	}
	if err := st.InsertBatch(ctx, post); err != nil {
		t.Fatalf("seed post: %v", err)
	}

	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/export", srv.Addr().String()))
	if err != nil {
		t.Fatalf("GET /export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	// Some payloads are short; default buffer (64KB) is enough.
	scanner.Buffer(make([]byte, 1<<16), 4<<20)
	var (
		preSeen, midSeen, postSeen int
		summary                    map[string]any
	)
	for scanner.Scan() {
		line := scanner.Text()
		// Final summary line begins with "{"_export_complete":true...".
		if strings.Contains(line, "_export_complete") {
			if err := json.Unmarshal([]byte(line), &summary); err != nil {
				t.Errorf("summary parse: %v (line=%q)", err, line)
			}
			continue
		}
		var e engram.Engram
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("ndjson parse: %v (line=%q)", err, line)
			continue
		}
		switch {
		case strings.HasPrefix(e.Payload, "pre-"):
			preSeen++
		case strings.HasPrefix(e.Payload, "mid-"):
			midSeen++
		case strings.HasPrefix(e.Payload, "post-"):
			postSeen++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}

	if preSeen != preCount {
		t.Errorf("pre rows: got %d, want %d", preSeen, preCount)
	}
	if midSeen != midCount {
		t.Errorf("MID-CLUSTER ROWS (shared-ts) DROPPED: got %d, want %d "+
			"(this is the boundary-row data-loss regression)", midSeen, midCount)
	}
	if postSeen != postCount {
		t.Errorf("post rows: got %d, want %d", postSeen, postCount)
	}
	if summary != nil {
		gotCount, _ := summary["_count"].(float64)
		if int(gotCount) != preCount+midCount+postCount {
			t.Errorf("summary _count: got %v, want %d",
				gotCount, preCount+midCount+postCount)
		}
	}
}

// TestExportEmptyStore returns just the summary line.
func TestExportEmptyStore(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()
	resp, err := http.Get(fmt.Sprintf("http://%s/export", srv.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<16), 4<<20)
	var lines int
	for scanner.Scan() {
		lines++
	}
	if lines != 1 {
		t.Errorf("empty export: got %d lines, want 1 (summary only)", lines)
	}
}

// TestExportSurfaceFilter returns only rows for the named surface.
func TestExportSurfaceFilter(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()
	now := time.Now().UnixNano()
	_, _ = st.Insert(ctx, engram.Engram{Surface: "cursor", TS: now, Payload: "cursor-1"})
	_, _ = st.Insert(ctx, engram.Engram{Surface: "claude_code", TS: now + 1, Payload: "cc-1"})
	_, _ = st.Insert(ctx, engram.Engram{Surface: "cursor", TS: now + 2, Payload: "cursor-2"})

	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()
	resp, err := http.Get(fmt.Sprintf("http://%s/export?surface=cursor", srv.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<16), 4<<20)
	var ccSeen, cursorSeen int
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "_export_complete") {
			continue
		}
		var e engram.Engram
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		switch e.Surface {
		case "cursor":
			cursorSeen++
		case "claude_code":
			ccSeen++
		}
	}
	if cursorSeen != 2 {
		t.Errorf("cursor rows exported: got %d, want 2", cursorSeen)
	}
	if ccSeen != 0 {
		t.Errorf("claude_code rows leaked through cursor filter: %d", ccSeen)
	}
}
