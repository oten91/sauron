package status

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"sauron/config"
	"sauron/selector"
	"sauron/storage"

	"go.uber.org/zap"
)

// newReadyTestHandler builds a status Handler backed by a real selector/store so the
// readiness endpoint can be exercised end to end.
func newReadyTestHandler(t *testing.T, store *storage.HeightStore) *Handler {
	t.Helper()

	yaml := `
api: true
rpc: false
grpc: false
listen: ":3000"
timeouts:
  health_check: 5s
  proxy: 60s
networks:
  - name: "pocket"
    api_listen: ":8080"
internals:
  - name: node-1
    api: "https://node1.example.com"
    network: "pocket"
`
	f, err := os.CreateTemp("", "sauron-ready-*.yaml")
	if err != nil {
		t.Fatalf("create temp config: %v", err)
	}
	if _, err := f.WriteString(yaml); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp config: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	logger := zap.NewNop()
	loader, err := config.NewLoader(f.Name(), logger)
	if err != nil {
		t.Fatalf("create config loader: %v", err)
	}

	endpointStore := storage.NewExternalEndpointStore(logger)
	sel := selector.NewSelector(store, endpointStore, loader, logger)
	return NewHandler(sel, loader, logger)
}

// TestHandleReadyRequiresRoutableBackend verifies the hardened readiness behavior:
// a replica is Ready only when it can actually route to a backend, so a pod that has
// lost contact with all backends is pulled from the LB instead of absorbing traffic.
func TestHandleReadyRequiresRoutableBackend(t *testing.T) {
	store := storage.NewHeightStore()
	h := newReadyTestHandler(t, store)

	// No backend data yet -> NotReady.
	rec := httptest.NewRecorder()
	h.handleReady(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no routable backend, got %d", rec.Code)
	}

	// Fresh backend -> Ready.
	store.Update("pocket", "node-1", "api", 100, 10*time.Millisecond, "internal")
	rec = httptest.NewRecorder()
	h.handleReady(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 once a fresh backend exists, got %d", rec.Code)
	}
}
