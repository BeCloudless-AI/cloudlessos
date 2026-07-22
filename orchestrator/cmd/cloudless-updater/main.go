package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/osupdate"
)

var managedPackages = []string{
	"cloudless-orchestrator",
	"cloudless-shell",
	"cloudless-branding",
	"cloudless-hardware",
	"cloudless-firstboot",
	"cloudless-updater",
}

func main() {
	if len(os.Args) != 2 {
		fatalf("usage: cloudless-updater check|apply|status")
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = check()
	case "apply":
		err = apply()
	case "status":
		var status osupdate.Status
		status, err = osupdate.Read()
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(status)
		}
	default:
		fatalf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cloudless-updater: "+format+"\n", args...)
	os.Exit(1)
}

func withLock(fn func() error) error {
	if err := os.MkdirAll("/run/cloudless-updater", 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile("/run/cloudless-updater/lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another update operation is already running")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func check() error {
	return withLock(func() error {
		status := currentStatus("checking", "Checking updates.becloudless.ai…")
		if !status.Configured {
			status.State = "not_configured"
			status.Message = "Cloudless update signing has not been configured yet."
			return osupdate.Write(status)
		}
		if err := writeProgress(&status, 5, "Preparing the update check…"); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := writeProgress(&status, 15, "Refreshing the signed Cloudless repository…"); err != nil {
			return err
		}
		if _, err := run(ctx, "apt-get", "update"); err != nil {
			return failStatus(status, "Could not refresh the Cloudless repository", err)
		}
		if err := writeProgress(&status, 75, "Comparing installed packages…"); err != nil {
			return err
		}
		status, err := candidates(ctx, status)
		if err != nil {
			return failStatus(status, "Could not inspect package updates", err)
		}
		status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		status.Message = "CloudlessOS is up to date."
		if len(status.Packages) > 0 {
			status.State = "available"
			status.Message = fmt.Sprintf("CloudlessOS %s is ready.", status.AvailableVersion)
		} else {
			status.State = "idle"
		}
		status.Progress = 100
		return osupdate.Write(status)
	})
}

func apply() error {
	return withLock(func() error {
		status := currentStatus("installing", "Preparing the CloudlessOS update…")
		if !status.Configured {
			status.State = "not_configured"
			status.Message = "Cloudless update signing has not been configured yet."
			return osupdate.Write(status)
		}
		if err := writeProgress(&status, 3, "Preparing the CloudlessOS update…"); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := writeProgress(&status, 8, "Refreshing the signed Cloudless repository…"); err != nil {
			return err
		}
		if _, err := run(ctx, "apt-get", "update"); err != nil {
			return failStatus(status, "Could not refresh the Cloudless repository", err)
		}
		if err := writeProgress(&status, 18, "Resolving the verified package update…"); err != nil {
			return err
		}
		var err error
		status, err = candidates(ctx, status)
		if err != nil {
			return failStatus(status, "Could not inspect package updates", err)
		}
		if len(status.Packages) == 0 {
			status.State = "idle"
			status.Message = "CloudlessOS is already up to date."
			status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
			status.Progress = 100
			return osupdate.Write(status)
		}

		if err := writeProgress(&status, 24, "Creating a recovery snapshot…"); err != nil {
			return err
		}
		rollbackDir := filepath.Join("/var/lib/cloudless-updater/rollback", time.Now().UTC().Format("20060102T150405Z"))
		rollbackAvailable := copyDebs("/var/lib/cloudless-updater/current", rollbackDir) == nil

		status.State = "installing"
		if err := writeProgress(&status, 30, fmt.Sprintf("Downloading %d verified package update(s)…", len(status.Packages))); err != nil {
			return err
		}
		args := []string{"install", "-y", "--only-upgrade"}
		for _, pkg := range status.Packages {
			args = append(args, pkg.Name)
		}
		downloadArgs := []string{"install", "-y", "--download-only", "--only-upgrade"}
		for _, pkg := range status.Packages {
			downloadArgs = append(downloadArgs, pkg.Name)
		}
		cachedDebs, _ := filepath.Glob("/var/cache/apt/archives/cloudless-*.deb")
		for _, deb := range cachedDebs {
			_ = os.Remove(deb)
		}
		if _, err := runEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive"}, "apt-get", downloadArgs...); err != nil {
			return failStatus(status, "Could not download the verified update", err)
		}
		if err := writeProgress(&status, 55, "Verifying downloaded packages…"); err != nil {
			return err
		}
		stagedDir := "/var/lib/cloudless-updater/staged"
		_ = os.RemoveAll(stagedDir)
		if err := copyDebs("/var/cache/apt/archives", stagedDir); err != nil {
			return failStatus(status, "Downloaded update packages were not found", err)
		}
		if err := writeProgress(&status, 62, fmt.Sprintf("Installing %d verified package update(s)…", len(status.Packages))); err != nil {
			return err
		}
		if _, err := runEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a"}, "apt-get", args...); err != nil {
			return failStatus(status, "Package installation failed", err)
		}

		if err := writeProgress(&status, 86, "Restarting Cloudless services and checking their health…"); err != nil {
			return err
		}
		_, _ = run(ctx, "systemctl", "try-restart", "cloudlessd.service")
		if !waitHealthy(90 * time.Second) {
			if !rollbackAvailable {
				return failStatus(status, "The update failed its health check and this legacy installation had no rollback generation", errors.New("cloudlessd did not become healthy"))
			}
			if rollbackErr := rollback(ctx, rollbackDir); rollbackErr != nil {
				return failStatus(status, "The update failed its health check and rollback also failed", rollbackErr)
			}
			return failStatus(status, "The update failed its health check and was rolled back", errors.New("cloudlessd did not become healthy"))
		}

		for _, pkg := range status.Packages {
			if pkg.Name == "cloudless-shell" {
				_, _ = run(ctx, "systemctl", "try-restart", "lightdm.service")
			}
			if pkg.Name == "cloudless-branding" || pkg.Name == "cloudless-hardware" {
				status.RebootRequired = true
			}
		}
		if _, err := os.Stat("/run/reboot-required"); err == nil {
			status.RebootRequired = true
		}
		if err := writeProgress(&status, 96, "Saving recovery packages…"); err != nil {
			return err
		}
		if err := copyDebs(stagedDir, "/var/lib/cloudless-updater/current"); err != nil {
			return failStatus(status, "Update installed but its recovery packages could not be retained", err)
		}
		status.State = "updated"
		status.CurrentVersion = status.AvailableVersion
		status.AvailableVersion = ""
		status.Packages = nil
		status.Error = ""
		status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		status.CheckedAt = status.UpdatedAt
		status.Message = "Update installed successfully."
		if status.RebootRequired {
			status.State = "reboot_required"
			status.Message = "Update installed. Restart CloudlessOS to finish."
		}
		status.Progress = 100
		return osupdate.Write(status)
	})
}

