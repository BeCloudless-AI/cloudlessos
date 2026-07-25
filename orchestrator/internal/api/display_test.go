package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseXrandrConnectedOutputsAndModes(t *testing.T) {
	snapshot, err := parseXrandr(`Screen 0: minimum 8 x 8, current 3840 x 2160, maximum 32767 x 32767
DP-0 connected primary 3840x2160+0+0 (normal left inverted right x axis y axis)
   3840x2160     60.00*+  59.94
   2560x1440     59.95
HDMI-0 disconnected (normal left inverted right x axis y axis)
DP-1 connected 1920x1080+3840+0 (normal left inverted right x axis y axis)
   1920x1080     60.00*+
`)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Available || snapshot.Output != "DP-0" || snapshot.Width != 3840 || snapshot.Height != 2160 {
		t.Fatalf("unexpected selected display: %+v", snapshot)
	}
	if len(snapshot.Outputs) != 2 {
		t.Fatalf("connected outputs = %d, want 2", len(snapshot.Outputs))
	}
	if len(snapshot.Outputs[0].Modes) != 2 || !snapshot.Outputs[0].Modes[0].Current || !snapshot.Outputs[0].Modes[0].Preferred {
		t.Fatalf("unexpected primary modes: %+v", snapshot.Outputs[0].Modes)
	}
}

func TestDisplaySetRequiresConfirmationHeader(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/system/display", strings.NewReader(`{"output":"DP-0","width":1920,"height":1080}`))
	rec := httptest.NewRecorder()
	s.displaySet(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
