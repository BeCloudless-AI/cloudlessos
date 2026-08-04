// Package displaylayout owns the deterministic XRandR layout policy used by
// CloudlessOS. Unknown multi-output configurations mirror by default so the
// kiosk cannot disappear onto a phantom connector.
package displaylayout

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	LayoutSingle = "single"
	LayoutMirror = "mirror"
	LayoutExtend = "extend"
)

type Mode struct {
	Width     int
	Height    int
	Current   bool
	Preferred bool
}

type Output struct {
	Name    string
	Primary bool
	Width   int
	Height  int
	X       int
	Y       int
	Modes   []Mode
}

func (o Output) Active() bool { return o.Width > 0 && o.Height > 0 }

type Snapshot struct {
	Outputs []Output
}

type Preference struct {
	Layout string
	Output string
	Width  int
	Height int
}

type Plan struct {
	Args       []string
	Effective  Preference
	Fallback   bool
	Reason     string
	WasChanged bool
}

var (
	resolutionPattern = regexp.MustCompile(`^([0-9]+)x([0-9]+)$`)
	geometryPattern   = regexp.MustCompile(`^([0-9]+)x([0-9]+)([+-][0-9]+)([+-][0-9]+)$`)
)

func ValidLayout(layout string) bool {
	return layout == LayoutSingle || layout == LayoutMirror || layout == LayoutExtend
}

