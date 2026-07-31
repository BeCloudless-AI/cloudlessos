package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
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

func TestTerminalProxyConcurrentLocalSessionsRemainIsolated(t *testing.T) {
	var upstreamRequests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequests.Add(1)
		w.Header().Set("X-Terminal-Request", r.URL.Query().Get("id"))
		_, _ = io.WriteString(w, r.URL.Path+"?"+r.URL.RawQuery)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler := terminalProxyHandler(target)

	const sessions = 64
	var wg sync.WaitGroup
	errors := make(chan error, sessions*2)
	for i := 0; i < sessions; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/terminal/ws?id=%d", id), nil)
			request.RemoteAddr = fmt.Sprintf("127.0.0.1:%d", 40000+id)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("X-Terminal-Request") != fmt.Sprint(id) ||
				response.Body.String() != fmt.Sprintf("/terminal/ws?id=%d", id) {
				errors <- fmt.Errorf("local session %d response = %d %q %#v", id, response.Code, response.Body.String(), response.Header())
			}
		}(i)
		go func(id int) {
			defer wg.Done()
			request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/terminal/ws?id=remote-%d", id), nil)
			request.RemoteAddr = fmt.Sprintf("192.0.2.%d:45000", (id%200)+1)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				errors <- fmt.Errorf("remote session %d status = %d", id, response.Code)
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if got := upstreamRequests.Load(); got != sessions {
		t.Fatalf("upstream requests = %d, want %d admitted local sessions only", got, sessions)
	}
}
