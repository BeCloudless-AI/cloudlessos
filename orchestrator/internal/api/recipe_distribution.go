package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

var recipePeerUsernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// Runtime images can be several gigabytes. Keep transfer archives on the
// persistent Cloudless data filesystem instead of /run, which is commonly a
// small tmpfs and can fill even when the machine has ample disk space.
var recipeImageTransferRoot = "/var/lib/cloudless/image-transfers"
var recipeImageExportProgressInterval = time.Second

type recipePeer struct {
	Alias    string
	Name     string
	Checkout string
}

// recipeSSHExecutable is fixed for production. Keeping the executable path
// injectable inside the package lets the failure matrix deterministically
// model unreachable and recovered peers without changing system SSH state.
var recipeSSHExecutable = "/usr/bin/ssh"

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
	selectedCluster, err := selectRecipeCluster(recipe, cluster)
	if err != nil {
		return nil, err
	}
	cluster = selectedCluster
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
	args = decorateRecipeDockerArgs(args, env, true)
	if operationID := strings.TrimSpace(env["CLOUDLESS_RECIPE_OPERATION_ID"]); operationID != "" {
		owned := []string{"/usr/bin/env", "CLOUDLESS_RECIPE_OPERATION_ID=" + operationID}
		if revision := strings.TrimSpace(env["CLOUDLESS_RECIPE_REVISION"]); revision != "" {
			owned = append(owned, "CLOUDLESS_RECIPE_REVISION="+revision)
		}
		args = append(owned, args...)
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, recipeShellQuote(arg))
	}
	// OpenSSH concatenates arguments into a command interpreted by the remote
	// login shell. Send one explicitly quoted command so spaces, dollar signs,
	// model IDs, and shell snippets arrive as the exact argv intended for Docker.
	sshArgs := []string{"-F", config, peer.Alias, strings.Join(quoted, " ")}
	cmd := exec.CommandContext(ctx, recipeSSHExecutable, sshArgs...)
	cmd.Dir, cmd.Env = dir, commandEnv(env)
	return cmd
}

func recipeShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func recipeLocalCommand(ctx context.Context, dir string, env map[string]string, name string, args ...string) *exec.Cmd {
	if filepath.Base(name) == "docker" {
		if strings.TrimSpace(env["CLOUDLESS_RECIPE_OPERATION_ID"]) == "" {
			return exec.CommandContext(ctx, "/usr/bin/false")
		}
		decorated := append([]string{"docker"}, args...)
		decorated = decorateRecipeDockerArgs(decorated, env, true)
		args = decorated[1:]
		name = recipeDockerCompatibilityExecutable
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, commandEnv(env)
	return cmd
}

func decorateRecipeDockerArgs(args []string, env map[string]string, commandIncludesProgram bool) []string {
	operationID := strings.TrimSpace(env["CLOUDLESS_RECIPE_OPERATION_ID"])
	if operationID == "" || len(args) < 2 || !commandIncludesProgram || filepath.Base(args[0]) != "docker" || (args[1] != "run" && args[1] != "create") {
		return args
	}
	for index := 2; index+1 < len(args); index++ {
		if args[index] == "--label" && strings.HasPrefix(args[index+1], "cloudless.recipe.operation=") {
			return args
		}
	}
	labels := []string{"--label", "cloudless.recipe.operation=" + operationID}
	if revision := strings.TrimSpace(env["CLOUDLESS_RECIPE_REVISION"]); revision != "" {
		labels = append(labels, "--label", "cloudless.recipe.revision="+revision)
	}
	decorated := make([]string, 0, len(args)+len(labels))
	decorated = append(decorated, args[:2]...)
	decorated = append(decorated, labels...)
	decorated = append(decorated, args[2:]...)
	return decorated
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

func recipeRemoteImageID(ctx context.Context, dir string, env map[string]string, peer recipePeer, image string) string {
	out, err := recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer, "docker", "image", "inspect", image, "--format", "{{.Id}}"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// reconcileLoadedRecipeImage verifies the immutable image content independently
// from its repository reference, then recreates that reference explicitly. Docker
// save/load guarantees the image content but does not guarantee that a RepoDigest
// or tag used to address the source daemon survives on a fresh destination daemon.
// Treating the reference as proof of content therefore rejects valid transfers and
// can also leave a stale destination tag pointing at older content.
func reconcileLoadedRecipeImage(expectedID, reference string, inspect func(string) string, tag func(string, string) error) error {
	expectedID = strings.TrimSpace(expectedID)
	reference = strings.TrimSpace(reference)
	if expectedID == "" || inspect(expectedID) != expectedID {
		return errors.New("worker did not load the expected runtime image content")
	}
	if err := tag(expectedID, reference); err != nil {
		return fmt.Errorf("restore verified runtime image reference: %w", err)
	}
	if inspect(reference) != expectedID {
		return errors.New("worker runtime image reference does not resolve to the verified content")
	}
	return nil
}

func reconcileRemoteRecipeImage(ctx context.Context, dir string, env map[string]string, peer recipePeer, expectedID, reference string) error {
	inspect := func(image string) string {
		return recipeRemoteImageID(ctx, dir, env, peer, image)
	}
	tag := func(source, target string) error {
		_, err := recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer, "docker", "image", "tag", source, target))
		return err
	}
	if err := reconcileLoadedRecipeImage(expectedID, reference, inspect, tag); err != nil {
		return fmt.Errorf("%s: %w", peer.Name, err)
	}
	return nil
}

func recipeImageTransferReference(localID string) (string, error) {
	digest := strings.TrimPrefix(strings.TrimSpace(localID), "sha256:")
	if len(digest) != 64 {
		return "", errors.New("local runtime image has no immutable sha256 content ID")
	}
	return "cloudless/recipe-transfer:" + digest[:16], nil
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
	type transferWait struct {
		producer bool
		err      error
	}
	waits := make(chan transferWait, 2)
	go func() { waits <- transferWait{producer: true, err: producer.Wait()} }()
	go func() { waits <- transferWait{producer: false, err: consumer.Wait()} }()
	first := <-waits
	if first.err != nil {
		if first.producer {
			_ = consumer.Cancel()
		} else {
			_ = producer.Cancel()
		}
	}
	second := <-waits
	var producerWait, consumerWait error
	for _, result := range []transferWait{first, second} {
		if result.producer {
			producerWait = result.err
		} else {
			consumerWait = result.err
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if first.err != nil {
		if first.producer {
			return recipeTransferError(label, first.err, producerErr.String())
		}
		return recipeTransferError(label, first.err, consumerErr.String())
	}
	if producerWait != nil {
		return recipeTransferError(label, producerWait, producerErr.String())
	}
	if consumerWait != nil {
		return recipeTransferError(label, consumerWait, consumerErr.String())
	}
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
		args := []string{"-az", "--delete", "--exclude", ".cloudless-home/"}
		args = append(args, recipeRsyncOwnershipArgs(env)...)
		args = append(args, checkout+"/", peer.Alias+":"+peer.Checkout+"/")
		if err := runRecipeCommand(ctx, job, "syncing-source", label, checkout, env, "rsync", args...); err != nil {
			return err
		}
	}
	return nil
}

func recipeRsyncOwnershipArgs(env map[string]string) []string {
	operationID := strings.TrimSpace(env["CLOUDLESS_RECIPE_OPERATION_ID"])
	if operationID == "" {
		return nil
	}
	remote := "env CLOUDLESS_RECIPE_OPERATION_ID=" + recipeShellQuote(operationID)
	if revision := strings.TrimSpace(env["CLOUDLESS_RECIPE_REVISION"]); revision != "" {
		remote += " CLOUDLESS_RECIPE_REVISION=" + recipeShellQuote(revision)
	}
	remote += " /usr/bin/rsync"
	return []string{"--rsync-path", remote}
}

func distributeRecipeImage(ctx context.Context, runtime engine.Engine, job *jobs.Job, recipe localrecipes.Recipe, dir string, env map[string]string, peers []recipePeer) error {
	image, err := runtime.InspectImage(ctx, recipe.Engine.Image)
	if err != nil {
		return err
	}
	localID, imageSize := strings.TrimSpace(image.ID), image.Size
	if localID == "" || imageSize <= 0 {
		return errors.New("local runtime image metadata is unavailable")
	}
	transferReference, err := recipeImageTransferReference(localID)
	if err != nil {
		return err
	}
	// Always export a Cloudless-owned tag. Registry digest references are valid
	// pull/inspect addresses but Docker refuses to create them as tags, and
	// save/load does not promise to preserve their RepoDigest metadata.
	if err := runtime.TagImage(ctx, localID, transferReference); err != nil {
		return fmt.Errorf("prepare runtime image transfer reference: %w", err)
	}
	if err := os.MkdirAll(recipeImageTransferRoot, 0o770); err != nil {
		return fmt.Errorf("prepare image transfer staging: %w", err)
	}
	placeholder, err := os.CreateTemp(recipeImageTransferRoot, "cloudless-image-*.tar")
	if err != nil {
		return fmt.Errorf("reserve image transfer staging: %w", err)
	}
	archive := placeholder.Name()
	if closeErr := placeholder.Close(); closeErr != nil {
		return closeErr
	}
	if err := os.Remove(archive); err != nil {
		return err
	}
	defer os.Remove(archive)
	exported := false
	grandTotal := imageSize * int64(len(peers))
	for index, peer := range peers {
		base := imageSize * int64(index)
		if recipeRemoteImageID(ctx, dir, env, peer, localID) == localID {
			if err := reconcileRemoteRecipeImage(ctx, dir, env, peer, localID, transferReference); err != nil {
				return err
			}
			job.ProgressBytes("syncing-image", peer.Name+" already has the exact runtime image.", base+imageSize, grandTotal)
			continue
		}
		// A mutable tag may point at an older local build. Remove only that exact
		// tag before loading the coordinator's immutable image archive.
		_, _ = recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer, "docker", "image", "rm", "-f", transferReference))
		label := fmt.Sprintf("Copying the inference runtime from %s to %s over the direct Spark fabric (%d/%d)", localRecipeNodeName(), peer.Name, index+1, len(peers))
		if !exported {
			if err := exportRecipeImageWithProgress(ctx, runtime, job, transferReference, archive, imageSize); err != nil {
				return err
			}
			exported = true
		}
		producer := exec.CommandContext(ctx, "/usr/bin/cat", archive)
		consumer := recipeSSHCommand(ctx, dir, env, peer, "docker", "image", "load")
		if err := runRecipeTransfer(ctx, job, "syncing-image", label, base, grandTotal, producer, consumer); err != nil {
			return err
		}
		if err := reconcileRemoteRecipeImage(ctx, dir, env, peer, localID, transferReference); err != nil {
			return err
		}
	}
	return nil
}

