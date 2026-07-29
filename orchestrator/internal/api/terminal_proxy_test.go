package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTerminalProxyIsLoopbackOnlyAndPreservesBasePath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler := terminalProxyHandler(target)

	local := httptest.NewRequest(http.MethodGet, "/terminal/ws", nil)
	local.RemoteAddr = "127.0.0.1:43210"
	localResponse := httptest.NewRecorder()
	handler.ServeHTTP(localResponse, local)
	if localResponse.Code != http.StatusOK || localResponse.Body.String() != "/terminal/ws" {
		t.Fatalf("local response = %d %q", localResponse.Code, localResponse.Body.String())
	}
	if localResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("terminal responses may be cached")
	}

	remote := httptest.NewRequest(http.MethodGet, "/terminal/", nil)
	remote.RemoteAddr = "192.0.2.10:43210"
	remoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf("remote response status = %d", remoteResponse.Code)
	}
}
