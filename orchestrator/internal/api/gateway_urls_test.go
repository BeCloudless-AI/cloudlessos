package api

import "testing"

func TestGatewayClientURLsFollowExposureState(t *testing.T) {
	modelURL, agentURL := gatewayClientURLs(false, "192.168.50.109", 8766)
	if modelURL != "http://localhost:8766/v1" || agentURL != "http://localhost:8766/agent/v1" {
		t.Fatalf("local-only URLs = %q, %q", modelURL, agentURL)
	}

	modelURL, agentURL = gatewayClientURLs(true, "192.168.50.109", 8766)
	if modelURL != "http://192.168.50.109:8766/v1" || agentURL != "http://192.168.50.109:8766/agent/v1" {
		t.Fatalf("LAN URLs = %q, %q", modelURL, agentURL)
	}
}

func TestGatewayClientURLsFallBackWhenLANAddressIsUnavailable(t *testing.T) {
	modelURL, agentURL := gatewayClientURLs(true, "", 8766)
	if modelURL != "http://localhost:8766/v1" || agentURL != "http://localhost:8766/agent/v1" {
		t.Fatalf("fallback URLs = %q, %q", modelURL, agentURL)
	}
}
