package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

const recipeFabricProbeBytes = 1024 * 1024

const recipeNCCLProbeProgram = `import datetime, os, sys
import torch
import torch.distributed as dist
if not torch.cuda.is_available():
    raise SystemExit("CUDA is not available in the checked runtime image")
rank = int(os.environ["RANK"])
world = int(os.environ["WORLD_SIZE"])
dist.init_process_group("nccl", init_method="env://", timeout=datetime.timedelta(seconds=45))
value = torch.tensor([rank + 1.0], device="cuda")
dist.all_reduce(value, op=dist.ReduceOp.SUM)
expected = world * (world + 1) / 2
if value.item() != expected:
    raise SystemExit(f"NCCL all-reduce returned {value.item()}, expected {expected}")
dist.barrier()
dist.destroy_process_group()
print(f"cloudless-nccl-pass rank={rank} world={world}")`

func recipeFabricProbePayload() []byte {
	payload := make([]byte, recipeFabricProbeBytes)
	for index := range payload {
		payload[index] = byte((index*31 + 17) % 251)
	}
	return payload
}

func recipeSHA256(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func recipeTransferRate(bytesTransferred int, elapsed time.Duration) int64 {
	if bytesTransferred <= 0 || elapsed <= 0 {
		return 0
	}
	return int64(float64(bytesTransferred) / elapsed.Seconds())
}

func probeRecipePeerTransfer(ctx context.Context, checkout string, env map[string]string, peer recipePeer) (uploadBytesPerSecond, downloadBytesPerSecond int64, err error) {
	payload := recipeFabricProbePayload()
	wantDigest := recipeSHA256(payload)
	uploadCtx, uploadCancel := context.WithTimeout(ctx, 12*time.Second)
	defer uploadCancel()
	upload := recipeSSHCommand(uploadCtx, checkout, env, peer, "sha256sum")
	upload.Stdin = bytes.NewReader(payload)
	started := time.Now()
	output, err := recipeCommandOutput(upload)
	if err != nil {
		return 0, 0, fmt.Errorf("upload probe to %s: %w", peer.Name, err)
	}
	uploadElapsed := time.Since(started)
	if fields := strings.Fields(output); len(fields) == 0 || fields[0] != wantDigest {
		return 0, 0, fmt.Errorf("upload probe to %s failed integrity verification", peer.Name)
	}

	downloadCtx, downloadCancel := context.WithTimeout(ctx, 12*time.Second)
	defer downloadCancel()
	download := recipeSSHCommand(downloadCtx, checkout, env, peer, "head", "-c", strconv.Itoa(recipeFabricProbeBytes), "/dev/zero")
	started = time.Now()
	downloadPayload, err := download.Output()
	downloadElapsed := time.Since(started)
	if err != nil {
		return 0, 0, fmt.Errorf("download probe from %s: %w", peer.Name, err)
	}
	if len(downloadPayload) != recipeFabricProbeBytes || recipeSHA256(downloadPayload) != recipeSHA256(make([]byte, recipeFabricProbeBytes)) {
		return 0, 0, fmt.Errorf("download probe from %s failed integrity verification", peer.Name)
	}
	return recipeTransferRate(len(payload), uploadElapsed), recipeTransferRate(len(downloadPayload), downloadElapsed), nil
}

func probeRecipePeerTCP(ctx context.Context, checkout string, env map[string]string, peer recipePeer, localIP string) error {
	ip := net.ParseIP(strings.TrimSpace(localIP))
	if ip == nil || ip.To4() == nil {
		return errors.New("cluster local fabric address is invalid")
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: ip.To4(), Port: 0})
	if err != nil {
		return fmt.Errorf("open bounded fabric listener: %w", err)
	}
	defer listener.Close()
	token := "cloudless-fabric-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	accepted := make(chan error, 1)
	go func() {
		_ = listener.SetDeadline(time.Now().Add(8 * time.Second))
		connection, acceptErr := listener.AcceptTCP()
		if acceptErr != nil {
			accepted <- acceptErr
			return
		}
		defer connection.Close()
		payload, readErr := io.ReadAll(io.LimitReader(connection, int64(len(token)+1)))
		if readErr == nil && string(payload) != token {
			readErr = errors.New("fabric TCP probe returned a different token")
		}
		accepted <- readErr
	}()
	address := listener.Addr().(*net.TCPAddr)
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	script := `printf '%s' "$1" > "/dev/tcp/$2/$3"`
	if _, err := recipeCommandOutput(recipeSSHCommand(probeCtx, checkout, env, peer,
		"bash", "-c", script, "cloudless-fabric", token, localIP, strconv.Itoa(address.Port))); err != nil {
		return fmt.Errorf("%s could not reach the coordinator fabric listener: %w", peer.Name, err)
	}
	select {
	case err := <-accepted:
		return err
	case <-probeCtx.Done():
		return probeCtx.Err()
	}
}

