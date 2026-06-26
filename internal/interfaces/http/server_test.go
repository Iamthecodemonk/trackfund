package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerAddsCorsHeadersAndHandlesPreflight(t *testing.T) {
	server := NewServer(nil)

	req := httptest.NewRequest(http.MethodOptions, "/funds", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")

	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rr.Code)
	}

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("expected allow-origin to echo request origin, got %q", got)
	}

	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected allow-methods header to be set")
	}

	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("expected allow-headers header to be set")
	}
}