func currentStatus(state, message string) osupdate.Status {
	status, err := osupdate.Read()
	if err != nil {
		status = osupdate.DefaultStatus()
	}
	status.State = state
	status.Message = message
	status.Error = ""
	status.Progress = 0
	status.Configured = osupdate.Configured()
	return status
}

func writeProgress(status *osupdate.Status, progress int, message string) error {
	if progress < 0 {
		progress = 0
	} else if progress > 100 {
		progress = 100
	}
	status.Progress = progress
	status.Message = message
	return osupdate.Write(*status)
}

func candidates(ctx context.Context, status osupdate.Status) (osupdate.Status, error) {
	status.Packages = nil
	status.AvailableVersion = ""
	for _, name := range managedPackages {
		installed, err := installedVersion(ctx, name)
		if err != nil {
			continue
		}
		candidate, err := candidateVersion(ctx, name)
		if err != nil {
			return status, err
		}
		if name == "cloudless-orchestrator" {
			status.CurrentVersion = installed
		}
		if candidate == "" || candidate == installed {
			continue
		}
		newer := exec.CommandContext(ctx, "dpkg", "--compare-versions", candidate, "gt", installed).Run() == nil
		if !newer {
			continue
		}
		status.Packages = append(status.Packages, osupdate.Package{Name: name, Installed: installed, Candidate: candidate})
		if name == "cloudless-orchestrator" {
			status.AvailableVersion = candidate
		}
	}
	if status.AvailableVersion == "" && len(status.Packages) > 0 {
		status.AvailableVersion = status.Packages[0].Candidate
	}
	return status, nil
}

func installedVersion(ctx context.Context, name string) (string, error) {
	out, err := run(ctx, "dpkg-query", "-W", "-f=${Version}", name)
	return strings.TrimSpace(out), err
}

func candidateVersion(ctx context.Context, name string) (string, error) {
	out, err := run(ctx, "apt-cache", "policy", name)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Candidate:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Candidate:"))
			if value == "(none)" {
				return "", nil
			}
			return value, nil
		}
	}
	return "", nil
}

func rollback(ctx context.Context, dir string) error {
	debs, _ := filepath.Glob(filepath.Join(dir, "*.deb"))
	if len(debs) == 0 {
		return errors.New("no previous packages were available for rollback")
	}
	args := append([]string{"-i"}, debs...)
	if _, err := run(ctx, "dpkg", args...); err != nil {
		return err
	}
	_, _ = run(ctx, "systemctl", "try-restart", "cloudlessd.service")
	return nil
}

func waitHealthy(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://127.0.0.1:8765/api/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

func failStatus(status osupdate.Status, message string, err error) error {
	status.State = "failed"
	status.Message = message
	status.Error = err.Error()
	_ = osupdate.Write(status)
	return fmt.Errorf("%s: %w", message, err)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	return runEnv(ctx, nil, name, args...)
}

func runEnv(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func copyDebs(from, to string) error {
	debs, err := filepath.Glob(filepath.Join(from, "cloudless-*.deb"))
	if err != nil {
		return err
	}
	if len(debs) == 0 {
		return fmt.Errorf("no Cloudless packages in %s", from)
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	for _, src := range debs {
		base := filepath.Base(src)
		if split := strings.IndexByte(base, '_'); split > 0 {
			old, _ := filepath.Glob(filepath.Join(to, base[:split+1]+"*.deb"))
			for _, path := range old {
				_ = os.Remove(path)
			}
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(to, base)
		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
