package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

var recipeDockerVolumePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var recipePeerUsernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

type recipePeer struct {
	Alias    string
	Name     string
	Checkout string
}

func recipePeerCheckout(recipeID, username string) (string, error) {
	username = strings.TrimSpace(username)
	if !localRecipeIDPattern.MatchString(recipeID) || !recipePeerUsernamePattern.MatchString(username) {
		return "", errors.New("recipe peer checkout identity is invalid")
	}
	return "/home/" + username + "/.local/share/cloudless/recipes-runtime/" + recipeID, nil
}

func recipeDistributionPeers(recipe localrecipes.Recipe, cluster sparkcluster.State, env map[string]string) ([]recipePeer, error) {
	if recipe.Distributed.Nodes <= 1 {
		return nil, nil
	}
	aliases := strings.Split(strings.TrimSpace(env["WORKER_HOSTS"]), ",")
	if len(aliases) != len(cluster.Nodes) {
		return nil, fmt.Errorf("recipe expected %d peer aliases, found %d", len(cluster.Nodes), len(aliases))
	}
	peers := make([]recipePeer, 0, len(aliases))
	for index, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return nil, errors.New("recipe peer alias is empty")
		}
		name := strings.TrimSpace(cluster.Nodes[index].Name)
		if name == "" {
			name = fmt.Sprintf("Spark %d", index+2)
		}
		checkout, err := recipePeerCheckout(recipe.ID, cluster.Nodes[index].Username)
		if err != nil {
			return nil, err
		}
		peers = append(peers, recipePeer{Alias: alias, Name: name, Checkout: checkout})
	}
	return peers, nil
}

func recipeSSHCommand(ctx context.Context, dir string, env map[string]string, peer recipePeer, args ...string) *exec.Cmd {
	config := filepath.Join(env["HOME"], ".ssh", "config")
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, recipeShellQuote(arg))
	}
	// OpenSSH concatenates arguments into a command interpreted by the remote
	// login shell. Send one explicitly quoted command so spaces, dollar signs,
	// model IDs, and shell snippets arrive as the exact argv intended for Docker.
	sshArgs := []string{"-F", config, peer.Alias, strings.Join(quoted, " ")}
	cmd := exec.CommandContext(ctx, "/usr/bin/ssh", sshArgs...)
	cmd.Dir, cmd.Env = dir, commandEnv(env)
	return cmd
}

func recipeShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func recipeLocalCommand(ctx context.Context, dir string, env map[string]string, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, commandEnv(env)
	return cmd
}