// Parse reads xrandr --query output. Connected-but-inactive outputs are kept;
// they matter when selecting a safe mirror or explicitly extended layout.
func Parse(raw string) (Snapshot, error) {
	var snapshot Snapshot
	var active *Output
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
			output := Output{Name: fields[0], Modes: []Mode{}}
			for _, field := range fields[2:] {
				if field == "primary" {
					output.Primary = true
				}
				if match := geometryPattern.FindStringSubmatch(field); match != nil {
					output.Width, _ = strconv.Atoi(match[1])
					output.Height, _ = strconv.Atoi(match[2])
					output.X, _ = strconv.Atoi(match[3])
					output.Y, _ = strconv.Atoi(match[4])
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
		mode := Mode{Width: width, Height: height}
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
	return snapshot, nil
}

func currentLayout(snapshot Snapshot) string {
	active := make([]Output, 0, len(snapshot.Outputs))
	for _, output := range snapshot.Outputs {
		if output.Active() {
			active = append(active, output)
		}
	}
	if len(active) <= 1 {
		return LayoutSingle
	}
	first := active[0]
	for _, output := range active[1:] {
		if output.X != first.X || output.Y != first.Y || output.Width != first.Width || output.Height != first.Height {
			return LayoutExtend
		}
	}
	return LayoutMirror
}

func primaryOutput(snapshot Snapshot) Output {
	for _, output := range snapshot.Outputs {
		if output.Primary {
			return output
		}
	}
	for _, output := range snapshot.Outputs {
		if output.Active() {
			return output
		}
	}
	return snapshot.Outputs[0]
}

// safeOutput prefers the active output currently anchored at the desktop
// origin. Firmware sometimes marks a phantom connector primary while placing
// the real panel at 0,0; choosing the origin keeps the single-display fallback
// visible when mirroring is impossible.
func safeOutput(snapshot Snapshot) Output {
	for _, output := range snapshot.Outputs {
		if output.Active() && output.X == 0 && output.Y == 0 {
			return output
		}
	}
	return primaryOutput(snapshot)
}

func findOutput(snapshot Snapshot, name string) (Output, bool) {
	for _, output := range snapshot.Outputs {
		if output.Name == name {
			return output, true
		}
	}
	return Output{}, false
}

func hasMode(output Output, width, height int) bool {
	for _, mode := range output.Modes {
		if mode.Width == width && mode.Height == height {
			return true
		}
	}
	return false
}

func bestMode(output Output, width, height int) Mode {
	if width > 0 && height > 0 {
		for _, mode := range output.Modes {
			if mode.Width == width && mode.Height == height {
				return mode
			}
		}
	}
	for _, mode := range output.Modes {
		if mode.Current {
			return mode
		}
	}
	for _, mode := range output.Modes {
		if mode.Preferred {
			return mode
		}
	}
	if len(output.Modes) > 0 {
		return output.Modes[0]
	}
	return Mode{Width: output.Width, Height: output.Height}
}

func commonMode(outputs []Output, width, height int) (Mode, bool) {
	if width > 0 && height > 0 {
		common := true
		for _, output := range outputs {
			common = common && hasMode(output, width, height)
		}
		if common {
			return Mode{Width: width, Height: height}, true
		}
	}
	candidates := append([]Mode(nil), outputs[0].Modes...)
	sort.SliceStable(candidates, func(i, j int) bool {
		ip, jp := candidates[i].Width*candidates[i].Height, candidates[j].Width*candidates[j].Height
		if ip == jp {
			return candidates[i].Preferred && !candidates[j].Preferred
		}
		return ip > jp
	})
	for _, candidate := range candidates {
		ok := candidate.Width > 0 && candidate.Height > 0
		for _, output := range outputs[1:] {
			ok = ok && hasMode(output, candidate.Width, candidate.Height)
		}
		if ok {
			return candidate, true
		}
	}
	return Mode{}, false
}

// BuildPlan resolves a requested/saved preference against current connectors.
// Empty or stale preferences never inherit X's extended layout: multiple
// outputs mirror, while one output becomes a primary single-display desktop.
func BuildPlan(snapshot Snapshot, requested Preference) (Plan, error) {
	if len(snapshot.Outputs) == 0 {
		return Plan{}, errors.New("no connected display was detected")
	}
	plan := Plan{}
	selected, selectedFound := findOutput(snapshot, requested.Output)
	if !selectedFound {
		selected = safeOutput(snapshot)
		if requested.Output != "" {
			plan.Fallback = true
			plan.Reason = "the saved display is no longer connected"
		}
	}
	layout := requested.Layout
	if layout == "" {
		if requested.Output != "" {
			layout = LayoutSingle // legacy saved resolution
		} else if len(snapshot.Outputs) > 1 {
			layout = LayoutMirror
		} else {
			layout = LayoutSingle
		}
	}
	if !ValidLayout(layout) {
		return Plan{}, fmt.Errorf("invalid display layout %q", layout)
	}
	if !selectedFound && requested.Output != "" {
		if len(snapshot.Outputs) > 1 {
			layout = LayoutMirror
		} else {
			layout = LayoutSingle
		}
	}

	switch layout {
	case LayoutSingle:
		mode := bestMode(selected, requested.Width, requested.Height)
		if mode.Width <= 0 || mode.Height <= 0 {
			return Plan{}, fmt.Errorf("display %s has no usable mode", selected.Name)
		}
		plan.Args = append(plan.Args, "--output", selected.Name, "--mode", fmt.Sprintf("%dx%d", mode.Width, mode.Height), "--primary", "--pos", "0x0")
		for _, output := range snapshot.Outputs {
			if output.Name != selected.Name {
				plan.Args = append(plan.Args, "--output", output.Name, "--off")
			}
		}
		plan.Effective = Preference{Layout: layout, Output: selected.Name, Width: mode.Width, Height: mode.Height}
	case LayoutMirror:
		mode, ok := commonMode(snapshot.Outputs, requested.Width, requested.Height)
		if !ok {
			// Mirroring is safer than extending, but an output with no shared mode
			// cannot be mirrored by XRandR. Keep the selected display visible and
			// explicitly disable the rest rather than creating an invisible desktop.
			fallback, err := BuildPlan(snapshot, Preference{Layout: LayoutSingle, Output: selected.Name, Width: requested.Width, Height: requested.Height})
			fallback.Fallback = true
			fallback.Reason = "connected displays have no common mirror resolution"
			return fallback, err
		}
		plan.Args = append(plan.Args, "--output", selected.Name, "--mode", fmt.Sprintf("%dx%d", mode.Width, mode.Height), "--primary", "--pos", "0x0")
		for _, output := range snapshot.Outputs {
			if output.Name != selected.Name {
				plan.Args = append(plan.Args, "--output", output.Name, "--mode", fmt.Sprintf("%dx%d", mode.Width, mode.Height), "--same-as", selected.Name)
			}
		}
		plan.Effective = Preference{Layout: layout, Output: selected.Name, Width: mode.Width, Height: mode.Height}
	case LayoutExtend:
		mode := bestMode(selected, requested.Width, requested.Height)
		if mode.Width <= 0 || mode.Height <= 0 {
			return Plan{}, fmt.Errorf("display %s has no usable mode", selected.Name)
		}
		plan.Args = append(plan.Args, "--output", selected.Name, "--mode", fmt.Sprintf("%dx%d", mode.Width, mode.Height), "--primary", "--pos", "0x0")
		previous := selected.Name
		for _, output := range snapshot.Outputs {
			if output.Name == selected.Name {
				continue
			}
			otherMode := bestMode(output, 0, 0)
			if otherMode.Width <= 0 || otherMode.Height <= 0 {
				return Plan{}, fmt.Errorf("display %s has no usable mode", output.Name)
			}
			plan.Args = append(plan.Args, "--output", output.Name, "--mode", fmt.Sprintf("%dx%d", otherMode.Width, otherMode.Height), "--right-of", previous)
			previous = output.Name
		}
		plan.Effective = Preference{Layout: layout, Output: selected.Name, Width: mode.Width, Height: mode.Height}
	}
	plan.WasChanged = !Matches(snapshot, plan.Effective)
	return plan, nil
}

// Matches verifies the safety invariants of an effective policy after xrandr
// applies it. Extended mode accepts any non-overlapping arrangement because X
// may normalize coordinates while preserving the explicit topology.
func Matches(snapshot Snapshot, pref Preference) bool {
	selected, ok := findOutput(snapshot, pref.Output)
	if !ok || !selected.Primary || selected.Width != pref.Width || selected.Height != pref.Height {
		return false
	}
	active := []Output{}
	for _, output := range snapshot.Outputs {
		if output.Active() {
			active = append(active, output)
		}
	}
	switch pref.Layout {
	case LayoutSingle:
		return len(active) == 1 && active[0].Name == selected.Name && selected.X == 0 && selected.Y == 0
	case LayoutMirror:
		if len(active) != len(snapshot.Outputs) {
			return false
		}
		for _, output := range active {
			if output.X != selected.X || output.Y != selected.Y || output.Width != pref.Width || output.Height != pref.Height {
				return false
			}
		}
		return true
	case LayoutExtend:
		if len(active) != len(snapshot.Outputs) {
			return false
		}
		for i, a := range active {
			for _, b := range active[i+1:] {
				if a.X < b.X+b.Width && a.X+a.Width > b.X && a.Y < b.Y+b.Height && a.Y+a.Height > b.Y {
					return false
				}
			}
		}
		return true
	default:
		return currentLayout(snapshot) == pref.Layout
	}
}

func DetectPreference(snapshot Snapshot) Preference {
	selected := primaryOutput(snapshot)
	mode := bestMode(selected, selected.Width, selected.Height)
	return Preference{Layout: currentLayout(snapshot), Output: selected.Name, Width: mode.Width, Height: mode.Height}
}
