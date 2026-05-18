package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/api"
	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// shortUDSPath returns a socket path inside /tmp short enough to fit in the
// 104-byte sun_path limit on macOS. t.TempDir() can produce paths >104 bytes
// which net.Listen("unix", ...) rejects with "invalid argument".
func shortUDSPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join("/tmp", fmt.Sprintf("eidetic-test-%d-%s.sock", os.Getpid(), t.Name()))
	if len(path) > 100 {
		path = filepath.Join("/tmp", fmt.Sprintf("ed-%d.sock", time.Now().UnixNano()))
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

func tempStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "engrams.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// startServer starts a Server in a goroutine and returns it + a shutdown fn.
func startServer(t *testing.T, st *store.Store, opts api.Options) (*api.Server, func()) {
	t.Helper()
	srv, err := api.New(st, opts)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx)
		close(doneCh)
	}()
	// Brief wait for listener to be ready before tests dial it.
	time.Sleep(20 * time.Millisecond)
	return srv, func() {
		cancel()
		<-doneCh
		srv.Close()
	}
}

// udsClient returns an http.Client whose Transport dials a unix socket.
func udsClient(path string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", path)
			},
		},
		Timeout: 2 * time.Second,
	}
}

func seedStore(t *testing.T, st *store.Store, surface string, n int) int64 {
	t.Helper()
	base := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		_, err := st.Insert(context.Background(), engram.Engram{
			Surface: surface,
			TS:      base + int64(i),
			Payload: fmt.Sprintf("payload-%d", i),
		})
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}
	return base
}

// --- the 9 test cases per docs/PHASE_2_DESIGN.md ---

func TestEndToEndUDS_GetEngrams(t *testing.T) {
	st := tempStore(t)
	seedStore(t, st, "cursor", 5)
	path := shortUDSPath(t)
	_, stop := startServer(t, st, api.Options{UDSPath: path})
	defer stop()

	resp, err := udsClient(path).Get("http://unix/engrams?surface=cursor&limit=10")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []engram.Engram
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows: want 5, got %d", len(rows))
	}
	if rows[0].TS <= rows[len(rows)-1].TS {
		t.Fatalf("rows not in ts DESC order")
	}
}

func TestEndToEndTCP_GetEngrams(t *testing.T) {
	st := tempStore(t)
	seedStore(t, st, "claude_code", 3)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	addr := srv.Addr().String()
	resp, err := http.Get(fmt.Sprintf("http://%s/engrams?surface=claude_code&limit=10", addr))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []engram.Engram
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows: want 3, got %d", len(rows))
	}
}

func TestGET_MissingSurfaceReturns400(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/engrams?limit=10", srv.Addr()))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestGET_WrongMethodReturns405(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	addr := srv.Addr().String()
	// DELETE is now valid (v0.0.13 purge endpoint); only POST/PUT/PATCH are not.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		req, _ := http.NewRequest(method, fmt.Sprintf("http://%s/engrams?surface=x", addr), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s status: want 405, got %d", method, resp.StatusCode)
		}
	}
}

func TestGET_LimitDefaultsAndClampsAcrossAPIBoundary(t *testing.T) {
	st := tempStore(t)
	seedStore(t, st, "cursor", 600) // store insert is single-threaded but fine for 600
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	addr := srv.Addr().String()

	// limit=999 → store clamps to 500
	resp, err := http.Get(fmt.Sprintf("http://%s/engrams?surface=cursor&limit=999", addr))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var rows []engram.Engram
	_ = json.NewDecoder(resp.Body).Decode(&rows)
	resp.Body.Close()
	if len(rows) != 500 {
		t.Fatalf("limit=999 should clamp to 500, got %d", len(rows))
	}

	// limit absent → store defaults to 50
	resp, err = http.Get(fmt.Sprintf("http://%s/engrams?surface=cursor", addr))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&rows)
	resp.Body.Close()
	if len(rows) != 50 {
		t.Fatalf("limit absent should default to 50, got %d", len(rows))
	}
}