func recipeCommandOutput(cmd *exec.Cmd) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 500 {
			message = message[len(message)-500:]
		}
		if message != "" {
			return "", fmt.Errorf("%w: %s", err, message)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func recipeImageMetadata(ctx context.Context, dir string, env map[string]string, image string) (string, int64, error) {
	out, err := recipeCommandOutput(recipeLocalCommand(ctx, dir, env, "docker", "image", "inspect", image, "--format", "{{.Id}} {{.Size}}"))
	if err != nil {
		return "", 0, fmt.Errorf("inspect built recipe image: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return "", 0, errors.New("Docker returned incomplete recipe image metadata")
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || size <= 0 {
		return "", 0, errors.New("Docker returned an invalid recipe image size")
	}
	return fields[0], size, nil
}

func recipeRemoteImageID(ctx context.Context, dir string, env map[string]string, peer recipePeer, image string) string {
	out, err := recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer, "docker", "image", "inspect", image, "--format", "{{.Id}}"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func configureRecipeTransferProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 2 * time.Second
}

type recipeProgressReader struct {
	reader      io.Reader
	job         *jobs.Job
	phase       string
	label       string
	base        int64
	total       int64
	transferred int64
	started     time.Time
	last        time.Time
}

func (r *recipeProgressReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.transferred += int64(n)
		now := time.Now()
		if r.last.IsZero() || now.Sub(r.last) >= 500*time.Millisecond {
			r.last = now
			r.report(false)
		}
	}
	if errors.Is(err, io.EOF) {
		r.report(true)
	}
	return n, err
}

func (r *recipeProgressReader) report(finished bool) {
	done := r.base + r.transferred
	if finished || done > r.total {
		done = r.total
	}
	message := r.label + " — " + formatDownloadProgress(done, r.total)
	if !finished && r.transferred > 0 {
		elapsed := time.Since(r.started).Seconds()
		if elapsed > 1 {
			rate := float64(r.transferred) / elapsed
			remaining := float64(r.total-done) / rate
			if remaining > 0 {
				eta := time.Duration(remaining * float64(time.Second)).Round(time.Second)
				if eta < time.Minute {
					message += fmt.Sprintf(" · about %d seconds remaining", max(1, int(eta.Seconds())))
				} else {
					message += fmt.Sprintf(" · about %d minutes remaining", max(1, int(eta.Round(time.Minute).Minutes())))
				}
			}
		}
	}
	r.job.ProgressBytes(r.phase, message, done, r.total)
}

func recipeTransferError(label string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if len(stderr) > 800 {
		stderr = stderr[len(stderr)-800:]
	}
	if stderr != "" {
		return fmt.Errorf("%s: %w: %s", label, err, stderr)
	}
	return fmt.Errorf("%s: %w", label, err)
}

func runRecipeTransfer(ctx context.Context, job *jobs.Job, phase, label string, base, total int64, producer, consumer *exec.Cmd) error {
	stream, err := producer.StdoutPipe()
	if err != nil {
		return err
	}
	var producerErr, consumerOut, consumerErr bytes.Buffer
	producer.Stderr = &producerErr
	consumer.Stdout, consumer.Stderr = &consumerOut, &consumerErr
	consumer.Stdin = &recipeProgressReader{reader: stream, job: job, phase: phase, label: label, base: base, total: total, started: time.Now()}
	configureRecipeTransferProcess(producer)
	configureRecipeTransferProcess(consumer)
	if err := consumer.Start(); err != nil {
		return recipeTransferError(label, err, consumerErr.String())
	}
	if err := producer.Start(); err != nil {
		_ = consumer.Cancel()
		_ = consumer.Wait()
		return recipeTransferError(label, err, producerErr.String())
	}
	producerWait := producer.Wait()
	consumerWait := consumer.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if producerWait != nil {
		return recipeTransferError(label, producerWait, producerErr.String())
	}
	if consumerWait != nil {
		return recipeTransferError(label, consumerWait, consumerErr.String())
	}
	job.ProgressBytes(phase, label+" — complete", total, total)
	return nil
}

func syncRecipeCheckout(ctx context.Context, job *jobs.Job, checkout string, env map[string]string, peers []recipePeer) error {
	for index, peer := range peers {
		label := fmt.Sprintf("Copying the small recipe setup to %s (%d/%d)", peer.Name, index+1, len(peers))
		job.Progress("syncing-source", label+"…", -1, -1)
		if err := runRecipeCommand(ctx, job, "syncing-source", label, checkout, env,
			"/usr/bin/ssh", "-F", filepath.Join(env["HOME"], ".ssh", "config"), peer.Alias, "mkdir", "-p", peer.Checkout); err != nil {
			return err
		}
		if err := runRecipeCommand(ctx, job, "syncing-source", label, checkout, env,
			"rsync", "-az", "--delete", "--exclude", ".cloudless-home/", checkout+"/", peer.Alias+":"+peer.Checkout+"/"); err != nil {
			return err
		}
	}
	return nil
}

func distributeRecipeImage(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe, dir string, env map[string]string, peers []recipePeer) error {
	localID, imageSize, err := recipeImageMetadata(ctx, dir, env, recipe.Engine.Image)
	if err != nil {
		return err
	}
	grandTotal := imageSize * int64(len(peers))
	for index, peer := range peers {
		base := imageSize * int64(index)
		if recipeRemoteImageID(ctx, dir, env, peer, recipe.Engine.Image) == localID {
			job.ProgressBytes("syncing-image", peer.Name+" already has the exact runtime image.", base+imageSize, grandTotal)
			continue
		}
		// A mutable tag may point at an older local build. Remove only that exact
		// tag before loading the coordinator's immutable image archive.
		_, _ = recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer, "docker", "image", "rm", "-f", recipe.Engine.Image))
		label := fmt.Sprintf("Copying the inference runtime from %s to %s over the direct Spark fabric (%d/%d)", localRecipeNodeName(), peer.Name, index+1, len(peers))
		producer := recipeLocalCommand(ctx, dir, env, "docker", "image", "save", recipe.Engine.Image)
		consumer := recipeSSHCommand(ctx, dir, env, peer, "docker", "image", "load")
		if err := runRecipeTransfer(ctx, job, "syncing-image", label, base, grandTotal, producer, consumer); err != nil {
			return err
		}
		if remoteID := recipeRemoteImageID(ctx, dir, env, peer, recipe.Engine.Image); remoteID != localID {
			return fmt.Errorf("%s loaded a different runtime image digest", peer.Name)
		}
	}
	return nil
}

func recipeCacheVolume(recipe localrecipes.Recipe) (string, error) {
	volume := strings.TrimSpace(recipe.Runtime.Environment["HF_CACHE"])
	if volume == "" {
		volume = "cloudless-hf"
	}
	if !recipeDockerVolumePattern.MatchString(volume) {
		return "", errors.New("download-once recipes require a named Hugging Face Docker volume")
	}
	return volume, nil
}

