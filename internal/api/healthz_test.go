package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/api"
)

func TestHealthzReturnsOK(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/healthz", srv.Addr()))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("body=%v, want status=ok", body)
	}
}

func TestHealthzRejectsNonGET(t *testing.T) {
	st := tempStore(t)
	srv, stop := startServer(t, st, api.Options{TCPAddr: "127.0.0.1:0"})
	defer stop()

	for _, method := range []string{"POST", "PUT", "DELETE"} {
		req, _ := http.NewRequest(method, fmt.Sprintf("http://%s/healthz", srv.Addr()), strings.NewReader(""))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /healthz status %d, want 405", method, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