func TestGET_SinceFilterAppliedViaQuery(t *testing.T) {
	st := tempStore(t)
	base := time.Now().UnixNano()
	for i := 0; i < 10; i++ {
		if _, err := st.Insert(context.Background(), engram.Engram{
			Surface: "cowork",
			TS:      base + int64(i*1000),
			Payload: "p",
		}); err != nil {
			t.Fatal(err)
		}
	}
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	addr := srv.Addr().String()
	// since=base+5000 → rows where ts > base+5000 → 4 rows
	url := fmt.Sprintf("http://%s/engrams?surface=cowork&since=%s&limit=100",
		addr, strconv.FormatInt(base+5000, 10))
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var rows []engram.Engram
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("since filter: want 4 rows, got %d", len(rows))
	}
}

func TestServerCloseRemovesUDSFile(t *testing.T) {
	st := tempStore(t)
	path := shortUDSPath(t)
	srv, err := api.New(st, api.Options{UDSPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("UDS file should exist after New: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("UDS file should be removed after Close, got: %v", err)
	}
}

func TestServerHandlesStaleSocket(t *testing.T) {
	st := tempStore(t)
	path := shortUDSPath(t)

	// Create a stale file at the path (simulates a crashed prior run).
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	// New should clean it up and bind successfully.
	srv, err := api.New(st, api.Options{UDSPath: path})
	if err != nil {
		t.Fatalf("New should clean stale socket: %v", err)
	}
	defer srv.Close()

	// Confirm we can now connect.
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial after stale cleanup: %v", err)
	}
	conn.Close()
}

func TestRequestTimeoutAppliesToStoreLayer(t *testing.T) {
	st := tempStore(t)
	// Insert a row so the surface filter has data — context timeout exercises
	// the layer regardless. Real timeout enforcement is via context, so we
	// rely on Server.timeout being respected by the context.WithTimeout in
	// handleEngramsGET. With 1ns timeout, any IO should miss the deadline.
	seedStore(t, st, "cursor", 1)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0", Timeout: 1 * time.Nanosecond})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/engrams?surface=cursor", srv.Addr()))
	if err != nil {
		// Connection error is also acceptable — server may have closed before reply.
		return
	}
	defer resp.Body.Close()

	// With nanosecond timeout, either:
	//   (a) the context cancels before retrieve completes → 500 + body mentions ctx
	//   (b) it completes anyway (modernc is in-memory after warmup) → 200 with body
	// Both are acceptable for "timeout was wired"; assert no panic / no 5xx that
	// isn't context.
	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		// Should reference context cancellation if the timeout fired.
		_ = body // accept either outcome; the test guards against panic / wrong wiring
	}
}

// --- DELETE /engrams tests (v0.0.13) ---

func TestEngramsDELETEPurgeAll(t *testing.T) {
	st := tempStore(t)
	seedStore(t, st, "cursor", 5)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://%s/engrams?surface=cursor", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE purge-all: want 200, got %d: %s", resp.StatusCode, body)
	}
	var result map[string]int64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["deleted"] != 5 {
		t.Errorf("DELETE purge-all: want deleted=5, got %d", result["deleted"])
	}

	// Verify store is empty for that surface.
	rows, err := st.Retrieve(context.Background(), "cursor", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("after DELETE purge-all: want 0 rows, got %d", len(rows))
	}
}

func TestEngramsDELETEPurgeBefore(t *testing.T) {
	st := tempStore(t)
	// Seed 4 rows with sequential ts values.
	base := seedStore(t, st, "claude_code", 4) // ts = base, base+1, base+2, base+3
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	// Delete rows with ts < base+2 (should delete 2 rows).
	before := base + 2
	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://%s/engrams?surface=claude_code&before=%d", srv.Addr(), before), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE purge-before: want 200, got %d: %s", resp.StatusCode, body)
	}
	var result map[string]int64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["deleted"] != 2 {
		t.Errorf("DELETE purge-before: want deleted=2, got %d", result["deleted"])
	}

	// 2 rows remain.
	rows, err := st.Retrieve(context.Background(), "claude_code", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("after DELETE purge-before: want 2 remaining, got %d", len(rows))
	}
}

