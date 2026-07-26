package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSystemInputReportsMouseWithoutKeyboard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices")
	data := "N: Name=\"Power Button\"\nH: Handlers=kbd event0\n\nN: Name=\"USB Mouse\"\nH: Handlers=mouse0 event2\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDLESS_INPUT_DEVICES_PATH", path)
	recorder := httptest.NewRecorder()
	(&Server{}).systemInput(recorder, httptest.NewRequest(http.MethodGet, "/api/system/input", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var state struct {
		Keyboard bool `json:"keyboard"`
		Mouse    bool `json:"mouse"`
		Pointer  bool `json:"pointer"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Keyboard || !state.Mouse || !state.Pointer {
		t.Fatalf("input state = %+v, want mouse/pointer without keyboard", state)
	}
}