func recipeNCCLProbeArgs(image string, rank, world int, env map[string]string) []string {
	name := "cloudless-nccl-probe-" + strconv.Itoa(rank)
	if operation := strings.TrimPrefix(env["CLOUDLESS_RECIPE_OPERATION_ID"], "rop-"); len(operation) >= 8 {
		name += "-" + operation[:8]
	}
	args := []string{"docker", "run", "--rm", "--name", name, "--network", "host", "--ipc", "host", "--gpus", "all", "--ulimit", "memlock=-1",
		"-e", "MASTER_ADDR=" + env["MASTER_ADDR"], "-e", "MASTER_PORT=" + env["MASTER_PORT"],
		"-e", "WORLD_SIZE=" + strconv.Itoa(world), "-e", "RANK=" + strconv.Itoa(rank),
		"-e", "NCCL_DEBUG=WARN"}
	for _, key := range []string{"NCCL_SOCKET_IFNAME", "NCCL_IB_HCA", "NCCL_IB_GID_INDEX"} {
		if value := strings.TrimSpace(env[key]); value != "" {
			args = append(args, "-e", key+"="+value)
		}
	}
	return append(args, "--entrypoint", "python3", image, "-c", recipeNCCLProbeProgram)
}

func probeRecipeNCCL(ctx context.Context, recipe localrecipes.Recipe, checkout string, env map[string]string, peers []recipePeer, image string) (map[string]string, error) {
	if recipe.Distributed.Nodes <= 1 || !strings.EqualFold(recipe.Distributed.Backend, "nccl") {
		return map[string]string{"ncclCollective": "not-required"}, nil
	}
	if strings.TrimSpace(image) == "" {
		return nil, errors.New("NCCL probe requires an immutable prepared runtime image")
	}
	world := len(peers) + 1
	probeCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	type result struct {
		rank    int
		node    string
		elapsed time.Duration
		output  string
		err     error
	}
	results := make(chan result, world)
	var group sync.WaitGroup
	launch := func(rank int, node string, command *exec.Cmd) {
		group.Add(1)
		go func() {
			defer group.Done()
			started := time.Now()
			output, err := recipeCommandOutput(command)
			if err != nil {
				cancel()
			}
			results <- result{rank: rank, node: node, elapsed: time.Since(started), output: output, err: err}
		}()
	}
	localArgs := recipeNCCLProbeArgs(image, 0, world, env)
	launch(0, localRecipeNodeName(), recipeLocalCommand(probeCtx, checkout, env, localArgs[0], localArgs[1:]...))
	for index, peer := range peers {
		args := recipeNCCLProbeArgs(image, index+1, world, env)
		launch(index+1, peer.Name, recipeSSHCommand(probeCtx, checkout, env, peer, args...))
	}
	group.Wait()
	close(results)
	values := map[string]string{"ncclCollective": "pass", "ncclWorldSize": strconv.Itoa(world), "ncclImage": image}
	var combined error
	for result := range results {
		if result.err != nil {
			combined = errors.Join(combined, fmt.Errorf("NCCL rank %d on %s: %w", result.rank, result.node, result.err))
			continue
		}
		if !strings.Contains(result.output, "cloudless-nccl-pass") {
			combined = errors.Join(combined, fmt.Errorf("NCCL rank %d on %s returned no success attestation", result.rank, result.node))
			continue
		}
		values["nccl.rank."+strconv.Itoa(result.rank)+".node"] = result.node
		values["nccl.rank."+strconv.Itoa(result.rank)+".milliseconds"] = strconv.FormatInt(result.elapsed.Milliseconds(), 10)
	}
	if combined != nil {
		return nil, combined
	}
	return values, nil
}

func preflightRecipeFabric(ctx context.Context, recipe localrecipes.Recipe, cluster sparkcluster.State, checkout string, env map[string]string, image string) (map[string]string, error) {
	if recipe.Distributed.Nodes <= 1 {
		return map[string]string{"mode": "single-node"}, nil
	}
	if !cluster.Healthy || !cluster.WorkerReady || len(cluster.LocalIPs) == 0 {
		return nil, errors.New("cluster fabric is not healthy")
	}
	peers, err := recipeDistributionPeers(recipe, cluster, env)
	if err != nil {
		return nil, err
	}
	values := map[string]string{"mode": "distributed", "probeBytes": strconv.Itoa(recipeFabricProbeBytes)}
	for _, peer := range peers {
		uploadRate, downloadRate, err := probeRecipePeerTransfer(ctx, checkout, env, peer)
		if err != nil {
			return nil, err
		}
		if err := probeRecipePeerTCP(ctx, checkout, env, peer, cluster.LocalIPs[0]); err != nil {
			return nil, err
		}
		prefix := "peer." + peer.Name + "."
		values[prefix+"uploadBytesPerSecond"] = strconv.FormatInt(uploadRate, 10)
		values[prefix+"downloadBytesPerSecond"] = strconv.FormatInt(downloadRate, 10)
		values[prefix+"tcpBootstrap"] = "pass"
	}
	ncclValues, err := probeRecipeNCCL(ctx, recipe, checkout, env, peers, image)
	if err != nil {
		return nil, err
	}
	for key, value := range ncclValues {
		values[key] = value
	}
	return values, nil
}
