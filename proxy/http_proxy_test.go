package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"sauron/selector"
	"sauron/storage"

	"go.uber.org/zap"
)

// TestIsWebSocketRequest covers both detection paths: the standard Upgrade
// headers and the /websocket path fallback used when an upstream proxy
// (e.g. HAProxy) strips the hop-by-hop Connection/Upgrade headers.
func TestIsWebSocketRequest(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		connection string
		upgrade    string
		want       bool
	}{
		{"standard upgrade headers", "/", "Upgrade", "websocket", true},
		{"mixed case headers", "/", "upgrade", "WebSocket", true},
		{"connection list contains upgrade", "/", "keep-alive, Upgrade", "websocket", true},
		{"path fallback when headers stripped", "/websocket", "", "", true},
		{"path fallback ignores casing of value", "/websocket", "keep-alive", "", true},
		{"plain request, no upgrade, root path", "/", "keep-alive", "", false},
		{"upgrade header but wrong protocol", "/", "Upgrade", "h2c", false},
		{"non-websocket path without headers", "/status", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.connection != "" {
				req.Header.Set("Connection", tc.connection)
			}
			if tc.upgrade != "" {
				req.Header.Set("Upgrade", tc.upgrade)
			}
			if got := isWebSocketRequest(req); got != tc.want {
				t.Errorf("isWebSocketRequest(path=%q, Connection=%q, Upgrade=%q) = %v, want %v",
					tc.path, tc.connection, tc.upgrade, got, tc.want)
			}
		})
	}
}

// TestHandleWebSocketHonorsSelectedNode is a regression test for the node
// mismatch bug: handleWebSocket used to call GetBestNode again, re-running
// round-robin and checking WebSocket support on a potentially different node
// than the one whose target was already chosen in ServeHTTP. The fix threads
// the already-selected nodeMetrics in, so the support gate must reflect the
// passed metrics and never re-select.
//
// We pass nodeMetrics with WebSocketAvailable=false and a nil selector. If the
// gate honors the passed metrics it short-circuits with 503 before touching the
// selector or the connection. If it ever re-selects, it would dereference the
// nil selector and panic — making this a strict guard against regressing.
func TestHandleWebSocketHonorsSelectedNode(t *testing.T) {
	p := NewHTTPProxy(nil, nil, nil, zap.NewNop(), "rpc", "pocket")

	target, err := url.Parse("https://node1.example.com:26657")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/websocket", nil)
	rec := httptest.NewRecorder()

	nodeMetrics := &storage.NodeMetrics{WebSocketAvailable: false}
	decision := &selector.SelectionDecision{}

	p.handleWebSocket(rec, req, target, nodeMetrics, "node-1", "pocket", time.Now(), decision)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d when selected node lacks WebSocket support, got %d",
			http.StatusServiceUnavailable, rec.Code)
	}
}