func recipeCacheBytes(ctx context.Context, dir string, env map[string]string, peer *recipePeer, image, volume, relative string) (int64, error) {
	args := []string{"docker", "run", "--rm", "-v", volume + ":/cache:ro", "--entrypoint", "/bin/sh", image,
		"-c", `exec du -sb "$1"`, "cloudless-cache-size", "/cache/" + relative}
	var cmd *exec.Cmd
	if peer == nil {
		cmd = recipeLocalCommand(ctx, dir, env, args[0], args[1:]...)
	} else {
		cmd = recipeSSHCommand(ctx, dir, env, *peer, args...)
	}
	out, err := recipeCommandOutput(cmd)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, errors.New("model cache size is unavailable")
	}
	return strconv.ParseInt(fields[0], 10, 64)
}

// recipeSnapshotSignature identifies one immutable Hugging Face revision from
// the paths and sizes of the files visible through its snapshot symlinks. It
// deliberately ignores refs, locks, metadata, and other cached revisions: all
// of those can change after a successful distribution without changing the
// model that a recipe requested.
func recipeSnapshotSignature(ctx context.Context, dir string, env map[string]string, peer *recipePeer, image, volume, relative, revision string) (string, error) {
	args := []string{"docker", "run", "--rm", "-v", volume + ":/cache:ro", "--entrypoint", "/bin/sh", image,
		"-c", `set -eu
root="$1"
test -d "$root"
manifest="$(mktemp)"
trap 'rm -f "$manifest"' EXIT
find -L "$root" -type f -printf '%P\t%s\n' | LC_ALL=C sort > "$manifest"
test -s "$manifest"
sha256sum "$manifest" | awk '{print $1}'`, "cloudless-snapshot-signature", "/cache/" + relative + "/snapshots/" + revision}
	var cmd *exec.Cmd
	if peer == nil {
		cmd = recipeLocalCommand(ctx, dir, env, args[0], args[1:]...)
	} else {
		cmd = recipeSSHCommand(ctx, dir, env, *peer, args...)
	}
	out, err := recipeCommandOutput(cmd)
	if err != nil {
		return "", err
	}
	signature := strings.TrimSpace(out)
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(signature) {
		return "", errors.New("model snapshot signature is unavailable")
	}
	return signature, nil
}

func distributeRecipeModel(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe, dir string, env map[string]string, peers []recipePeer) error {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok {
		return errors.New("recipe model ID cannot be mapped to a Hugging Face cache")
	}
	volume, err := recipeCacheVolume(recipe)
	if err != nil {
		return err
	}
	relative := filepath.ToSlash(filepath.Join("hub", cacheName))
	// Use the same runtime image on both sides so the cache inspection and tar
	// tools are identical. This also avoids assuming the remote Docker volume's
	// host mount path is readable by the enrolled user.
	localSize, err := recipeCacheBytes(ctx, dir, env, nil, recipe.Engine.Image, volume, relative)
	if err != nil || localSize <= 0 {
		return fmt.Errorf("inspect prepared model cache: %w", err)
	}
	localSignature, err := recipeSnapshotSignature(ctx, dir, env, nil, recipe.Engine.Image, volume, relative, recipe.Model.Revision)
	if err != nil {
		return fmt.Errorf("inspect prepared model snapshot: %w", err)
	}
	grandTotal := localSize * int64(len(peers))
	for index, peer := range peers {
		base := localSize * int64(index)
		remoteSignature, signatureErr := recipeSnapshotSignature(ctx, dir, env, &peer, recipe.Engine.Image, volume, relative, recipe.Model.Revision)
		if signatureErr == nil && remoteSignature == localSignature {
			job.ProgressBytes("syncing-model", peer.Name+" already has the exact model snapshot.", base+localSize, grandTotal)
			continue
		}
		_, _ = recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer,
			"docker", "run", "--rm", "-v", volume+":/cache", "--entrypoint", "/bin/sh", recipe.Engine.Image,
			"-c", `exec rm -rf "$1"`, "cloudless-cache-remove", "/cache/"+relative))
		label := fmt.Sprintf("Copying model files from %s to %s over the direct Spark fabric (%d/%d)", localRecipeNodeName(), peer.Name, index+1, len(peers))
		producer := recipeLocalCommand(ctx, dir, env, "docker", "run", "--rm", "-v", volume+":/cache:ro",
			"--entrypoint", "/bin/sh", recipe.Engine.Image, "-c", `exec tar -C /cache -cf - "$1"`, "cloudless-cache-send", relative)
		consumer := recipeSSHCommand(ctx, dir, env, peer, "docker", "run", "--rm", "-i", "-v", volume+":/cache",
			"--entrypoint", "/bin/sh", recipe.Engine.Image, "-c", `exec tar -C /cache -xf -`)
		if err := runRecipeTransfer(ctx, job, "syncing-model", label, base, grandTotal, producer, consumer); err != nil {
			return err
		}
		remoteSignature, err = recipeSnapshotSignature(ctx, dir, env, &peer, recipe.Engine.Image, volume, relative, recipe.Model.Revision)
		if err != nil || remoteSignature != localSignature {
			return fmt.Errorf("%s did not receive the complete model snapshot", peer.Name)
		}
	}
	return nil
}
