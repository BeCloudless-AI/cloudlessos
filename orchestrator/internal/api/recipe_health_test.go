package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func noRedirectTestClient() *http.Client {
	return &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestRecipeHealthAcceptsOnlyDirectTwoHundredResponses(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusUnauthorized, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if status == http.StatusFound {
				w.Header().Set("Location", "/ready")
			}
			w.WriteHeader(status)
		}))
		err := probeRecipeHealth(context.Background(), noRedirectTestClient(), server.URL)
		server.Close()
		if err == nil {
			t.Fatalf("health status %d was accepted", status)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	if err := probeRecipeHealth(context.Background(), noRedirectTestClient(), server.URL); err != nil {
		t.Fatalf("direct 2xx health was rejected: %v", err)
	}
}

func TestRecipePrivateContractRequiresCloudlessModelIdentity(t *testing.T) {
	for _, payload := range []string{
		`{"data":[]}`,
		`{"data":[{"id":"upstream-model-name"}]}`,
		`not-json`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(payload))
		}))
		err := probeRecipePrivateContract(context.Background(), noRedirectTestClient(), server.URL)
		server.Close()
		if err == nil {
			t.Fatalf("invalid private contract was accepted: %s", payload)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"cloudless"}]}`))
	}))
	defer server.Close()
	if err := probeRecipePrivateContract(context.Background(), noRedirectTestClient(), server.URL); err != nil {
		t.Fatalf("Cloudless private contract was rejected: %v", err)
	}
}
