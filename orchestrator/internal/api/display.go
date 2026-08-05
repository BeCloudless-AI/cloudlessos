package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudless/orchestrator/internal/desktop"
	"github.com/cloudless/orchestrator/internal/displaylayout"
	"github.com/cloudless/orchestrator/internal/state"
)

type displayMode struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Current   bool `json:"current,omitempty"`
	Preferred bool `json:"preferred,omitempty"`
}

type displayOutput struct {
	Name    string        `json:"name"`
	Primary bool          `json:"primary,omitempty"`
	Width   int           `json:"width,omitempty"`
	Height  int           `json:"height,omitempty"`
	X       int           `json:"x,omitempty"`
	Y       int           `json:"y,omitempty"`
	Modes   []displayMode `json:"modes"`
}

type displaySnapshot struct {
	Available  bool                    `json:"available"`
	Output     string                  `json:"output,omitempty"`
	Width      int                     `json:"width,omitempty"`
	Height     int                     `json:"height,omitempty"`
	Layout     string                  `json:"layout,omitempty"`
	Outputs    []displayOutput         `json:"outputs,omitempty"`
	Configured state.DisplayPreference `json:"configured,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

type pendingDisplayChange struct {
	Schema           string                  `json:"schema"`
	Token            string                  `json:"token"`
	Selected         state.DisplayPreference `json:"selected"`
	Previous         state.DisplayPreference `json:"previous"`
	ExpiresAt        time.Time               `json:"expiresAt"`
	Timer            *time.Timer             `json:"-"`
	RollbackAttempts int                     `json:"-"`
}

func queryDisplays(ctx context.Context) (displaySnapshot, error) {
	data, err := desktop.NewClient().QueryDisplay(ctx)
	if err != nil {
		return displaySnapshot{}, fmt.Errorf("display service unavailable: %w", err)
	}
	return parseXrandr(data)
}

func parseXrandr(raw string) (displaySnapshot, error) {
	parsed, err := displaylayout.Parse(raw)
	if err != nil {
		return displaySnapshot{}, err
	}
	pref := displaylayout.DetectPreference(parsed)
	snapshot := displaySnapshot{Available: true, Output: pref.Output, Width: pref.Width, Height: pref.Height, Layout: pref.Layout}
	for _, output := range parsed.Outputs {
		dst := displayOutput{Name: output.Name, Primary: output.Primary, Width: output.Width, Height: output.Height, X: output.X, Y: output.Y}
		for _, mode := range output.Modes {
			dst.Modes = append(dst.Modes, displayMode{Width: mode.Width, Height: mode.Height, Current: mode.Current, Preferred: mode.Preferred})
		}
		snapshot.Outputs = append(snapshot.Outputs, dst)
	}
	return snapshot, nil
}

func applyDisplayMode(ctx context.Context, layout, output string, width, height int) error {
	if err := desktop.NewClient().ApplyDisplay(ctx, layout, output, width, height); err != nil {
		return fmt.Errorf("could not change display mode: %w", err)
	}
	return nil
}

func (s *Server) queryDisplay(ctx context.Context) (displaySnapshot, error) {
	if s.displayQuery != nil {
		return s.displayQuery(ctx)
	}
	return queryDisplays(ctx)
}

func (s *Server) applyDisplay(ctx context.Context, layout, output string, width, height int) error {
	if s.displayApply != nil {
		return s.displayApply(ctx, layout, output, width, height)
	}
	return applyDisplayMode(ctx, layout, output, width, height)
}

func layoutSnapshot(snapshot displaySnapshot) displaylayout.Snapshot {
	result := displaylayout.Snapshot{}
	for _, output := range snapshot.Outputs {
		dst := displaylayout.Output{Name: output.Name, Primary: output.Primary, Width: output.Width, Height: output.Height, X: output.X, Y: output.Y}
		for _, mode := range output.Modes {
			dst.Modes = append(dst.Modes, displaylayout.Mode{Width: mode.Width, Height: mode.Height, Current: mode.Current, Preferred: mode.Preferred})
		}
		result.Outputs = append(result.Outputs, dst)
	}
	return result
}

func layoutPreference(pref state.DisplayPreference) displaylayout.Preference {
	return displaylayout.Preference{Layout: pref.Layout, Output: pref.Output, Width: pref.Width, Height: pref.Height}
}

func statePreference(pref displaylayout.Preference) state.DisplayPreference {
	return state.DisplayPreference{Layout: pref.Layout, Output: pref.Output, Width: pref.Width, Height: pref.Height}
}

func displayChangeToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Server) pendingDisplayPath() string {
	if s.state == nil {
		return ""
	}
	return filepath.Join(s.state.Dir(), "display-change-pending.json")
}

func (s *Server) persistPendingDisplay(change *pendingDisplayChange) error {
	path := s.pendingDisplayPath()
	if path == "" {
		return nil
	}
	data, err := json.Marshal(change)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Server) clearPendingDisplay() error {
	path := s.pendingDisplayPath()
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Server) recoverPendingDisplayChange() {
	path := s.pendingDisplayPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Printf("Cloudless could not read pending display recovery: %v\n", err)
		return
	}
	var change pendingDisplayChange
	if json.Unmarshal(data, &change) != nil || (change.Schema != "cloudless.display-change.v1" && change.Schema != "cloudless.display-change.v2") ||
		change.Token == "" || change.Previous.Output == "" || change.Previous.Width <= 0 || change.Previous.Height <= 0 {
		fmt.Printf("Cloudless found an invalid pending display recovery record\n")
		return
	}
	if change.Previous.Layout == "" {
		change.Previous.Layout = displaylayout.LayoutSingle
	}
	if change.Selected.Layout == "" {
		change.Selected.Layout = displaylayout.LayoutSingle
	}
	s.displayMu.Lock()
	if s.displayChange != nil {
		s.displayMu.Unlock()
		return
	}
	s.displayChange = &change
	delay := s.displayRecoveryDelay
	if delay <= 0 {
		delay = time.Second
	}
	change.Timer = time.AfterFunc(delay, func() {
		if rollbackErr := s.rollbackDisplay(change.Token); rollbackErr != nil && rollbackErr.Error() != "display confirmation expired" {
			fmt.Printf("Cloudless pending display recovery failed: %v\n", rollbackErr)
		}
	})
	s.displayMu.Unlock()
}

func (s *Server) rollbackDisplay(token string) error {
	s.displayMu.Lock()
	defer s.displayMu.Unlock()
	change := s.displayChange
	if change == nil || change.Token != token {
		return errors.New("display confirmation expired")
	}
	if change.Timer != nil {
		change.Timer.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.applyDisplay(ctx, change.Previous.Layout, change.Previous.Output, change.Previous.Width, change.Previous.Height); err != nil {
		change.RollbackAttempts++
		if change.RollbackAttempts < 30 {
			change.Timer = time.AfterFunc(2*time.Second, func() {
				if retryErr := s.rollbackDisplay(token); retryErr != nil && retryErr.Error() != "display confirmation expired" {
					fmt.Printf("Cloudless display rollback retry failed: %v\n", retryErr)
				}
			})
		}
		return err
	}
	if err := s.clearPendingDisplay(); err != nil {
		return fmt.Errorf("clear pending display recovery: %w", err)
	}
	s.displayChange = nil
	return nil
}

func (s *Server) displayGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	snapshot, err := s.queryDisplay(ctx)
	if s.state != nil {
		snapshot.Configured = s.state.DisplayPreference()
	}
	if err != nil {
		snapshot.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) displaySet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "display" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "display confirmation header required"})
		return
	}
	var request state.DisplayPreference
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid display request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	snapshot, err := s.queryDisplay(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	if request.Layout == "" {
		request.Layout = displaylayout.LayoutSingle
	}
	if !displaylayout.ValidLayout(request.Layout) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that display layout is not supported"})
		return
	}
	parsed := layoutSnapshot(snapshot)
	previous := statePreference(displaylayout.DetectPreference(parsed))
	plan, err := displaylayout.BuildPlan(parsed, layoutPreference(request))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if plan.Effective.Layout != request.Layout || plan.Effective.Output != request.Output ||
		plan.Effective.Width != request.Width || plan.Effective.Height != request.Height {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that layout or resolution is not supported by the connected displays"})
		return
	}
	request = statePreference(plan.Effective)
	if previous.Width <= 0 || previous.Height <= 0 || previous.Output == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the current display mode cannot be restored safely"})
		return
	}
	token, err := displayChangeToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create display confirmation"})
		return
	}
	delay := s.displayDelay
	if delay <= 0 {
		delay = 20 * time.Second
	}
	s.displayMu.Lock()
	if s.displayChange != nil {
		s.displayMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "confirm or revert the current display change first"})
		return
	}
	change := &pendingDisplayChange{
		Schema: "cloudless.display-change.v2", Token: token, Selected: request, Previous: previous,
		ExpiresAt: time.Now().Add(delay).UTC(),
	}
	if err := s.persistPendingDisplay(change); err != nil {
		s.displayMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create a durable display rollback"})
		return
	}
	if err := s.applyDisplay(ctx, request.Layout, request.Output, request.Width, request.Height); err != nil {
		_ = s.clearPendingDisplay()
		s.displayMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.displayChange = change
	change.Timer = time.AfterFunc(delay, func() {
		if err := s.rollbackDisplay(token); err != nil && err.Error() != "display confirmation expired" {
			fmt.Printf("Cloudless display rollback failed: %v\n", err)
		}
	})
	s.displayMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"applied": true, "confirmationRequired": true, "token": token,
		"confirmSeconds": int(delay.Round(time.Second) / time.Second), "configured": request,
	})
}

// displayNormalize is called by kiosk startup, connector hotplug handling, and
// repair. It enforces the saved topology, or the safe default (mirror for
// multiple unknown outputs, single for one) before the browser is shown.
func (s *Server) displayNormalize(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "display-normalize" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "display normalization header required"})
		return
	}
	s.displayMu.Lock()
	defer s.displayMu.Unlock()
	if s.displayChange != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a display change is awaiting confirmation"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	snapshot, err := s.queryDisplay(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	requested := state.DisplayPreference{}
	if s.state != nil {
		requested = s.state.DisplayPreference()
	}
	plan, err := displaylayout.BuildPlan(layoutSnapshot(snapshot), layoutPreference(requested))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if plan.WasChanged {
		if err := s.applyDisplay(ctx, plan.Effective.Layout, plan.Effective.Output, plan.Effective.Width, plan.Effective.Height); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"normalized": true, "changed": plan.WasChanged, "configured": statePreference(plan.Effective),
		"fallback": plan.Fallback, "reason": plan.Reason,
	})
}

func (s *Server) displayConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "display-confirm" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "display confirmation header required"})
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid display confirmation"})
		return
	}
	s.displayMu.Lock()
	change := s.displayChange
	if change == nil || change.Token != request.Token {
		s.displayMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "display confirmation expired"})
		return
	}
	if s.state != nil {
		if err := s.state.SetDisplayPreference(change.Selected); err != nil {
			s.displayMu.Unlock()
			_ = s.rollbackDisplay(request.Token)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save display preference; the previous mode was restored"})
			return
		}
	}
	if err := s.clearPendingDisplay(); err != nil {
		if s.state != nil {
			_ = s.state.SetDisplayPreference(change.Previous)
		}
		s.displayMu.Unlock()
		_ = s.rollbackDisplay(request.Token)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not finish display confirmation; the previous mode was restored"})
		return
	}
	if change.Timer != nil {
		change.Timer.Stop()
	}
	s.displayChange = nil
	s.displayMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": true, "configured": change.Selected})
}

func (s *Server) displayRevert(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "display-revert" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "display revert header required"})
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid display revert request"})
		return
	}
	if err := s.rollbackDisplay(request.Token); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"reverted": true})
}