func exportRecipeImageWithProgress(ctx context.Context, runtime engine.Engine, job *jobs.Job, image, archive string, expectedBytes int64) error {
	message := "Preparing the verified inference runtime for transfer..."
	job.ProgressBytes("exporting-image", message, 0, expectedBytes)
	result := make(chan error, 1)
	go func() { result <- runtime.ExportImage(ctx, image, archive) }()
	ticker := time.NewTicker(recipeImageExportProgressInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			if err == nil {
				job.ProgressBytes("exporting-image", "Verified runtime archive is ready for transfer.", expectedBytes, expectedBytes)
			}
			return err
		case <-ticker.C:
			if info, err := os.Stat(archive + ".partial"); err == nil {
				done := info.Size()
				if expectedBytes > 0 && done > expectedBytes {
					done = expectedBytes
				}
				message := "Packaging the verified runtime for transfer..."
				if expectedBytes > 0 {
					message = fmt.Sprintf("Packaging the verified runtime — %d%%", done*100/expectedBytes)
				}
				job.ProgressBytes("exporting-image", message, done, expectedBytes)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func recipeCacheVolume(recipe localrecipes.Recipe) (string, error) {
	cache := strings.TrimSpace(recipe.Runtime.Environment["HF_CACHE"])
	if cache == "" || cache == "cloudless-hf" {
		cache = modelcache.Root()
	}
	cache = filepath.Clean(cache)
	root := filepath.Clean(modelcache.Root())
	if !filepath.IsAbs(cache) || !pathWithin(cache, root) {
		return "", errors.New("download-once recipes require Cloudless-owned host model storage")
	}
	return cache, nil
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
// the path, size and SHA-256 digest of every file visible through its snapshot
// symlinks. A path-and-size-only signature accepted same-size corruption.
func recipeSnapshotSignature(ctx context.Context, dir string, env map[string]string, peer *recipePeer, image, volume, relative, revision string) (string, error) {
	return recipeSnapshotSignatureWithProgress(ctx, dir, env, peer, image, volume, relative, revision, nil)
}

func recipeSnapshotSignatureWithProgress(ctx context.Context, dir string, env map[string]string, peer *recipePeer, image, volume, relative, revision string, progress func(int64)) (string, error) {
	const verifier = `import hashlib, os, pathlib, sys
root = pathlib.Path(sys.argv[1]).resolve()
repository = pathlib.Path(sys.argv[2]).resolve()
if not root.is_dir() or repository not in (root, *root.parents):
    raise SystemExit("unsafe or missing snapshot")
files = sorted((path for path in root.rglob("*") if path.is_file()), key=lambda path: path.relative_to(root).as_posix())
if not files:
    raise SystemExit("empty snapshot")
manifest = hashlib.sha256()
done = 0
reported = 0
for path in files:
    resolved = path.resolve(strict=True)
    if repository not in (resolved, *resolved.parents) or not resolved.is_file():
        raise SystemExit("snapshot link escapes repository")
    digest = hashlib.sha256()
    with resolved.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
            done += len(chunk)
            if done - reported >= 256 * 1024 * 1024:
                print("PROGRESS " + str(done), flush=True)
                reported = done
    relative = path.relative_to(root).as_posix()
    manifest.update(relative.encode() + b"\0" + str(resolved.stat().st_size).encode() + b"\0" + digest.hexdigest().encode() + b"\n")
print("PROGRESS " + str(done), flush=True)
print("DIGEST " + manifest.hexdigest(), flush=True)`
	repository := "/cache/" + relative
	args := []string{"docker", "run", "--rm", "-v", volume + ":/cache:ro", "--entrypoint", "python3", image,
		"-c", verifier, repository + "/snapshots/" + revision, repository}
	var cmd *exec.Cmd
	if peer == nil {
		cmd = recipeLocalCommand(ctx, dir, env, args[0], args[1:]...)
	} else {
		cmd = recipeSSHCommand(ctx, dir, env, *peer, args...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var signature string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if value, ok := strings.CutPrefix(line, "PROGRESS "); ok {
			if done, parseErr := strconv.ParseInt(strings.TrimSpace(value), 10, 64); parseErr == nil && done >= 0 && progress != nil {
				progress(done)
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "DIGEST "); ok {
			signature = strings.TrimSpace(value)
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return "", scanErr
	}
	if waitErr != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", fmt.Errorf("%w: %s", waitErr, detail)
		}
		return "", waitErr
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(signature) {
		return "", errors.New("model snapshot signature is unavailable")
	}
	return signature, nil
}

func verifyRecipePeerModelSnapshots(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe, dir string, env map[string]string, peers []recipePeer, verified recipeArtifactManifest) (map[string]string, error) {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok {
		return nil, errors.New("recipe model ID cannot be mapped to a Hugging Face cache")
	}
	volume, err := recipeCacheVolume(recipe)
	if err != nil {
		return nil, err
	}
	if verified.ModelID != recipe.Model.ID || verified.Revision != recipe.Model.Revision || verified.Bytes <= 0 || !regexpSHA256.MatchString(verified.Digest) {
		return nil, errors.New("verified local model evidence is invalid")
	}
	relative := filepath.ToSlash(filepath.Join("hub", cacheName))
	total := verified.Bytes * int64(len(peers))
	digests := make(map[string]string, len(peers))
	for index, peer := range peers {
		base := verified.Bytes * int64(index)
		started, lastUpdate := time.Now(), time.Time{}
		job.ProgressBytes("verifying-peer-model", "Checking existing weights on "+peer.Name+" without downloading.", base, total)
		digest, digestErr := recipeSnapshotSignatureWithProgress(ctx, dir, env, &peer, recipe.Engine.Image, volume, relative, recipe.Model.Revision, func(done int64) {
			if time.Since(lastUpdate) < time.Second && done < verified.Bytes {
				return
			}
			message := "Verifying existing weights on " + peer.Name + " — " + formatDownloadProgress(done, verified.Bytes)
			if elapsed := time.Since(started).Seconds(); verified.Bytes > done && elapsed > 1 && done > 0 {
				if eta := recipeProgressETA(verified.Bytes-done, float64(done)/elapsed); eta != "" {
					message += " · " + eta
				}
			}
			job.ProgressBytes("verifying-peer-model", message, base+done, total)
			lastUpdate = time.Now()
		})
		if digestErr != nil || digest != verified.Digest {
			return nil, fmt.Errorf("prepared model integrity on %s does not match the verified coordinator snapshot", peer.Name)
		}
		digests[peer.Name] = digest
		job.ProgressBytes("model-ready", peer.Name+" has the exact verified model snapshot.", base+verified.Bytes, total)
	}
	return digests, nil
}

func distributeRecipeModel(ctx context.Context, runtime engine.Engine, job *jobs.Job, recipe localrecipes.Recipe, dir string, env map[string]string, peers []recipePeer, verified recipeArtifactManifest, shared bool) (map[string]string, error) {
	if shared {
		job.Progress("verifying-peer-model", "Shared NFS storage is active; verifying the model on every Spark without copying weights.", -1, -1)
		return verifyRecipePeerModelSnapshots(ctx, job, recipe, dir, env, peers, verified)
	}
	peerDigests := make(map[string]string, len(peers))
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok {
		return nil, errors.New("recipe model ID cannot be mapped to a Hugging Face cache")
	}
	volume, err := recipeCacheVolume(recipe)
	if err != nil {
		return nil, err
	}
	if verified.ModelID != recipe.Model.ID || verified.Revision != recipe.Model.Revision || verified.Bytes <= 0 || !regexpSHA256.MatchString(verified.Digest) {
		return nil, errors.New("verified local model evidence is invalid")
	}
	relative := filepath.ToSlash(filepath.Join("hub", cacheName))
	localSignature := verified.Digest
	verificationTotal := verified.Bytes * int64(len(peers))
	var localMount string
	var localTransferManifest recipeTransferManifest
	for index, peer := range peers {
		verificationBase := verified.Bytes * int64(index)
		started, lastUpdate := time.Now(), time.Time{}
		job.ProgressBytes("verifying-peer-model", "Checking existing weights on "+peer.Name+" without downloading.", verificationBase, verificationTotal)
		remoteSignature, signatureErr := recipeSnapshotSignatureWithProgress(ctx, dir, env, &peer, recipe.Engine.Image, volume, relative, recipe.Model.Revision, func(done int64) {
			if time.Since(lastUpdate) < time.Second && done < verified.Bytes {
				return
			}
			message := "Verifying existing weights on " + peer.Name + " — " + formatDownloadProgress(done, verified.Bytes)
			if elapsed := time.Since(started).Seconds(); verified.Bytes > done && elapsed > 1 && done > 0 {
				if eta := recipeProgressETA(verified.Bytes-done, float64(done)/elapsed); eta != "" {
					message += " · " + eta
				}
			}
			job.ProgressBytes("verifying-peer-model", message, verificationBase+done, verificationTotal)
			lastUpdate = time.Now()
		})
		if signatureErr == nil && remoteSignature == localSignature {
			peerDigests[peer.Name] = remoteSignature
			job.ProgressBytes("model-ready", peer.Name+" already has the exact verified model snapshot.", verificationBase+verified.Bytes, verificationTotal)
			continue
		}
		if localTransferManifest.Bytes == 0 {
			localMount, err = recipeModelVolumeMountpoint(ctx, runtime, recipe)
			if err != nil {
				return nil, fmt.Errorf("inspect prepared model cache volume: %w", err)
			}
			localTransferManifest, err = buildRecipeTransferManifest(filepath.Join(localMount, filepath.FromSlash(relative)))
			if err != nil || localTransferManifest.Bytes <= 0 {
				return nil, fmt.Errorf("inspect prepared model cache: %w", err)
			}
		}
		transferTotal := localTransferManifest.Bytes * int64(len(peers))
		transferBase := localTransferManifest.Bytes * int64(index)
		artifactKey := recipeModelArtifactKey(recipe)
		stagingRelative := filepath.ToSlash(filepath.Join(".cloudless-staging", artifactKey, relative))
		_, _ = recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer,
			"docker", "run", "--rm", "-v", volume+":/cache", "--entrypoint", "/bin/sh", recipe.Engine.Image,
			"-c", `set -eu; mkdir -p "$1"`, "cloudless-cache-stage", "/cache/"+filepath.ToSlash(filepath.Dir(stagingRelative))))
		label := fmt.Sprintf("Copying model files from %s to %s over the direct Spark fabric (%d/%d)", localRecipeNodeName(), peer.Name, index+1, len(peers))
		if err := retryRecipePeerTransfer(ctx, job, peer.Name, func() error {
			return incrementalRecipePeerTransfer(ctx, job, localMount, relative, stagingRelative, localTransferManifest,
				dir, env, peer, recipe.Engine.Image, volume, label, transferBase, transferTotal)
		}); err != nil {
			return nil, err
		}
		job.ProgressBytes("verifying-peer-model", "Verifying copied model files on "+peer.Name+" before activation...", verificationBase, verificationTotal)
		remoteSignature, err = recipeSnapshotSignatureWithProgress(ctx, dir, env, &peer, recipe.Engine.Image, volume, stagingRelative, recipe.Model.Revision, func(done int64) {
			job.ProgressBytes("verifying-peer-model", "Verifying copied weights on "+peer.Name+" — "+formatDownloadProgress(done, verified.Bytes), verificationBase+done, verificationTotal)
		})
		if err != nil || remoteSignature != localSignature {
			return nil, fmt.Errorf("%s did not receive an exact, content-verified model snapshot", peer.Name)
		}
		promotion := `set -eu
final="$1"
stage="$2"
backup="$3"
test -d "$stage"
rm -rf "$backup"
mkdir -p "$(dirname "$final")" "$(dirname "$backup")"
had_final=0
if [ -e "$final" ]; then mv "$final" "$backup"; had_final=1; fi
if mv "$stage" "$final"; then
  rm -rf "$backup" "$(dirname "$(dirname "$stage")")"
else
  rm -rf "$final"
  if [ "$had_final" = 1 ]; then mv "$backup" "$final"; fi
  exit 1
fi`
		backupRelative := filepath.ToSlash(filepath.Join(".cloudless-backup", artifactKey, relative))
		_, err = recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer,
			"docker", "run", "--rm", "-v", volume+":/cache", "--entrypoint", "/bin/sh", recipe.Engine.Image,
			"-c", promotion, "cloudless-cache-promote", "/cache/"+relative, "/cache/"+stagingRelative, "/cache/"+backupRelative))
		if err != nil {
			return nil, fmt.Errorf("activate verified model cache on %s: %w", peer.Name, err)
		}
		if err := copyRecipeManifestToPeer(ctx, runtime, dir, env, peer, recipe, volume); err != nil {
			return nil, fmt.Errorf("record verified model manifest on %s: %w", peer.Name, err)
		}
		peerDigests[peer.Name] = remoteSignature
	}
	return peerDigests, nil
}

func copyRecipeManifestToPeer(ctx context.Context, runtime engine.Engine, dir string, env map[string]string, peer recipePeer, recipe localrecipes.Recipe, volume string) error {
	mountpoint, err := recipeModelVolumeMountpoint(ctx, runtime, recipe)
	if err != nil {
		return err
	}
	payload, err := os.ReadFile(recipeModelCompleteMarker(recipe, mountpoint))
	if err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	marker := "/cache/.cloudless-complete/" + recipeModelArtifactKey(recipe) + ".json"
	script := `set -eu
mkdir -p "$(dirname "$1")"
temporary="$1.tmp.$$"
printf '%s' "$2" | base64 -d > "$temporary"
chmod 600 "$temporary"
mv "$temporary" "$1"`
	_, err = recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer,
		"docker", "run", "--rm", "-v", volume+":/cache", "--entrypoint", "/bin/sh", recipe.Engine.Image,
		"-c", script, "cloudless-manifest-copy", marker, encoded))
	return err
}