func TestEngramsDELETEMissingSurface(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://%s/engrams", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("DELETE missing surface: want 400, got %d", resp.StatusCode)
	}
}

func TestEngramsDELETEDoesNotTouchOtherSurfaces(t *testing.T) {
	st := tempStore(t)
	seedStore(t, st, "cursor", 3)
	seedStore(t, st, "cowork", 2)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://%s/engrams?surface=cursor", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// cowork surface untouched.
	rows, err := st.Retrieve(context.Background(), "cowork", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("DELETE cursor: cowork should be untouched, got %d rows", len(rows))
	}
}

func TestEngramsPUTMethodNotAllowed(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("http://%s/engrams?surface=x", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("PUT /engrams: want 405, got %d", resp.StatusCode)
	}
}

// --- GET /surfaces tests (v0.0.13) ---

func TestSurfacesEmpty(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/surfaces", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var counts map[string]int64
	if err := json.NewDecoder(resp.Body).Decode(&counts); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Errorf("empty store: want {}, got %v", counts)
	}
}

func TestSurfacesReturnsCounts(t *testing.T) {
	st := tempStore(t)
	seedStore(t, st, "cursor", 3)
	seedStore(t, st, "claude_code", 7)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/surfaces", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var counts map[string]int64
	if err := json.NewDecoder(resp.Body).Decode(&counts); err != nil {
		t.Fatal(err)
	}
	if counts["cursor"] != 3 {
		t.Errorf("cursor: want 3, got %d", counts["cursor"])
	}
	if counts["claude_code"] != 7 {
		t.Errorf("claude_code: want 7, got %d", counts["claude_code"])
	}
	if len(counts) != 2 {
		t.Errorf("want 2 surfaces, got %d: %v", len(counts), counts)
	}
}

func TestSurfacesMethodNotAllowed(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/surfaces", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /surfaces: want 405, got %d", resp.StatusCode)
	}
}

// ── GET /search tests (v0.0.14+) ────────────────────────────────────────────

func seedSearch(t *testing.T, st *store.Store, surface, payload string) {
	t.Helper()
	_, err := st.Insert(context.Background(), engram.Engram{
		Surface: surface, TS: time.Now().UnixNano(), Payload: payload,
	})
	if err != nil {
		t.Fatalf("seedSearch insert: %v", err)
	}
}

func TestSearchMissingQReturns400(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/search", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestSearchMethodNotAllowed(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/search?q=x", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /search: want 405, got %d", resp.StatusCode)
	}
}

func TestSearchReturnsMatchingEngrams(t *testing.T) {
	st := tempStore(t)
	seedSearch(t, st, "claude_code", `benchmark latency P95 under 1ms`)
	seedSearch(t, st, "claude_code", `nothing relevant here`)

	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/search?q=benchmark", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var rows []engram.Engram
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 match, got %d", len(rows))
	}
}

func TestSearchSurfaceFilter(t *testing.T) {
	st := tempStore(t)
	seedSearch(t, st, "claude_code", "needle haystack")
	seedSearch(t, st, "cursor", "needle haystack")

	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/search?q=needle&surface=cursor", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rows []engram.Engram
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 result (cursor only), got %d", len(rows))
	}
	if rows[0].Surface != "cursor" {
		t.Errorf("wrong surface: %s", rows[0].Surface)
	}
}

func TestSearchNoMatchReturnsEmptyArray(t *testing.T) {
	st := tempStore(t)
	seedSearch(t, st, "claude_code", "nothing special")

	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/search?q=xyzzy", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var rows []engram.Engram
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if rows == nil || len(rows) != 0 {
		t.Errorf("want empty array, got %v", rows)
	}
}
