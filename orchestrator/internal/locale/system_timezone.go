package locale

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// SetSystemTimezone changes the host timezone on CloudlessOS. Development
// builds on non-Linux hosts deliberately remain a no-op.
func SetSystemTimezone(ctx context.Context, zone string) error {
	zone = strings.TrimSpace(zone)
	if zone == "" || runtime.GOOS != "linux" {
		return nil
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", zone, err)
	}
	path, err := exec.LookPath("timedatectl")
	if err != nil {
		return fmt.Errorf("timedatectl is unavailable: %w", err)
	}
	if output, err := exec.CommandContext(ctx, path, "set-timezone", zone).CombinedOutput(); err != nil {
		return fmt.Errorf("set timezone: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
