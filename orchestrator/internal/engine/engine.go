// Package engine abstracts the container runtime the orchestrator drives.
// Phase 0 ships a Docker (CLI) implementation; the interface keeps Podman or the
// Docker SDK as drop-in alternatives later (see docs/DECISIONS.md, D1).
package engine

import "context"

// RunSpec describes how to launch a container for a catalog app.
type RunSpec struct {
	Name    string            // container name (orchestrator-managed, "cloudless-" prefix)
	Image   string            // image ref to run
	Ports   map[int]int       // hostPort -> containerPort (bound to 127.0.0.1)
	Env     map[string]string // environment variables
	Volumes map[string]string // hostPath-or-named-volume -> containerPath
	GPUs         string            // "all", "0", ... or "" for no GPU
	Network      string            // docker network to join (container-name DNS), or ""
	NetworkAlias string            // extra DNS alias on the network (e.g. "cloudless-ai")
	Args         []string          // extra args appended after the image (container command)
}

// Container is the orchestrator's view of a container.
type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`  // running, exited, created, ...
	Status string `json:"status"` // human-readable (e.g. "Up 3 minutes")
	Ports  string `json:"ports"`
}

// Engine is the container runtime abstraction.
type Engine interface {
	// Available reports whether the runtime is reachable.
	Available(ctx context.Context) error
	// EnsureNetwork creates the named docker network if it does not exist.
	EnsureNetwork(ctx context.Context, name string) error
	// ConnectNetwork attaches a running container to a network (no-op if already attached).
	ConnectNetwork(ctx context.Context, network, container string) error
	// HasAlias reports whether a container has the given network alias.
	HasAlias(ctx context.Context, container, alias string) (bool, error)
	// Exec runs a command inside a running container.
	Exec(ctx context.Context, container string, args ...string) error
	// Pull fetches an image.
	Pull(ctx context.Context, image string) error
	// PullStream fetches an image, invoking onLine for each line of pull output.
	PullStream(ctx context.Context, image string, onLine func(string)) error
	// Build builds an image from a local context dir, invoking onLine per output line.
	Build(ctx context.Context, image, contextDir string, onLine func(string)) error
	// Run starts a detached container and returns its ID.
	Run(ctx context.Context, spec RunSpec) (string, error)
	// Stop stops a running container by name.
	Stop(ctx context.Context, name string) error
	// Remove force-removes a container by name.
	Remove(ctx context.Context, name string) error
	// List returns all containers (running and stopped).
	List(ctx context.Context) ([]Container, error)
	// Find returns the container with the given name, or nil if absent.
	Find(ctx context.Context, name string) (*Container, error)
}
