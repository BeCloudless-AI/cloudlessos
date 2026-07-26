package api

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestEmbeddedWebToastUsesThemeIndependentAccessibleColors(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)

	for _, want := range []string{
		"background: var(--toast-bg); color: var(--toast-ink)",
		"background: var(--toast-error-bg); color: var(--toast-error-ink)",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI toast is missing %q", want)
		}
	}

	colors := map[string]string{
		"--toast-bg":        "#111528",
		"--toast-ink":       "#ffffff",
		"--toast-error-bg":  "#a9262d",
		"--toast-error-ink": "#ffffff",
	}
	for token, value := range colors {
		declaration := token + ": " + value
		if strings.Count(page, declaration) != 1 {
			t.Fatalf("%s must have exactly one shared declaration, independent of theme", token)
		}
	}

	for _, pair := range [][2]string{
		{colors["--toast-bg"], colors["--toast-ink"]},
		{colors["--toast-error-bg"], colors["--toast-error-ink"]},
	} {
		if ratio := contrastRatio(pair[0], pair[1]); ratio < 4.5 {
			t.Fatalf("toast colors %s on %s have insufficient contrast %.2f:1", pair[1], pair[0], ratio)
		}
	}
}

func contrastRatio(a, b string) float64 {
	lighter, darker := relativeLuminance(a), relativeLuminance(b)
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(hex string) float64 {
	if len(hex) != 7 || hex[0] != '#' {
		panic(fmt.Sprintf("invalid test color %q", hex))
	}
	channels := make([]float64, 3)
	for i := range channels {
		value, err := strconv.ParseUint(hex[1+i*2:3+i*2], 16, 8)
		if err != nil {
			panic(err)
		}
		channel := float64(value) / 255
		if channel <= 0.04045 {
			channels[i] = channel / 12.92
		} else {
			channels[i] = math.Pow((channel+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}
