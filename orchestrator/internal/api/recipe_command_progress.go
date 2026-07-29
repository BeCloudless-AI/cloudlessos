package api

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	recipeByteProgressPattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?i?b)\s*/\s*([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?i?b)`)
	recipeElapsedPattern      = regexp.MustCompile(`(?:^|\s)([0-9]+(?:\.[0-9]+)?)s(?:\s|$)`)
)

type recipeCommandProgress struct {
	phase     string
	label     string
	started   time.Time
	firstDone int64
	lastDone  int64
}

func newRecipeCommandProgress(phase, label string) *recipeCommandProgress {
	return &recipeCommandProgress{phase: phase, label: label, started: time.Now()}
}

func recipeByteValue(number, unit string) (int64, bool) {
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	multiplier := float64(1)
	switch strings.ToLower(unit) {
	case "kb":
		multiplier = 1_000
	case "kib":
		multiplier = 1 << 10
	case "mb":
		multiplier = 1_000_000
	case "mib":
		multiplier = 1 << 20
	case "gb":
		multiplier = 1_000_000_000
	case "gib":
		multiplier = 1 << 30
	case "tb":
		multiplier = 1_000_000_000_000
	case "tib":
		multiplier = 1 << 40
	case "b":
	default:
		return 0, false
	}
	return int64(math.Round(value * multiplier)), true
}

func recipeProgressETA(remaining int64, rate float64) string {
	if remaining <= 0 || rate <= 0 {
		return ""
	}
	eta := time.Duration(float64(remaining) / rate * float64(time.Second)).Round(time.Second)
	if eta < time.Minute {
		seconds := max(1, int(eta.Seconds()))
		if seconds == 1 {
			return "about 1 second remaining"
		}
		return fmt.Sprintf("about %d seconds remaining", seconds)
	}
	if eta < time.Hour {
		minutes := max(1, int(eta.Round(time.Minute).Minutes()))
		if minutes == 1 {
			return "about 1 minute remaining"
		}
		return fmt.Sprintf("about %d minutes remaining", minutes)
	}
	hours := int(eta / time.Hour)
	minutes := int((eta % time.Hour).Round(time.Minute) / time.Minute)
	if minutes == 60 {
		hours++
		minutes = 0
	}
	if minutes == 0 {
		if hours == 1 {
			return "about 1 hour remaining"
		}
		return fmt.Sprintf("about %d hours remaining", hours)
	}
	if hours == 1 {
		return fmt.Sprintf("about 1 hour %d minutes remaining", minutes)
	}
	return fmt.Sprintf("about %d hours %d minutes remaining", hours, minutes)
}

func (p *recipeCommandProgress) parse(line string) (string, int64, int64, bool) {
	match := recipeByteProgressPattern.FindStringSubmatch(line)
	if len(match) != 5 {
		return "", 0, 0, false
	}
	done, doneOK := recipeByteValue(match[1], match[2])
	total, totalOK := recipeByteValue(match[3], match[4])
	if !doneOK || !totalOK || total <= 0 {
		return "", 0, 0, false
	}
	// Docker may move to another layer and restart its byte counter. Reset the
	// rate sample instead of displaying an increasingly incorrect ETA.
	if done < p.lastDone {
		p.started = time.Now()
		p.firstDone = done
	}
	if p.firstDone == 0 {
		p.firstDone = done
	}
	p.lastDone = done

	action := "Preparing recipe data"
	switch p.phase {
	case "building":
		action = "Downloading the pinned custom inference runtime"
	case "downloading":
		action = "Downloading model weights"
	}
	message := action + " — " + formatDownloadProgress(done, total)
	elapsed := time.Since(p.started).Seconds()
	rate := float64(0)
	if match := recipeElapsedPattern.FindStringSubmatch(line); len(match) == 2 {
		if seconds, err := strconv.ParseFloat(match[1], 64); err == nil && seconds > 0 {
			// BuildKit reports the layer's total elapsed time here. Pair it with
			// the layer's total downloaded bytes; using a delta from the first
			// line would mix two different time origins and overstate the ETA.
			rate = float64(done) / seconds
		}
	}
	if rate == 0 {
		if transferred := done - p.firstDone; elapsed > 1 && transferred > 0 {
			rate = float64(transferred) / elapsed
		}
	}
	if eta := recipeProgressETA(total-done, rate); eta != "" {
		message += " · " + eta
	}
	return message, done, total, true
}
