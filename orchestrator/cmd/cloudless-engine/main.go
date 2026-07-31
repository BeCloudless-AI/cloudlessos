//go:build linux

// Command cloudless-engine owns CloudlessOS container-runtime authority. The
// HTTP daemon reaches it only through the typed, peer-authenticated Engine
// protocol; Docker's root-equivalent socket is never exposed to the UI process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/privileged"
)

func main() {
	socketPath := flag.String("socket", engine.DefaultBrokerSocket, "Unix socket path")
	controlGroup := flag.String("group", "cloudless-control", "authorized caller group")
	stateDir := flag.String("state-dir", "/var/lib/cloudless", "Cloudless durable state directory")
	recipeRuntimeRoot := flag.String(
		"recipe-runtime-root", "/var/lib/cloudless/recipes-runtime", "reviewed recipe checkout root",
	)
	modelCacheRoot := flag.String(
		"model-cache-root", modelCacheRootDefault(), "Cloudless host model-cache root",
	)
	modelCacheGroup := flag.String("model-cache-group", "cloudless", "read-only desktop model-cache group")
	legacyModelCacheSource := flag.String(
		"legacy-model-cache-source", "", "explicit legacy cache directory (recovery and qualification only)",
	)
	prepareModelCacheOnly := flag.Bool(
		"prepare-model-cache-only", false, "prepare/import the model cache and exit",
	)
	flag.Parse()
	if os.Geteuid() != 0 {
		log.Fatal("cloudless-engine must run as root")
	}
	runtime := engine.NewDocker()
	if err := prepareModelCache(
		context.Background(), runtime, *modelCacheRoot, "cloudlessd", *modelCacheGroup, *legacyModelCacheSource,
	); err != nil {
		log.Fatalf("prepare model cache: %v", err)
	}
	if *prepareModelCacheOnly {
		log.Printf("model cache is ready at %s", *modelCacheRoot)
		return
	}
	if err := prepareEngineSocket(*socketPath); err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(*socketPath)
	}()
	if err := protectEngineSocket(*socketPath, *controlGroup); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	log.Printf("container broker listening on %s", *socketPath)
	authorize := engine.BrokerAuthorizer(privileged.AuthorizePeer(*controlGroup))
	recipeRunner := engine.ReviewedRecipeDockerRunner{
		Policy: engine.ReviewedRecipePolicy{
			StateDir: *stateDir, RuntimeRoot: *recipeRuntimeRoot,
		},
		DockerBinary: "/usr/bin/docker",
	}
	if err := engine.ServeBrokerWithRecipeExecutor(
		ctx, listener, authorize, runtime, recipeRunner.Execute,
	); err != nil {
		log.Fatal(err)
	}
}

func modelCacheRootDefault() string {
	if value := os.Getenv("CLOUDLESS_MODEL_CACHE"); value != "" {
		return value
	}
	return modelcache.DefaultRoot
}

func prepareModelCache(
	ctx context.Context,
	runtime engine.Engine,
	root, userName, groupName, sourceOverride string,
) error {
	account, err := user.Lookup(userName)
	if err != nil {
		return fmt.Errorf("lookup model-cache owner: %w", err)
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("lookup model-cache group: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse model-cache owner: %w", err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("parse model-cache group: %w", err)
	}
	source := sourceOverride
	if source == "" {
		volumes, err := runtime.ListVolumes(ctx)
		if err != nil {
			return fmt.Errorf("list legacy model-cache volumes: %w", err)
		}
		for _, volume := range volumes {
			if volume != "cloudless-hf" {
				continue
			}
			source, err = runtime.VolumeMountpoint(ctx, volume)
			if err != nil {
				return fmt.Errorf("inspect legacy model-cache volume: %w", err)
			}
			break
		}
	}
	return modelcache.Prepare(root, source, uid, gid)
}

func prepareEngineSocket(socketPath string) error {
	if !filepath.IsAbs(socketPath) {
		return errors.New("socket path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", socketPath)
	}
	return os.Remove(socketPath)
}

func protectEngineSocket(socketPath, groupName string) error {
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("lookup control group: %w", err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("parse control group id: %w", err)
	}
	if err := os.Chown(socketPath, 0, gid); err != nil {
		return fmt.Errorf("set socket ownership: %w", err)
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		return fmt.Errorf("set socket mode: %w", err)
	}
	socketDir := filepath.Dir(socketPath)
	if err := os.Chown(socketDir, 0, gid); err != nil {
		return fmt.Errorf("set socket directory ownership: %w", err)
	}
	if err := os.Chmod(socketDir, 0o750); err != nil {
		return fmt.Errorf("set socket directory mode: %w", err)
	}
	transferDir := filepath.Join(socketDir, "transfers")
	if err := os.Mkdir(transferDir, 0o2770); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create image transfer directory: %w", err)
	}
	transferInfo, err := os.Lstat(transferDir)
	if err != nil || !transferInfo.IsDir() || transferInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("image transfer path is not a real directory")
	}
	if err := os.Chown(transferDir, 0, gid); err != nil {
		return fmt.Errorf("set image transfer ownership: %w", err)
	}
	if err := os.Chmod(transferDir, 0o2770); err != nil {
		return fmt.Errorf("set image transfer mode: %w", err)
	}
	return nil
}
