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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/desktop"
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
	Modes   []displayMode `json:"modes"`
}

type displaySnapshot struct {
	Available  bool                    `json:"available"`
	Output     string                  `json:"output,omitempty"`
	Width      int                     `json:"width,omitempty"`
	Height     int                     `json:"height,omitempty"`
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

var resolutionPattern = regexp.MustCompile(`^([0-9]+)x([0-9]+)$`)

func queryDisplays(ctx context.Context) (displaySnapshot, error) {
	data, err := desktop.NewClient().QueryDisplay(ctx)
	if err != nil {
		return displaySnapshot{}, fmt.Errorf("display service unavailable: %w", err)
	}
	return parseXrandr(data)
}

func parseXrandr(raw string) (displaySnapshot, error) {
	var snapshot displaySnapshot
	var active *displayOutput
	seenModes := map[string]map[string]int{}
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			fields := strings.Fields(line)
			active = nil
			if len(fields) < 2 || fields[1] != "connected" {
				continue
			}
			output := displayOutput{Name: fields[0], Modes: []displayMode{}}
			for _, field := range fields[2:] {
				if field == "primary" {
					output.Primary = true
				}
				if strings.Contains(field, "+") {
					if match := resolutionPattern.FindStringSubmatch(strings.SplitN(field, "+", 2)[0]); match != nil {
						output.Width, _ = strconv.Atoi(match[1])
						output.Height, _ = strconv.Atoi(match[2])
					}
				}
			}
			snapshot.Outputs = append(snapshot.Outputs, output)
			active = &snapshot.Outputs[len(snapshot.Outputs)-1]
			seenModes[output.Name] = map[string]int{}
			continue
		}
		if active == nil {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		match := resolutionPattern.FindStringSubmatch(fields[0])
		if match == nil {
			continue
		}
		width, _ := strconv.Atoi(match[1])
		height, _ := strconv.Atoi(match[2])
		key := fmt.Sprintf("%dx%d", width, height)
		mode := displayMode{Width: width, Height: height}
		for _, rate := range fields[1:] {
			mode.Current = mode.Current || strings.Contains(rate, "*")
			mode.Preferred = mode.Preferred || strings.Contains(rate, "+")
		}
		if index, ok := seenModes[active.Name][key]; ok {
			active.Modes[index].Current = active.Modes[index].Current || mode.Current
			active.Modes[index].Preferred = active.Modes[index].Preferred || mode.Preferred
			continue
		}
		seenModes[active.Name][key] = len(active.Modes)
		active.Modes = append(active.Modes, mode)
	}
	if len(snapshot.Outputs) == 0 {
		return snapshot, errors.New("no connected display was detected")
	}
	sort.SliceStable(snapshot.Outputs, func(i, j int) bool {
		return snapshot.Outputs[i].Primary && !snapshot.Outputs[j].Primary
	})
	selected := &snapshot.Outputs[0]
	for i := range snapshot.Outputs {
		if snapshot.Outputs[i].Primary {
			selected = &snapshot.Outputs[i]
			break
		}
	}
	snapshot.Available = true
	snapshot.Output = selected.Name
	snapshot.Width = selected.Width
	snapshot.Height = selected.Height
	return snapshot, nil
}

func applyDisplayMode(ctx context.Context, output string, width, height int) error {
	if err := desktop.NewClient().ApplyDisplay(ctx, output, width, height); err != nil {
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

func (s *Server) applyDisplay(ctx context.Context, output string, width, height int) error {
	if s.displayApply != nil {
		return s.displayApply(ctx, output, width, height)
	}
	return applyDisplayMode(ctx, output, width, height)
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
	if json.Unmarshal(data, &change) != nil || change.Schema != "cloudless.display-change.v1" ||
		change.Token == "" || change.Previous.Output == "" || change.Previous.Width <= 0 || change.Previous.Height <= 0 {
		fmt.Printf("Cloudless found an invalid pending display recovery record\n")
		return
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
	if err := s.applyDisplay(ctx, change.Previous.Output, change.Previous.Width, change.Previous.Height); err != nil {
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
	valid := false
	var previous state.DisplayPreference
	for _, output := range snapshot.Outputs {
		if output.Name != request.Output {
			continue
		}
		previous = state.DisplayPreference{Output: output.Name, Width: output.Width, Height: output.Height}
		for _, mode := range output.Modes {
			if mode.Width == request.Width && mode.Height == request.Height {
				valid = true
				break
			}
		}
	}
	if !valid {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that resolution is not supported by the selected display"})
		return
	}
	if previous.Width <= 0 || previous.Height <= 0 {
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
		Schema: "cloudless.display-change.v1", Token: token, Selected: request, Previous: previous,
		ExpiresAt: time.Now().Add(delay).UTC(),
	}
	if err := s.persistPendingDisplay(change); err != nil {
		s.displayMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create a durable display rollback"})
		return
	}
	if err := s.applyDisplay(ctx, request.Output, request.Width, request.Height); err != nil {
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
