// Package engine abstracts the container runtime the orchestrator drives.
// Phase 0 ships a Docker (CLI) implementation; the interface keeps Podman or the
// Docker SDK as drop-in alternatives later (see docs/DECISIONS.md, D1).
package engine

import "context"

// RunSpec describes how to launch a container for a catalog app.
type RunSpec struct {
	Name          string            // container name (orchestrator-managed, "cloudless-" prefix)
	Image         string            // image ref to run
	RestartPolicy string            // Docker restart policy; empty preserves the app default (unless-stopped)
	Ports         map[int]int       // hostPort -> containerPort (bound to 127.0.0.1)
	Env           map[string]string // environment variables
	Labels        map[string]string // orchestrator-owned lifecycle labels
	Volumes       map[string]string // hostPath-or-named-volume -> containerPath
	SecretFiles   map[string]string // protected host file -> fixed read-only container path
	GPUs          string            // "all", "0", ... or "" for no GPU
	Network       string            // docker network to join (container-name DNS), or ""
	NetworkAlias  string            // extra DNS alias on the network (e.g. "cloudless-ai")
	IPC           string            // IPC namespace mode (for example "host" for large inference workers)
	Ulimits       []string          // Docker ulimit assignments (for example "memlock=-1")
	ExtraHosts    []string          // host mappings (for example host.docker.internal:host-gateway)
	EntryPoint    string            // optional container entrypoint override
	User          string            // optional numeric container user ("0" is permitted for managed maintenance helpers)
	Args          []string          // extra args appended after the image (container command)
	ReadOnly      bool              // mount the image root filesystem read-only
	CapAdd        []string          // Linux capabilities added to the container
	CapDrop       []string          // Linux capabilities removed from the container
	SecurityOpts  []string          // Docker security options (for example no-new-privileges:true)
	Tmpfs         []string          // isolated writable tmpfs mounts
	PidsLimit     int               // maximum number of container processes (0 = Docker default)
	ShmSize       string            // private /dev/shm allocation (for example 16g)
	Devices       []string          // narrowly admitted host devices (currently only /dev/infiniband)
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

// ContainerIO is a monotonic snapshot of traffic attributed to one managed
// container. Startup observers use it instead of scraping human console output.
type ContainerIO struct {
	ReceivedBytes int64 `json:"receivedBytes"`
	SentBytes     int64 `json:"sentBytes"`
}

// ContainerIOReader is optional so test and third-party Engine implementations
// remain source compatible. Docker and the privileged broker implement it.
type ContainerIOReader interface {
	ContainerIO(ctx context.Context, name string) (ContainerIO, error)
}

// ImageInfo is the bounded metadata Cloudless needs from a local image.
type ImageInfo struct {
	ID           string
	OS           string
	Architecture string
	Size         int64
	EntryPoint   []string
	Command      []string
	RepoDigests  []string
}

// ImageDigestRef identifies one locally available repository tag and digest.
type ImageDigestRef struct {
	Repository string
	Tag        string
	Digest     string
}

// Engine is the container runtime abstraction.
type Engine interface {
	// Available reports whether the runtime is reachable.
	Available(ctx context.Context) error
	// EnsureNetwork creates the named docker network if it does not exist.
	EnsureNetwork(ctx context.Context, name string) error
	// EnsureVolume creates a Cloudless-owned named volume if it does not exist.
	EnsureVolume(ctx context.Context, name string) error
	// VolumeMountpoint returns the host mountpoint for a Cloudless-owned named volume.
	VolumeMountpoint(ctx context.Context, name string) (string, error)
	// ListVolumes returns every Cloudless-owned named volume.
	ListVolumes(ctx context.Context) ([]string, error)
	// RemoveVolume removes a Cloudless-owned named volume.
	RemoveVolume(ctx context.Context, name string) error
	// ConnectNetwork attaches a running container to a network (no-op if already attached).
	ConnectNetwork(ctx context.Context, network, container string) error
	// HasAlias reports whether a container has the given network alias.
	HasAlias(ctx context.Context, container, alias string) (bool, error)
	// ContainerEnvironment returns a managed container's configured environment.
	ContainerEnvironment(ctx context.Context, container string) (map[string]string, error)
	// HermesConfigValue reads one admitted Hermes model setting.
	HermesConfigValue(ctx context.Context, container, key string) (string, error)
	// HasNVIDIARuntime reports whether Docker advertises the NVIDIA runtime.
	HasNVIDIARuntime(ctx context.Context) (bool, error)
	// ContainerNamesByLabel returns managed containers with an admitted ownership label.
	ContainerNamesByLabel(ctx context.Context, key, value string) ([]string, error)
	// ContainerNamesByAncestor returns managed containers created from an image.
	ContainerNamesByAncestor(ctx context.Context, image string) ([]string, error)
	// LogsTail returns a bounded tail of a managed container's logs.
	LogsTail(ctx context.Context, container string, lines int) (string, error)
	// Exec runs a command inside a running container.
	Exec(ctx context.Context, container string, args ...string) error
	// Pull fetches an image.
	Pull(ctx context.Context, image string) error
	// PullStream fetches an image, invoking onLine for each line of pull output.
	PullStream(ctx context.Context, image string, onLine func(string)) error
	// Build builds an image from a local context dir, invoking onLine per output line.
	Build(ctx context.Context, image, contextDir string, onLine func(string)) error
	// RemoveImage force-removes an image (used by reset/uninstall).
	RemoveImage(ctx context.Context, image string) error
	// InspectImage returns bounded metadata for one local image.
	InspectImage(ctx context.Context, image string) (ImageInfo, error)
	// TagImage creates a second local reference for an already admitted image.
	TagImage(ctx context.Context, source, target string) error
	// RemoteImageManifest returns registry manifest JSON for an immutable or tagged image.
	RemoteImageManifest(ctx context.Context, image string) (string, error)
	// RemoteImageConfig returns registry image-configuration JSON.
	RemoteImageConfig(ctx context.Context, image string) (string, error)
	// ExportImage writes an immutable local image archive to Cloudless's bounded transfer staging.
	ExportImage(ctx context.Context, image, destination string) error
	// ListImageDigests returns the local repository/tag/digest inventory.
	ListImageDigests(ctx context.Context) ([]ImageDigestRef, error)
	// Run starts a detached container and returns its ID.
	Run(ctx context.Context, spec RunSpec) (string, error)
	// RunTransient runs an automatically removed helper container and returns its output.
	RunTransient(ctx context.Context, spec RunSpec) (string, error)
	// Stop stops a running container by name.
	Stop(ctx context.Context, name string) error
	// Remove force-removes a container by name.
	Remove(ctx context.Context, name string) error
	// List returns all containers (running and stopped).
	List(ctx context.Context) ([]Container, error)
	// Find returns the container with the given name, or nil if absent.
	Find(ctx context.Context, name string) (*Container, error)
	// Logs returns the captured stdout+stderr of a container.
	Logs(ctx context.Context, name string) (string, error)
	// ImageDigest returns the local repo digest of a pulled image ("sha256:…"), or "" if not pulled.
	ImageDigest(ctx context.Context, image string) (string, error)
	// RemoteDigest returns the digest the registry currently serves for image's tag ("sha256:…").
	RemoteDigest(ctx context.Context, image string) (string, error)
	// ContainerImageDigest returns the repo digest of the image a container runs ("" if absent).
	ContainerImageDigest(ctx context.Context, name string) (string, error)
}
