// Package sparkcluster configures a DGX Spark ConnectX-7 fabric with one
// coordinator and up to seven workers. Two nodes can use a direct cable;
// larger clusters use a shared RoCE-capable switch while normal management
// Ethernet or Wi-Fi remains untouched.
package sparkcluster

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/privileged"
)

const (
	configPath           = "/etc/netplan/99-cloudless-spark-cluster.yaml"
	statePath            = "/var/lib/cloudless/cluster/state.json"
	keyPath              = "/var/lib/cloudless/cluster/id_ed25519"
	knownPath            = "/var/lib/cloudless/cluster/known_hosts"
	remoteAddressCleanup = `for n in 1 2 3 4 5 6 7 8; do for subnet in 0 1; do addr=10.100.$subnet.$n/24; dev=$(ip -o -4 addr show to "$addr" | head -n 1 | tr -s " " | cut -d " " -f 2); if [ -n "$dev" ]; then ip addr del "$addr" dev "$dev" || true; fi; done; done`
	maxNodes             = 8
	workerPath           = "/usr/lib/cloudless/cloudless-cluster-worker"
	sudoersPath          = "/etc/sudoers.d/cloudless-cluster-worker"
	workerScript         = `#!/bin/sh
set -eu
action=${1:-}
case "$action" in
  start)
    image=$(printf %s "$2" | base64 -d)
    head_ip=$(printf %s "$3" | base64 -d)
    worker_ip=$(printf %s "$4" | base64 -d)
    iface=$(printf %s "$5" | base64 -d)
    docker rm -f cloudless-cluster-worker >/dev/null 2>&1 || true
    docker pull "$image"
    install -d -m 0770 /var/lib/cloudless/models-cache
    docker run -d --name cloudless-cluster-worker --restart unless-stopped --network host --ipc host --device nvidia.com/gpu=all --ulimit memlock=-1 --ulimit stack=67108864 -v /var/lib/cloudless/models-cache:/root/.cache/huggingface -e VLLM_HOST_IP="$worker_ip" -e UCX_NET_DEVICES="$iface" -e NCCL_SOCKET_IFNAME="$iface" -e GLOO_SOCKET_IFNAME="$iface" -e TP_SOCKET_IFNAME="$iface" -e RAY_memory_monitor_refresh_ms=0 --entrypoint /bin/bash "$image" -lc "pip install -q --root-user-action=ignore 'ray[default]>=2.9' && exec ray start --block --address=$head_ip:6379 --node-ip-address=$worker_ip --num-gpus=1"
    ;;
  stop)
    docker rm -f cloudless-cluster-worker >/dev/null 2>&1 || true
    ;;
  status)
    docker inspect cloudless-cluster-worker --format '{{.State.Status}}'
    ;;
  model-progress)
    model=$(printf %s "$2" | base64 -d)
    case "$model" in
      ""|/*|*'..'*) echo "invalid model" >&2; exit 2 ;;
    esac
    cache_name=models--$(printf %s "$model" | sed 's#/#--#g')
    path=/var/lib/cloudless/models-cache/hub/$cache_name
    bytes=0
    incomplete=0
    loaded=0
    if [ -d "$path" ]; then
      bytes=$(du -sb "$path" 2>/dev/null | awk '{print $1}')
      incomplete=$(find "$path" -type f -name '*.incomplete' 2>/dev/null | wc -l)
    fi
    if docker exec cloudless-cluster-worker /bin/sh -lc "grep -Rqs 'Model loading took' /tmp/ray/session_latest/logs/worker-*.out 2>/dev/null"; then
      loaded=1
    fi
    printf '%s %s %s\n' "${bytes:-0}" "${incomplete:-0}" "$loaded"
    ;;
  remove-container)
    name=$(printf %s "$2" | base64 -d)
    case "$name" in
      ""|.*|*..*|*[!A-Za-z0-9_.-]*) echo "invalid container name" >&2; exit 2 ;;
    esac
    docker rm -f "$name" >/dev/null 2>&1 || true
    ;;
  upgrade)
    payload=$(printf %s "$2" | base64 -d)
    tmp=$(mktemp)
    printf %s "$payload" >"$tmp"
    chmod 755 "$tmp"
    mv -f "$tmp" /usr/lib/cloudless/cloudless-cluster-worker
    ;;
  *)
    echo "usage: cloudless-cluster-worker start IMAGE_B64 HEAD_IP_B64 WORKER_IP_B64 IFACE_B64 | stop | status | model-progress MODEL_B64 | remove-container NAME_B64 | upgrade SCRIPT_B64" >&2
    exit 2
    ;;
esac
`
)

var (
	hostPattern       = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	userPattern       = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	ifacePattern      = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	containerPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	packetLossPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%\s+packet loss`)
	commandContext    = exec.CommandContext
	now               = time.Now
	mutationMu        sync.Mutex
	telemetryMu       sync.Mutex
	telemetryCache    []PeerTelemetry
	telemetryErr      error
	telemetryAt       time.Time
)

type PeerTelemetry struct {
	Connected             bool           `json:"connected"`
	Reachable             bool           `json:"reachable"`
	Name                  string         `json:"name,omitempty"`
	Host                  string         `json:"host,omitempty"`
	GPUs                  []hardware.GPU `json:"gpus,omitempty"`
	StorageTotalBytes     uint64         `json:"storageTotalBytes,omitempty"`
	StorageAvailableBytes uint64         `json:"storageAvailableBytes,omitempty"`
	Error                 string         `json:"error,omitempty"`
}

// ModelProgress is the peer's observable model preparation state. Bytes are
// read from the persistent Hugging Face cache; Incomplete identifies an active
// Hub download, and WeightsLoaded comes from vLLM's Ray worker log.
type ModelProgress struct {
	Node          string
	Host          string
	Bytes         int64
	Incomplete    int
	WeightsLoaded bool
}

type Peer struct {
	Name string `json:"name"`
	Host string `json:"host"`
	IP   string `json:"ip,omitempty"`
}

type Check struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	OK      bool   `json:"ok"`
	Details string `json:"details,omitempty"`
}

// HealthLayer keeps physically different failure domains separate. Status is
// healthy, attention, idle, or unknown; only attention makes a configured
// cluster unhealthy.
type HealthLayer struct {
	Status  string `json:"status"`
	Label   string `json:"label"`
	Details string `json:"details,omitempty"`
}

type NodeHealth struct {
	PhysicalLink  HealthLayer `json:"physicalLink"`
	ManagementIP  HealthLayer `json:"managementIp"`
	SSH           HealthLayer `json:"ssh"`
	Fabric        HealthLayer `json:"fabric"`
	WorkerRuntime HealthLayer `json:"workerRuntime"`
}

type ClusterHealth struct {
	PhysicalLink  HealthLayer `json:"physicalLink"`
	ManagementIP  HealthLayer `json:"managementIp"`
	SSH           HealthLayer `json:"ssh"`
	Fabric        HealthLayer `json:"fabric"`
	WorkerRuntime HealthLayer `json:"workerRuntime"`
}

type PreflightRequest struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Preflight struct {
	Ready       bool     `json:"ready"`
	LocalName   string   `json:"localName,omitempty"`
	PeerName    string   `json:"peerName,omitempty"`
	PeerHost    string   `json:"peerHost"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	LocalLinks  []string `json:"localLinks,omitempty"`
	PeerLinks   []string `json:"peerLinks,omitempty"`
	NodeIndex   int      `json:"nodeIndex"`
	NodeCount   int      `json:"nodeCount"`
	Checks      []Check  `json:"checks"`
}

type CreateRequest struct {
	Host        string `json:"host"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Fingerprint string `json:"fingerprint"`
}

type RebindRequest struct {
	Node     string `json:"node"`
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// Node is one remote Spark enrolled into the coordinator's cluster. Passwords
// are deliberately never persisted; the installed, restricted SSH identity is
// used for normal operation.
type Node struct {
	Name        string         `json:"name"`
	Host        string         `json:"host"`
	Username    string         `json:"username"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Links       []string       `json:"links,omitempty"`
	IPs         []string       `json:"ips,omitempty"`
	WorkerReady bool           `json:"workerReady"`
	Healthy     bool           `json:"healthy"`
	Selected    bool           `json:"selected"`
	Health      NodeHealth     `json:"health"`
	Telemetry   *PeerTelemetry `json:"telemetry,omitempty"`
}

type State struct {
	Configured         bool           `json:"configured"`
	Healthy            bool           `json:"healthy"`
	WorkerReady        bool           `json:"workerReady"`
	ComputeHealthy     bool           `json:"computeHealthy"`
	ComputeWorkerReady bool           `json:"computeWorkerReady"`
	Role               string         `json:"role,omitempty"`
	LocalName          string         `json:"localName,omitempty"`
	PeerName           string         `json:"peerName,omitempty"`
	PeerHost           string         `json:"peerHost,omitempty"`
	Username           string         `json:"username,omitempty"`
	Fingerprint        string         `json:"fingerprint,omitempty"`
	LocalLinks         []string       `json:"localLinks,omitempty"`
	PeerLinks          []string       `json:"peerLinks,omitempty"`
	LocalIPs           []string       `json:"localIps,omitempty"`
	PeerIPs            []string       `json:"peerIps,omitempty"`
	Nodes              []Node         `json:"nodes,omitempty"`
	NodeCount          int            `json:"nodeCount,omitempty"`
	ComputeNodeCount   int            `json:"computeNodeCount,omitempty"`
	SelectedHosts      []string       `json:"selectedHosts,omitempty"`
	Topology           string         `json:"topology,omitempty"`
	CreatedAt          time.Time      `json:"createdAt,omitempty"`
	Checks             []Check        `json:"checks,omitempty"`
	Error              string         `json:"error,omitempty"`
	Health             ClusterHealth  `json:"health"`
	Operation          Operation      `json:"operation,omitempty"`
	LocalTelemetry     *PeerTelemetry `json:"localTelemetry,omitempty"`
}

type Operation struct {
	ID               string          `json:"id,omitempty"`
	Action           string          `json:"action,omitempty"` // connect | disconnect
	Phase            string          `json:"phase,omitempty"`
	Message          string          `json:"message,omitempty"`
	Percent          int             `json:"percent,omitempty"`
	PeerHost         string          `json:"peerHost,omitempty"`
	PeerName         string          `json:"peerName,omitempty"`
	OwnerPID         int             `json:"ownerPid,omitempty"`
	OwnerBootID      string          `json:"ownerBootId,omitempty"`
	RollbackRequired bool            `json:"rollbackRequired,omitempty"`
	StartedAt        time.Time       `json:"startedAt,omitempty"`
	UpdatedAt        time.Time       `json:"updatedAt,omitempty"`
	Error            string          `json:"error,omitempty"`
	Nodes            []OperationNode `json:"nodes,omitempty"`
}

type OperationNode struct {
	Name    string `json:"name,omitempty"`
	Host    string `json:"host,omitempty"`
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`
	Cleaned bool   `json:"cleaned"`
	Error   string `json:"error,omitempty"`
}

func (o Operation) Active() bool {
	return o.ID != "" && o.Phase != "completed" && o.Phase != "error"
}

type ProgressFunc func(phase, message string, percent int)

func normalizeState(state State) State {
	if len(state.Nodes) == 0 && strings.TrimSpace(state.PeerHost) != "" {
		state.Nodes = []Node{{
			Name: state.PeerName, Host: state.PeerHost, Username: state.Username,
			Fingerprint: state.Fingerprint, Links: state.PeerLinks, IPs: state.PeerIPs,
			WorkerReady: state.WorkerReady,
		}}
	}
	state.NodeCount = 1 + len(state.Nodes)
	if state.Configured && strings.TrimSpace(state.Role) == "" {
		state.Role = "coordinator"
	}
	selected := make(map[string]bool, len(state.SelectedHosts))
	for _, host := range state.SelectedHosts {
		selected[strings.ToLower(strings.TrimSpace(host))] = true
	}
	localHealthy, localHealthKnown := true, false
	for _, check := range state.Checks {
		if check.ID == "local-config" || check.ID == "local-links" {
			localHealthKnown = true
			localHealthy = localHealthy && check.OK
		}
	}
	if !localHealthKnown {
		localHealthy = state.Healthy
	}
	state.ComputeNodeCount = 1
	state.ComputeHealthy = state.Configured && localHealthy
	state.ComputeWorkerReady = state.Configured
	for index := range state.Nodes {
		state.Nodes[index].Selected = len(selected) == 0 || selected[nodeIdentity(state.Nodes[index])]
		if state.Nodes[index].Selected {
			state.ComputeNodeCount++
			state.ComputeHealthy = state.ComputeHealthy && state.Nodes[index].Healthy
			state.ComputeWorkerReady = state.ComputeWorkerReady && state.Nodes[index].WorkerReady
		}
	}
	if state.ComputeNodeCount < 2 {
		state.ComputeHealthy = false
		state.ComputeWorkerReady = false
	}
	if state.NodeCount < 2 && !state.Configured {
		state.NodeCount = 1
		state.ComputeNodeCount = 1
	}
	if state.Topology == "" && state.Configured {
		if state.NodeCount == 2 {
			state.Topology = "direct"
		} else {
			state.Topology = "switch"
		}
	}
	state.WorkerReady = len(state.Nodes) > 0
	for _, node := range state.Nodes {
		state.WorkerReady = state.WorkerReady && node.WorkerReady
	}
	// Legacy fields keep older clients compatible while all new code consumes
	// Nodes. They represent the first worker only.
	if len(state.Nodes) > 0 {
		first := state.Nodes[0]
		state.PeerName, state.PeerHost, state.Username = first.Name, first.Host, first.Username
		state.Fingerprint, state.PeerLinks, state.PeerIPs = first.Fingerprint, first.Links, first.IPs
	}
	return state
}

func nodeIdentity(node Node) string {
	for _, value := range []string{node.Fingerprint, node.Host, node.Name} {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			return value
		}
	}
	return ""
}

func selectedNodesForState(state State) []Node {
	state = normalizeState(state)
	nodes := make([]Node, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		if node.Selected {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func validateTarget(host, username string) error {
	host = strings.TrimSpace(host)
	username = strings.TrimSpace(username)
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return errors.New("use the peer IPv4 address or hostname")
	} else if ip == nil && !hostPattern.MatchString(host) {
		return errors.New("enter a valid Spark hostname or IP address")
	}
	if !userPattern.MatchString(username) {
		return errors.New("enter a valid Linux username")
	}
	return nil
}

func enrollmentIndex(state State, host string) (int, error) {
	state = normalizeState(state)
	if len(state.Nodes) >= maxNodes-1 {
		return 0, fmt.Errorf("this cluster already has the maximum of %d Sparks", maxNodes)
	}
	for _, node := range state.Nodes {
		if strings.EqualFold(strings.TrimSpace(node.Host), strings.TrimSpace(host)) {
			return 0, errors.New("this Spark is already part of the cluster")
		}
	}
	return 2 + len(state.Nodes), nil
}

func findNodeIndex(state State, selector string) (int, error) {
	selector = strings.ToLower(strings.TrimSpace(selector))
	if selector == "" {
		return -1, errors.New("choose an enrolled Spark")
	}
	match := -1
	for index, node := range state.Nodes {
		if selector == strings.ToLower(strings.TrimSpace(node.Fingerprint)) ||
			selector == strings.ToLower(strings.TrimSpace(node.Host)) ||
			selector == strings.ToLower(strings.TrimSpace(node.Name)) {
			if match >= 0 {
				return -1, fmt.Errorf("Spark selector %q is ambiguous", selector)
			}
			match = index
		}
	}
	if match < 0 {
		return -1, fmt.Errorf("Spark %q is not enrolled", selector)
	}
	return match, nil
}

func run(ctx context.Context, env []string, stdin []byte, name string, args ...string) (string, error) {
	cmd := commandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s", text)
	}
	return text, nil
}

// Discover returns DGX Spark candidates advertised through mDNS SSH service.
// A manual hostname remains available in the UI for networks that suppress mDNS.
func Discover(ctx context.Context) ([]Peer, error) {
	out, err := run(ctx, nil, nil, "avahi-browse", "-ptr", "_ssh._tcp")
	if err != nil {
		return nil, fmt.Errorf("Spark discovery is unavailable: %w", err)
	}
	seen := map[string]Peer{}
	state, _ := load()
	enrolled := map[string]bool{}
	for _, node := range state.Nodes {
		enrolled[strings.ToLower(node.Host)] = true
		for _, ip := range node.IPs {
			enrolled[strings.ToLower(ip)] = true
		}
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, ";")
		if len(parts) < 9 || parts[0] != "=" || parts[2] != "IPv4" {
			continue
		}
		host := strings.TrimSuffix(strings.TrimSpace(parts[6]), ".")
		ip := strings.TrimSpace(parts[7])
		if host == "" || net.ParseIP(ip) == nil {
			continue
		}
		if enrolled[strings.ToLower(host)] || enrolled[strings.ToLower(ip)] {
			continue
		}
		if local, _ := os.Hostname(); strings.EqualFold(strings.TrimSuffix(local, ".local"), strings.TrimSuffix(host, ".local")) {
			continue
		}
		seen[host] = Peer{Name: discoveryName(parts[3], host), Host: host, IP: ip}
	}
	peers := make([]Peer, 0, len(seen))
	for _, peer := range seen {
		peers = append(peers, peer)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Name < peers[j].Name })
	return peers, nil
}

func discoveryName(raw, host string) string {
	name := strings.TrimSpace(strings.ReplaceAll(raw, `\032`, " "))
	name = strings.TrimSpace(strings.TrimSuffix(name, " SSH"))
	if name == "" {
		name = strings.TrimSuffix(host, ".local")
	}
	return name
}

func hostKey(ctx context.Context, host string) (line, fingerprint string, err error) {
	// DGX Spark's Ubuntu image always provisions an Ed25519 host key. Asking
	// ssh-keyscan for several algorithms made its output order nondeterministic,
	// so the security code could appear to change between otherwise identical
	// checks. Pin the strongest supported identity for a stable confirmation.
	out, err := run(ctx, nil, nil, "ssh-keyscan", "-T", "5", "-t", "ed25519", host)
	if err != nil {
		return "", "", fmt.Errorf("could not read the peer SSH identity: %w", err)
	}
	for _, candidate := range strings.Split(out, "\n") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || strings.HasPrefix(candidate, "#") {
			continue
		}
		tmp, e := os.CreateTemp("", "cloudless-host-key-*")
		if e != nil {
			return "", "", e
		}
		name := tmp.Name()
		_, _ = tmp.WriteString(candidate + "\n")
		_ = tmp.Close()
		fp, fpErr := run(ctx, nil, nil, "ssh-keygen", "-lf", name, "-E", "sha256")
		_ = os.Remove(name)
		if fpErr != nil {
			continue
		}
		fields := strings.Fields(fp)
		if len(fields) >= 2 {
			return candidate, fields[1], nil
		}
	}
	return "", "", errors.New("the peer did not present a supported SSH host key")
}

func writeKnownHost(line string) error {
	if err := os.MkdirAll(filepath.Dir(knownPath), 0o700); err != nil {
		return err
	}
	line = strings.TrimSpace(line)
	host := strings.Fields(line)
	if len(host) == 0 {
		return errors.New("empty SSH host identity")
	}
	var kept []string
	if data, err := os.ReadFile(knownPath); err == nil {
		for _, existing := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			fields := strings.Fields(existing)
			if len(fields) > 0 && fields[0] != host[0] && strings.TrimSpace(existing) != "" {
				kept = append(kept, existing)
			}
		}
	}
	kept = append(kept, line)
	return os.WriteFile(knownPath, []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}

func sshCommand(host, username string, password bool) (string, []string) {
	args := []string{"-o", "ConnectTimeout=8", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + knownPath, username + "@" + host}
	if password {
		// sshpass options must be followed by the command it should execute.
		// Descriptor 3 carries the one-shot password. It must never enter argv
		// or SSHPASS, where another process could recover it from /proc.
		return "sshpass", append([]string{"-d", "3", "ssh"}, args...)
	}
	args = append([]string{"-i", keyPath, "-o", "IdentitiesOnly=yes"}, args...)
	return "ssh", args
}

func runWithSecretFD(ctx context.Context, secret string, stdin []byte, name string, args ...string) (string, error) {
	if secret == "" || len(secret) > 4096 {
		return "", errors.New("peer administrator password must contain 1 through 4096 bytes")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("create protected credential pipe: %w", err)
	}
	cmd := commandContext(ctx, name, args...)
	cmd.ExtraFiles = []*os.File{reader} // first inherited descriptor is fd 3
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	written := make(chan error, 1)
	go func() {
		_, writeErr := writer.Write([]byte(secret + "\n"))
		closeErr := writer.Close()
		if writeErr != nil {
			written <- writeErr
		} else {
			written <- closeErr
		}
	}()
	out, commandErr := cmd.CombinedOutput()
	_ = reader.Close()
	writeErr := <-written
	text := strings.TrimSpace(strings.ReplaceAll(string(out), secret, "[REDACTED]"))
	if commandErr != nil {
		if text == "" {
			text = commandErr.Error()
		}
		return text, fmt.Errorf("%s", text)
	}
	if writeErr != nil {
		return text, fmt.Errorf("write protected credential: %w", writeErr)
	}
	return text, nil
}

func remote(ctx context.Context, host, username, password, command string, stdin []byte) (string, error) {
	program, args := sshCommand(host, username, password != "")
	args = append(args, command)
	if password != "" {
		return runWithSecretFD(ctx, password, stdin, program, args...)
	}
	return run(ctx, nil, stdin, program, args...)
}

func activeLinks(raw string) []string {
	var links []string
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		for i, field := range fields {
			if field == "==>" && i+2 < len(fields) && strings.EqualFold(strings.Trim(fields[i+2], "()"), "up") {
				name := fields[i+1]
				if ifacePattern.MatchString(name) && !seen[name] {
					links = append(links, name)
					seen[name] = true
				}
			}
		}
	}
	sort.Strings(links)
	return links
}

func connectXDiagnostic(raw string) string {
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "cable unplugged") || strings.Contains(lower, "cable removal") {
		return "the Spark firmware does not detect a supported cable in either rear high-speed port; fully reseat both ends and verify the cable is a QSFP112 DAC, 400 GbE, Ethernet-only cable"
	}
	return ""
}

func localLinks(ctx context.Context) ([]string, error) {
	out, err := run(ctx, nil, nil, "ibdev2netdev")
	if err != nil {
		return nil, fmt.Errorf("ConnectX-7 inspection failed: %w", err)
	}
	links := activeLinks(out)
	if len(links) != 2 {
		if len(links) == 0 && strings.TrimSpace(out) == "" {
			if kernel, kernelErr := run(ctx, nil, nil, "journalctl", "-k", "-b", "--no-pager", "-o", "cat"); kernelErr == nil {
				if diagnostic := connectXDiagnostic(kernel); diagnostic != "" {
					return links, errors.New(diagnostic)
				}
			}
			return links, errors.New("the high-speed network hardware is asleep; leave the fabric connected, restart this Spark, then try again")
		}
		return links, fmt.Errorf("the high-speed fabric is not fully ready yet (%d of 2 links active); check the cable or switch port, then restart this Spark if needed", len(links))
	}
	return links, nil
}

func remoteFacts(ctx context.Context, request PreflightRequest) (name, arch, dgx string, links []string, linkDiagnostic string, err error) {
	command := `set -u; hostname; uname -m; if [ -r /etc/dgx-release ]; then tr '\n' ' ' </etc/dgx-release; else echo NOT_DGX; fi; echo __CLOUDLESS_LINKS__; ibdev2netdev 2>/dev/null || true; echo __CLOUDLESS_KERNEL__; sudo -S -p '' journalctl -k -b --no-pager -o cat 2>/dev/null || true`
	out, err := remote(ctx, request.Host, request.Username, request.Password, command, []byte(request.Password+"\n"))
	if err != nil {
		return "", "", "", nil, "", fmt.Errorf("could not authenticate to or inspect the other Spark: %w", err)
	}
	parts := strings.SplitN(out, "__CLOUDLESS_LINKS__", 2)
	if len(parts) != 2 {
		return "", "", "", nil, "", errors.New("the peer returned an unexpected readiness response")
	}
	head := strings.Split(strings.TrimSpace(parts[0]), "\n")
	if len(head) < 3 {
		return "", "", "", nil, "", errors.New("the peer returned incomplete system information")
	}
	linkParts := strings.SplitN(parts[1], "__CLOUDLESS_KERNEL__", 2)
	linkOutput := linkParts[0]
	if len(linkParts) == 2 {
		linkDiagnostic = connectXDiagnostic(linkParts[1])
	}
	return strings.TrimSpace(head[0]), strings.TrimSpace(head[1]), strings.TrimSpace(strings.Join(head[2:], " ")), activeLinks(linkOutput), linkDiagnostic, nil
}

func routeConflict(raw string) bool {
	return strings.Contains(raw, "10.100.0.0/24") || strings.Contains(raw, "10.100.1.0/24")
}

// reusableClusterAddresses recognizes addresses left in the live kernel after
// an earlier Cloudless disconnect. Netplan can leave addresses behind on the
// unmanaged ConnectX ports even after its YAML is removed. They are safe to
// reuse only when every remaining cluster address exactly matches the address
// and dedicated interface Cloudless is about to configure.
func reusableClusterAddresses(raw string, links []string, lastOctet int) bool {
	if len(links) != 2 {
		return false
	}
	expected := map[string]string{
		links[0]: fmt.Sprintf("10.100.0.%d/24", lastOctet),
		links[1]: fmt.Sprintf("10.100.1.%d/24", lastOctet),
	}
	found := false
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != "inet" || !isClusterAddress(fields[3]) {
			continue
		}
		found = true
		if expected[fields[1]] != fields[3] {
			return false
		}
	}
	return found
}

func PreflightCheck(ctx context.Context, request PreflightRequest) (Preflight, error) {
	request.Host, request.Username = strings.TrimSpace(request.Host), strings.TrimSpace(request.Username)
	existing, loadErr := load()
	if loadErr != nil {
		return Preflight{}, loadErr
	}
	nodeIndex, err := enrollmentIndex(existing, request.Host)
	if err != nil {
		return Preflight{}, err
	}
	result := Preflight{PeerHost: request.Host, NodeIndex: nodeIndex, NodeCount: nodeIndex, Checks: []Check{}}
	add := func(id, label string, ok bool, details string) {
		result.Checks = append(result.Checks, Check{ID: id, Label: label, OK: ok, Details: details})
	}
	if err := validateTarget(request.Host, request.Username); err != nil {
		return result, err
	}
	if request.Password == "" {
		return result, errors.New("the peer administrator password is required and will not be saved")
	}
	localName, _ := os.Hostname()
	result.LocalName = localName
	line, fingerprint, err := hostKey(ctx, request.Host)
	add("identity", "Peer identity", err == nil, fingerprint)
	if err != nil {
		return result, nil
	}
	result.Fingerprint = fingerprint
	if err := writeKnownHost(line); err != nil {
		return result, err
	}
	local, localErr := localLinks(ctx)
	result.LocalLinks = local
	localLinkDetails := strings.Join(local, ", ")
	if localErr != nil {
		localLinkDetails = localErr.Error()
	}
	add("local-link", "High-speed connection on this Spark", localErr == nil, localLinkDetails)
	_, localConfigErr := os.Stat(configPath)
	localConfigOK := errors.Is(localConfigErr, os.ErrNotExist)
	localConfigLabel := "No previous Cloudless cluster configuration"
	if existing.Configured {
		localConfigOK = localConfigErr == nil
		localConfigLabel = "Existing Cloudless cluster network is ready"
	}
	add("local-config", localConfigLabel, localConfigOK, configPath)
	localRoutes, localRouteErr := run(ctx, nil, nil, "ip", "route", "show")
	localAddressesOK := localRouteErr == nil && (!routeConflict(localRoutes) || existing.Configured)
	localAddressDetails := "10.100.0.0/24 and 10.100.1.0/24"
	if !localAddressesOK && !existing.Configured && localConfigOK {
		if localAddresses, addressErr := run(ctx, nil, nil, "ip", "-o", "-4", "addr", "show"); addressErr == nil && reusableClusterAddresses(localAddresses, local, 1) {
			localAddressesOK = true
			localAddressDetails = "Existing Cloudless addresses on the dedicated ports will be reused."
		}
	}
	add("local-addresses", "Private cluster addresses are available", localAddressesOK, localAddressDetails)
	peerName, arch, dgx, peerLinks, peerLinkDiagnostic, remoteErr := remoteFacts(ctx, request)
	result.PeerName, result.PeerLinks = peerName, peerLinks
	add("ssh", "Secure administrator connection", remoteErr == nil, peerName)
	if remoteErr == nil {
		add("peer-hardware", "Peer is a DGX Spark", strings.Contains(strings.ToLower(dgx), "dgx") || strings.Contains(strings.ToLower(dgx), "gb10"), dgx)
		add("peer-architecture", "Peer uses ARM64", arch == "aarch64" || arch == "arm64", arch)
		peerLinkDetails := strings.Join(peerLinks, ", ")
		if len(peerLinks) == 0 {
			peerLinkDetails = peerLinkDiagnostic
			if peerLinkDetails == "" {
				peerLinkDetails = "The high-speed network hardware is asleep. Leave the cable connected, restart this Spark, then try again."
			}
		} else if len(peerLinks) != 2 {
			peerLinkDetails = fmt.Sprintf("The cable is not fully ready yet (%d of 2 links active). Check the plug or restart with the cable connected.", len(peerLinks))
		}
		add("peer-link", "High-speed connection on the other Spark", len(peerLinks) == 2, peerLinkDetails)
		_, peerConfigErr := remote(ctx, request.Host, request.Username, request.Password, "test ! -e "+configPath, nil)
		peerConfigMissing := peerConfigErr == nil
		peerRoutes, peerRouteErr := remote(ctx, request.Host, request.Username, request.Password, "ip route show", nil)
		peerAddressesOK := peerRouteErr == nil && !routeConflict(peerRoutes)
		peerAddressDetails := "10.100.0.0/24 and 10.100.1.0/24"
		if !peerAddressesOK && peerConfigMissing {
			if peerAddresses, addressErr := remote(ctx, request.Host, request.Username, request.Password, "ip -o -4 addr show", nil); addressErr == nil && reusableClusterAddresses(peerAddresses, peerLinks, nodeIndex) {
				peerAddressesOK = true
				peerAddressDetails = "Existing Cloudless addresses on the dedicated ports will be reused."
			}
		}
		add("peer-addresses", "Peer cluster addresses are available", peerAddressesOK, peerAddressDetails)
		add("peer-config", "No previous Cloudless cluster configuration on peer", peerConfigMissing, configPath)
		_, sudoErr := remote(ctx, request.Host, request.Username, request.Password, "sudo -S -p '' true", []byte(request.Password+"\n"))
		add("sudo", "Peer administrator access", sudoErr == nil, "Required to apply the dedicated network configuration")
	}
	result.Ready = len(result.Checks) >= 11
	for _, check := range result.Checks {
		result.Ready = result.Ready && check.OK
	}
	return result, nil
}

func netplan(links []string, lastOctet int) string {
	return fmt.Sprintf("network:\n  version: 2\n  ethernets:\n    %s:\n      addresses: [10.100.0.%d/24]\n      dhcp4: false\n      optional: true\n    %s:\n      addresses: [10.100.1.%d/24]\n      dhcp4: false\n      optional: true\n", links[0], lastOctet, links[1], lastOctet)
}

func ensureKey(ctx context.Context) (string, error) {
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		if _, err := run(ctx, nil, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "cloudless-spark-cluster", "-f", keyPath); err != nil {
			return "", err
		}
	}
	data, err := os.ReadFile(keyPath + ".pub")
	return strings.TrimSpace(string(data)), err
}

func localPrivilegeClient() privileged.Client {
	return privileged.Client{SocketPath: os.Getenv("CLOUDLESS_PRIVILEGED_SOCKET")}
}

func applyLocal(ctx context.Context, links []string, nodeIndex int) error {
	return localPrivilegeClient().ConfigureClusterNetwork(ctx, links, nodeIndex)
}

func rollbackLocal() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = localPrivilegeClient().Do(ctx, privileged.ActionClusterNetworkRemove)
}

func isClusterAddress(cidr string) bool {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	v4 := ip.To4()
	return v4 != nil && v4[0] == 10 && v4[1] == 100 && (v4[2] == 0 || v4[2] == 1)
}

func rollbackRemote(host, username, password, publicKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	encodedKey := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(publicKey)))
	command := fmt.Sprintf(`sudo -S -p '' sh -c 'set -eu; if [ -x %s ]; then %s stop; fi; rm -f %s %s %s; home=$(getent passwd "$1" | cut -d: -f6); if [ -n "$home" ] && [ -f "$home/.ssh/authorized_keys" ] && [ -n "$2" ]; then key=$(printf %%s "$2" | base64 -d); tmp=$(mktemp); grep -vxF "$key" "$home/.ssh/authorized_keys" >"$tmp" || true; install -m 600 -o "$1" "$tmp" "$home/.ssh/authorized_keys"; rm -f "$tmp"; fi; netplan generate; netplan apply; %s' sh %s %s`, workerPath, workerPath, configPath, workerPath, sudoersPath, remoteAddressCleanup, username, encodedKey)
	_, _ = remote(ctx, host, username, password, command, []byte(password+"\n"))
}

func Create(ctx context.Context, request CreateRequest) (State, error) {
	return CreateWithProgress(ctx, request, nil)
}

// RebindManagementAddress updates only the normal-network address used to
// reach an enrolled worker. The private fabric identity and addresses remain
// unchanged. The saved SSH fingerprint must match at the new address, which
// prevents silently replacing a failed Spark with another machine.
func RebindManagementAddress(ctx context.Context, request RebindRequest) (State, error) {
	mutationMu.Lock()
	defer mutationMu.Unlock()
	request.Node, request.Host = strings.TrimSpace(request.Node), strings.TrimSpace(request.Host)
	request.Username = strings.TrimSpace(request.Username)
	if err := validateTarget(request.Host, request.Username); err != nil {
		return State{}, err
	}
	if request.Password == "" {
		return State{}, errors.New("the peer administrator password is required and will not be saved")
	}
	state, err := load()
	if err != nil {
		return State{}, err
	}
	if !state.Configured {
		return State{}, errors.New("no Spark cluster is configured")
	}
	index, err := findNodeIndex(state, request.Node)
	if err != nil {
		return State{}, err
	}
	for otherIndex, node := range state.Nodes {
		if otherIndex != index && strings.EqualFold(strings.TrimSpace(node.Host), request.Host) {
			return State{}, errors.New("the new address already belongs to another enrolled Spark")
		}
	}
	line, fingerprint, err := hostKey(ctx, request.Host)
	if err != nil {
		return State{}, fmt.Errorf("read the Spark identity at its new address: %w", err)
	}
	if state.Nodes[index].Fingerprint == "" || fingerprint != state.Nodes[index].Fingerprint {
		return State{}, errors.New("the SSH fingerprint at the new address does not match the enrolled Spark")
	}
	name, arch, dgx, _, _, err := remoteFacts(ctx, PreflightRequest{
		Host: request.Host, Username: request.Username, Password: request.Password,
	})
	if err != nil {
		return State{}, err
	}
	if arch != "aarch64" && arch != "arm64" {
		return State{}, errors.New("the machine at the new address is not ARM64 DGX Spark hardware")
	}
	dgx = strings.ToLower(dgx)
	if !strings.Contains(dgx, "dgx") && !strings.Contains(dgx, "gb10") {
		return State{}, errors.New("the machine at the new address is not a DGX Spark")
	}
	if err := writeKnownHost(line); err != nil {
		return State{}, err
	}
	state.Nodes[index].Host = request.Host
	state.Nodes[index].Username = request.Username
	if strings.TrimSpace(name) != "" {
		state.Nodes[index].Name = strings.TrimSpace(name)
	}
	state.Nodes[index].Healthy = false
	state.Nodes[index].Health = NodeHealth{}
	state.Nodes[index].Telemetry = nil
	state.Error = ""
	if err := save(state); err != nil {
		return State{}, err
	}
	telemetryMu.Lock()
	telemetryAt, telemetryCache, telemetryErr = time.Time{}, nil, nil
	telemetryMu.Unlock()
	return normalizeState(state), nil
}

func CreateWithProgress(ctx context.Context, request CreateRequest, report ProgressFunc) (result State, resultErr error) {
	mutationMu.Lock()
	defer mutationMu.Unlock()
	if err := validateTarget(request.Host, request.Username); err != nil {
		return State{}, err
	}
	existing, err := load()
	if err != nil {
		return State{}, err
	}
	if _, err := enrollmentIndex(existing, request.Host); err != nil {
		return State{}, err
	}
	if request.Password == "" || request.Fingerprint == "" {
		return State{}, errors.New("peer authentication and fingerprint confirmation are required")
	}
	operation := newOperation("connect", request.Host)
	operation.Nodes = []OperationNode{{
		Host: request.Host, Phase: "preflight",
		Message: "Waiting for identity, hardware, and fabric verification.",
	}}
	mutationStarted := false
	progress := func(phase, message string, percent int) {
		operation.Phase, operation.Message, operation.Percent = phase, message, percent
		operation.Nodes[0].Phase, operation.Nodes[0].Message = phase, message
		_ = persistOperation(operation)
		if report != nil {
			report(phase, message, percent)
		}
	}
	if err := persistOperation(operation); err != nil {
		return State{}, fmt.Errorf("record cluster operation: %w", err)
	}
	defer func() {
		if resultErr == nil {
			return
		}
		operation.Phase = "error"
		operation.Message = "CloudlessOS could not finish adding this Spark."
		operation.Error = resultErr.Error()
		operation.RollbackRequired = mutationStarted
		operation.Nodes[0].Phase = "error"
		operation.Nodes[0].Message = operation.Message
		operation.Nodes[0].Error = resultErr.Error()
		_ = persistOperation(operation)
	}()
	progress("preflight", "Rechecking identity, login, hardware, and both high-speed ports.", 8)
	preflight, err := PreflightCheck(ctx, PreflightRequest{Host: request.Host, Username: request.Username, Password: request.Password})
	if err != nil {
		return State{}, err
	}
	if !preflight.Ready {
		return State{}, errors.New("both the coordinator and the new Spark must pass every readiness check")
	}
	if preflight.Fingerprint != request.Fingerprint {
		return State{}, errors.New("the peer SSH fingerprint changed; stop and verify the other Spark")
	}
	operation.PeerName = preflight.PeerName
	operation.Nodes[0].Name = preflight.PeerName
	progress("identity", "Creating a restricted Spark-to-Spark identity.", 20)
	publicKey, err := ensureKey(ctx)
	if err != nil {
		return State{}, fmt.Errorf("create cluster identity: %w", err)
	}
	remoteConfig := base64.StdEncoding.EncodeToString([]byte(netplan(preflight.PeerLinks, preflight.NodeIndex)))
	remoteKey := base64.StdEncoding.EncodeToString([]byte(publicKey + "\n"))
	remoteWorker := base64.StdEncoding.EncodeToString([]byte(workerScript))
	remoteSudoers := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s ALL=(root) NOPASSWD: %s *\n", request.Username, workerPath)))
	remoteCommand := fmt.Sprintf(`sudo -S -p '' sh -c 'set -eu; printf %%s "$1" | base64 -d > %s; chmod 600 %s; home=$(getent passwd "$2" | cut -d: -f6); test -n "$home"; install -d -m 700 -o "$2" "$home/.ssh"; touch "$home/.ssh/authorized_keys"; chown "$2" "$home/.ssh/authorized_keys"; chmod 600 "$home/.ssh/authorized_keys"; key=$(printf %%s "$3" | base64 -d); grep -qxF "$key" "$home/.ssh/authorized_keys" || printf "%%s\\n" "$key" >> "$home/.ssh/authorized_keys"; install -d -m 755 /usr/lib/cloudless; printf %%s "$4" | base64 -d > %s; chown root:root %s; chmod 755 %s; printf %%s "$5" | base64 -d > %s; chown root:root %s; chmod 440 %s; visudo -cf %s >/dev/null; netplan generate; netplan apply' sh %s %s %s %s %s`, configPath, configPath, workerPath, workerPath, workerPath, sudoersPath, sudoersPath, sudoersPath, sudoersPath, remoteConfig, request.Username, remoteKey, remoteWorker, remoteSudoers)
	mutationStarted = true
	progress("peer-network", "Configuring the private fabric on "+preflight.PeerName+".", 38)
	if _, err := remote(ctx, request.Host, request.Username, request.Password, remoteCommand, []byte(request.Password+"\n")); err != nil {
		rollbackRemote(request.Host, request.Username, request.Password, publicKey)
		return State{}, fmt.Errorf("configure the other Spark: %w", err)
	}
	if !existing.Configured {
		progress("coordinator-network", "Configuring the private fabric on this Spark.", 58)
		if err := applyLocal(ctx, preflight.LocalLinks, 1); err != nil {
			rollbackRemote(request.Host, request.Username, request.Password, publicKey)
			return State{}, fmt.Errorf("configure this Spark: %w", err)
		}
	}
	state := existing
	if !state.Configured {
		state = State{Configured: true, Role: "coordinator", LocalName: preflight.LocalName, LocalLinks: preflight.LocalLinks, LocalIPs: []string{"10.100.0.1", "10.100.1.1"}, CreatedAt: now().UTC()}
	}
	workerIPs := []string{fmt.Sprintf("10.100.0.%d", preflight.NodeIndex), fmt.Sprintf("10.100.1.%d", preflight.NodeIndex)}
	state.Nodes = append(state.Nodes, Node{
		Name: preflight.PeerName, Host: request.Host, Username: request.Username,
		Fingerprint: request.Fingerprint, Links: preflight.PeerLinks, IPs: workerIPs,
		WorkerReady: true,
	})
	state.Topology = "switch"
	if len(state.Nodes) == 1 {
		state.Topology = "direct"
	}
	state = normalizeState(state)
	progress("saving", "Saving the enrolled Spark and restricted runtime profile.", 76)
	operation.Phase = "verifying"
	operation.Message = "Waiting for both private fabric paths to settle."
	operation.Percent = 84
	state.Operation = operation
	if err := save(state); err != nil {
		if !existing.Configured {
			rollbackLocal()
		}
		rollbackRemote(request.Host, request.Username, request.Password, publicKey)
		return State{}, err
	}
	// netplan apply returns once the configuration has been handed to the
	// network stack, but the ConnectX addresses and neighbour entries can take
	// a few more seconds to become usable. Absorb that normal convergence here
	// so a successful setup does not immediately ask the user to run a second
	// health check.
	result, resultErr = waitForHealthy(ctx, 15*time.Second)
	if resultErr != nil {
		return result, resultErr
	}
	operation.Phase = "completed"
	operation.Message = fmt.Sprintf("%d-Spark cluster is connected and healthy.", result.NodeCount)
	operation.Percent = 100
	operation.RollbackRequired = false
	operation.Nodes[0].Phase = "connected"
	operation.Nodes[0].Message = "The Spark is enrolled and both fabric paths are verified."
	result.Operation = operation
	if err := save(result); err != nil {
		return State{}, err
	}
	if report != nil {
		report(operation.Phase, operation.Message, operation.Percent)
	}
	return result, nil
}

func waitForHealthy(ctx context.Context, timeout time.Duration) (State, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()

	var last State
	for {
		status, err := Status(ctx)
		if err != nil {
			return State{}, err
		}
		last = status
		if status.Healthy {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-deadline.C:
			// The cluster remains configured. Returning its last health result
			// lets the UI explain a real cable/path issue instead of turning a
			// completed configuration into an opaque setup failure.
			return last, nil
		case <-ticker.C:
		}
	}
}

func save(state State) error {
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(normalizeState(state), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, append(data, '\n'), 0o600)
}

func load() (State, error) {
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if recovered, interrupted := recoverInterruptedOperation(state, os.Getpid(), currentBootID()); interrupted {
		state = recovered
		_ = save(state)
	}
	return normalizeState(state), nil
}

func recoverInterruptedOperation(state State, pid int, bootID string) (State, bool) {
	operation := state.Operation
	if !operation.Active() || operation.OwnerPID == 0 {
		return state, false
	}
	sameProcess := operation.OwnerPID == pid
	if operation.OwnerBootID != "" && bootID != "" {
		sameProcess = sameProcess && operation.OwnerBootID == bootID
	}
	if sameProcess {
		return state, false
	}
	operation.Phase = "error"
	operation.Message = "Cluster setup was interrupted. Re-enter the peer credentials to check or clean up the connection."
	operation.Error = "the Cloudless service or machine restarted during cluster mutation"
	operation.RollbackRequired = true
	operation.UpdatedAt = now().UTC()
	for index := range operation.Nodes {
		if !operation.Nodes[index].Cleaned && operation.Nodes[index].Error == "" {
			operation.Nodes[index].Phase = "unknown"
			operation.Nodes[index].Message = "The previous operation ended before cleanup could be verified."
		}
	}
	state.Operation = operation
	return state, true
}

func currentBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func newOperation(action, peerHost string) Operation {
	timestamp := now().UTC()
	return Operation{
		ID: fmt.Sprintf("%s-%d", action, timestamp.UnixNano()), Action: action,
		Phase: "starting", Message: "Preparing cluster operation.", Percent: 1,
		PeerHost: peerHost, OwnerPID: os.Getpid(), OwnerBootID: currentBootID(),
		StartedAt: timestamp, UpdatedAt: timestamp,
	}
}

func persistOperation(operation Operation) error {
	current, err := load()
	if err != nil {
		return err
	}
	operation.UpdatedAt = now().UTC()
	current.Operation = operation
	return save(current)
}

// NodeCount returns the coordinator plus all enrolled workers. It is safe for
// launch planning and falls back to one when no cluster exists.
func NodeCount() int {
	state, err := load()
	if err != nil || !state.Configured {
		return 1
	}
	return nodeCountForState(state)
}

// ComputeNodes returns the coordinator plus the explicitly selected workers.
// An empty selection preserves the historical behaviour of using every
// enrolled worker.
func ComputeNodes() int {
	state, err := load()
	if err != nil || !state.Configured {
		return 1
	}
	return computeNodeCountForState(state)
}

// SetSelection persists an exact worker subset for managed distributed
// inference. Selectors may be stable fingerprints, hosts, or node names.
// Passing no selectors restores automatic use of every enrolled worker.
func SetSelection(ctx context.Context, selectors []string) (State, error) {
	mutationMu.Lock()
	defer mutationMu.Unlock()
	state, err := Status(ctx)
	if err != nil {
		return State{}, err
	}
	if !state.Configured {
		return State{}, errors.New("connect a Spark cluster before selecting compute nodes")
	}
	if len(selectors) == 0 {
		state.SelectedHosts = nil
		if err := save(state); err != nil {
			return State{}, err
		}
		return normalizeState(state), nil
	}
	if len(selectors) > maxNodes-1 {
		return State{}, fmt.Errorf("select between 1 and %d worker Sparks", maxNodes-1)
	}
	chosen := make([]string, 0, len(selectors))
	used := make(map[string]bool, len(selectors))
	for _, selector := range selectors {
		selector = strings.ToLower(strings.TrimSpace(selector))
		if selector == "" {
			return State{}, errors.New("worker selection contains an empty node")
		}
		var match *Node
		for index := range state.Nodes {
			node := &state.Nodes[index]
			if selector == strings.ToLower(strings.TrimSpace(node.Fingerprint)) ||
				selector == strings.ToLower(strings.TrimSpace(node.Host)) ||
				selector == strings.ToLower(strings.TrimSpace(node.Name)) {
				if match != nil {
					return State{}, fmt.Errorf("worker selector %q is ambiguous", selector)
				}
				match = node
			}
		}
		if match == nil {
			return State{}, fmt.Errorf("selected worker %q is not enrolled", selector)
		}
		identity := nodeIdentity(*match)
		if used[identity] {
			return State{}, fmt.Errorf("worker %q was selected more than once", selector)
		}
		if !match.Healthy || !match.WorkerReady {
			return State{}, fmt.Errorf("%s must be healthy and worker-ready before it can be selected", match.Name)
		}
		used[identity] = true
		chosen = append(chosen, identity)
	}
	state.SelectedHosts = chosen
	if err := save(state); err != nil {
		return State{}, err
	}
	return normalizeState(state), nil
}

// Snapshot returns persisted, credential-free cluster state without network
// probes. Support bundles use it so interrupted operations remain diagnosable.
func Snapshot() (State, error) { return load() }

func nodeCountForState(state State) int { return max(1, normalizeState(state).NodeCount) }

func computeNodeCountForState(state State) int {
	return max(1, normalizeState(state).ComputeNodeCount)
}

func Status(ctx context.Context) (State, error) {
	state, err := load()
	if err != nil || !state.Configured {
		return state, err
	}
	if state.Role != "coordinator" {
		state.Healthy, state.ComputeHealthy = false, false
		state.Error = "this Spark is not the enrolled cluster coordinator; disconnect the existing cluster before reversing roles"
		return state, errors.New(state.Error)
	}
	state.Checks = nil
	state.Healthy = true
	state.Error = ""
	_, configErr := os.Stat(configPath)
	state.Checks = append(state.Checks, Check{ID: "local-config", Label: "Cloudless cluster network configuration", OK: configErr == nil})
	if configErr != nil {
		state.Healthy = false
	}
	links, linkErr := localLinks(ctx)
	linksOK := linkErr == nil && strings.Join(links, "\x00") == strings.Join(state.LocalLinks, "\x00")
	localPhysical := HealthLayer{Status: "healthy", Label: "High-speed cable", Details: strings.Join(links, ", ")}
	if !linksOK {
		localPhysical.Status = "attention"
		localPhysical.Details = "The coordinator cannot see the two expected ConnectX-7 paths."
		if linkErr != nil {
			localPhysical.Details = linkErr.Error()
		}
	}
	linkCheck := Check{ID: "local-links", Label: "ConnectX-7 cable and interfaces", OK: linksOK, Details: strings.Join(links, ", ")}
	if !linksOK {
		state.Healthy = false
		if linkErr != nil {
			linkCheck.Details = linkErr.Error()
		}
	}
	state.Checks = append(state.Checks, linkCheck)
	nodeChecks := make([][]Check, len(state.Nodes))
	var wg sync.WaitGroup
	for nodeIndex, node := range state.Nodes {
		wg.Add(1)
		go func(nodeIndex int, node Node) {
			defer wg.Done()
			checks := make([]Check, 0, 6+len(node.IPs))
			health := NodeHealth{
				PhysicalLink:  HealthLayer{Status: "unknown", Label: "High-speed cable"},
				ManagementIP:  HealthLayer{Status: "unknown", Label: "Normal network"},
				SSH:           HealthLayer{Status: "unknown", Label: "Secure login"},
				Fabric:        HealthLayer{Status: "unknown", Label: "Spark fabric"},
				WorkerRuntime: HealthLayer{Status: "unknown", Label: "Distributed runtime"},
			}
			_, managementErr := run(ctx, nil, nil, "ping", "-c", "1", "-W", "1", node.Host)
			health.ManagementIP.Status = "healthy"
			health.ManagementIP.Details = node.Host + " is reachable."
			if managementErr != nil {
				health.ManagementIP.Status = "attention"
				health.ManagementIP.Details = managementErr.Error()
			}
			checks = append(checks, Check{ID: "management-" + node.Host, Label: node.Name + " management network", OK: managementErr == nil, Details: health.ManagementIP.Details})

			_, sshErr := remote(ctx, node.Host, node.Username, "", "true", nil)
			health.SSH.Status = "healthy"
			health.SSH.Details = "Cloudless restricted-key login works."
			if sshErr != nil {
				health.SSH.Status = "attention"
				health.SSH.Details = sshErr.Error()
			}
			checks = append(checks, Check{ID: "ssh-" + node.Host, Label: node.Name + " secure login", OK: sshErr == nil, Details: health.SSH.Details})

			if sshErr == nil {
				remoteLinksRaw, remoteLinksErr := remote(ctx, node.Host, node.Username, "", "ibdev2netdev", nil)
				remoteLinks := activeLinks(remoteLinksRaw)
				remoteLinksOK := remoteLinksErr == nil && len(remoteLinks) == 2
				health.PhysicalLink.Status = "healthy"
				health.PhysicalLink.Details = strings.Join(remoteLinks, ", ")
				if !remoteLinksOK {
					health.PhysicalLink.Status = "attention"
					health.PhysicalLink.Details = "The peer cannot see both expected ConnectX-7 paths."
					if remoteLinksErr != nil {
						health.PhysicalLink.Details = remoteLinksErr.Error()
					}
				}
				checks = append(checks, Check{ID: "links-" + node.Host, Label: node.Name + " high-speed interfaces", OK: remoteLinksOK, Details: health.PhysicalLink.Details})

				_, helperErr := remote(ctx, node.Host, node.Username, "", "test -x "+workerPath, nil)
				if helperErr != nil {
					health.WorkerRuntime.Status = "attention"
					health.WorkerRuntime.Details = "The Cloudless worker helper is missing; reconnect this Spark."
				} else {
					runtimeOutput, runtimeErr := remote(ctx, node.Host, node.Username, "", "sudo -n "+workerPath+" status", nil)
					if runtimeErr == nil && strings.TrimSpace(runtimeOutput) == "running" {
						health.WorkerRuntime.Status = "healthy"
						health.WorkerRuntime.Details = "Distributed inference worker is active."
					} else {
						health.WorkerRuntime.Status = "idle"
						health.WorkerRuntime.Details = "Worker is ready and will start when a distributed model is loaded."
					}
				}
				checks = append(checks, Check{ID: "worker-helper-" + node.Host, Label: node.Name + " distributed runtime", OK: helperErr == nil, Details: health.WorkerRuntime.Details})
			} else {
				health.PhysicalLink.Status = "unknown"
				health.PhysicalLink.Details = "Secure login must work before the peer cable can be inspected."
				health.WorkerRuntime.Status = "unknown"
				health.WorkerRuntime.Details = "Secure login must work before the worker can be inspected."
			}
			if len(node.IPs) != 2 {
				checks = append(checks, Check{ID: "addresses-" + node.Host, Label: node.Name + " fabric addresses", OK: false, Details: "Reconnect this Spark to assign both private fabric paths."})
			}
			fabricOK := len(node.IPs) == 2
			var fabricFailures []string
			for _, ip := range node.IPs {
				// Netplan can return before address discovery on the new fabric has
				// completely settled. A short burst prevents that normal warm-up from
				// being presented as packet loss immediately after cluster creation.
				pingOutput, pingErr := run(ctx, nil, nil, "ping", fabricPingArguments(ip)...)
				pathOK := fabricProbeHealthy(pingOutput, pingErr)
				check := Check{ID: "ping-" + ip, Label: node.Name + " · ConnectX-7 path " + ip, OK: pathOK}
				if !pathOK {
					check.Details = strings.TrimSpace(pingOutput)
					if check.Details == "" && pingErr != nil {
						check.Details = pingErr.Error()
					}
					fabricOK = false
					fabricFailures = append(fabricFailures, ip)
				}
				checks = append(checks, check)
			}
			health.Fabric.Status = "healthy"
			health.Fabric.Details = "Both private high-speed paths pass traffic."
			if !fabricOK {
				health.Fabric.Status = "attention"
				health.Fabric.Details = "One or more private high-speed paths cannot pass traffic."
				if len(fabricFailures) > 0 {
					health.Fabric.Details += " Failed: " + strings.Join(fabricFailures, ", ")
				}
			}
			state.Nodes[nodeIndex].Health = health
			nodeChecks[nodeIndex] = checks
		}(nodeIndex, node)
	}
	wg.Wait()
	for nodeIndex, checks := range nodeChecks {
		state.Nodes[nodeIndex].Healthy = true
		for _, check := range checks {
			if !check.OK {
				state.Nodes[nodeIndex].Healthy = false
				state.Healthy = false
			}
			state.Checks = append(state.Checks, check)
		}
	}
	physicalLayers := []HealthLayer{localPhysical}
	managementLayers := make([]HealthLayer, 0, len(state.Nodes))
	sshLayers := make([]HealthLayer, 0, len(state.Nodes))
	fabricLayers := make([]HealthLayer, 0, len(state.Nodes))
	runtimeLayers := make([]HealthLayer, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		physicalLayers = append(physicalLayers, node.Health.PhysicalLink)
		managementLayers = append(managementLayers, node.Health.ManagementIP)
		sshLayers = append(sshLayers, node.Health.SSH)
		fabricLayers = append(fabricLayers, node.Health.Fabric)
		runtimeLayers = append(runtimeLayers, node.Health.WorkerRuntime)
	}
	state.Health = ClusterHealth{
		PhysicalLink:  aggregateHealthLayer("High-speed cable and interfaces", physicalLayers, false),
		ManagementIP:  aggregateHealthLayer("Normal network reachability", managementLayers, false),
		SSH:           aggregateHealthLayer("Secure Cloudless login", sshLayers, false),
		Fabric:        aggregateHealthLayer("Private Spark fabric", fabricLayers, false),
		WorkerRuntime: aggregateHealthLayer("Distributed runtime", runtimeLayers, true),
	}
	if telemetry, telemetryErr := ClusterGPUs(ctx); telemetryErr == nil {
		byHost := make(map[string]PeerTelemetry, len(telemetry))
		for _, peer := range telemetry {
			byHost[peer.Host] = peer
		}
		for i := range state.Nodes {
			if peer, ok := byHost[state.Nodes[i].Host]; ok {
				copy := peer
				state.Nodes[i].Telemetry = &copy
			}
		}
	}
	if telemetry := localTelemetry(ctx, state.LocalName); telemetry != nil {
		state.LocalTelemetry = telemetry
	}
	state = normalizeState(state)
	return state, nil
}

func aggregateHealthLayer(label string, layers []HealthLayer, runtimeLayer bool) HealthLayer {
	result := HealthLayer{Status: "unknown", Label: label}
	if len(layers) == 0 {
		result.Details = "No worker Sparks are enrolled."
		return result
	}
	healthy, idle, unknown, attention := 0, 0, 0, 0
	for _, layer := range layers {
		switch layer.Status {
		case "healthy":
			healthy++
		case "idle":
			idle++
		case "attention":
			attention++
		default:
			unknown++
		}
	}
	switch {
	case attention > 0:
		result.Status = "attention"
		result.Details = fmt.Sprintf("%d of %d checks need attention.", attention, len(layers))
	case unknown > 0:
		result.Status = "unknown"
		result.Details = fmt.Sprintf("%d of %d checks are still unknown.", unknown, len(layers))
	case runtimeLayer && healthy > 0 && idle > 0:
		result.Status = "attention"
		result.Details = "Distributed workers are only active on part of the cluster."
	case runtimeLayer && idle == len(layers):
		result.Status = "idle"
		result.Details = "Workers are ready and currently idle."
	default:
		result.Status = "healthy"
		result.Details = "All enrolled Sparks passed this layer."
	}
	return result
}

func fabricPingArguments(ip string) []string {
	return []string{"-c", "3", "-i", "0.25", "-W", "1", ip}
}

func fabricProbeHealthy(output string, err error) bool {
	if err != nil {
		return false
	}
	match := packetLossPattern.FindStringSubmatch(strings.ToLower(output))
	if len(match) != 2 {
		return false
	}
	loss, parseErr := strconv.ParseFloat(match[1], 64)
	return parseErr == nil && loss == 0
}

// ClusterGPUs returns live GB10 telemetry from every worker. It uses the
// restricted cluster identity and probes nodes concurrently so dashboard
// latency does not grow linearly with cluster size.
func ClusterGPUs(ctx context.Context) ([]PeerTelemetry, error) {
	telemetryMu.Lock()
	defer telemetryMu.Unlock()
	if time.Since(telemetryAt) < 2500*time.Millisecond {
		return telemetryCache, telemetryErr
	}
	state, err := load()
	if err != nil || !state.Configured {
		telemetryCache, telemetryErr, telemetryAt = nil, err, time.Now()
		return nil, err
	}
	results := make([]PeerTelemetry, len(state.Nodes))
	var wg sync.WaitGroup
	for i, node := range state.Nodes {
		wg.Add(1)
		go func(i int, node Node) {
			defer wg.Done()
			result := PeerTelemetry{Connected: true, Name: node.Name, Host: node.Host}
			command := `nvidia-smi --query-gpu=index,name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw,power.limit,driver_version --format=csv,noheader,nounits; printf '\n__CLOUDLESS_MEM__\n'; awk '/MemTotal:/{t=$2}/MemAvailable:/{a=$2}/^Cached:/{c=$2}/SReclaimable:/{r=$2}END{printf "%d %d %d\n",t/1024,a/1024,(c+r)/1024}' /proc/meminfo; printf '__CLOUDLESS_DISK__\n'; df -B1 --output=size,avail / | tail -n 1`
			output, remoteErr := remote(ctx, node.Host, node.Username, "", command, nil)
			if remoteErr != nil {
				result.Error = remoteErr.Error()
				results[i] = result
				return
			}
			gpuText, memText, found := strings.Cut(output, "__CLOUDLESS_MEM__")
			if !found {
				remoteErr = errors.New("peer returned incomplete GPU telemetry")
				result.Error = remoteErr.Error()
				results[i] = result
				return
			}
			memText, diskText, _ := strings.Cut(memText, "__CLOUDLESS_DISK__")
			gpus := hardware.ParseGPUsCSV(strings.TrimSpace(gpuText))
			memFields := strings.Fields(memText)
			if len(memFields) >= 2 {
				totalMB, _ := strconv.Atoi(memFields[0])
				availableMB, _ := strconv.Atoi(memFields[1])
				reclaimableMB := 0
				if len(memFields) >= 3 {
					reclaimableMB, _ = strconv.Atoi(memFields[2])
				}
				hardware.ApplyUnifiedMemoryBudget(gpus, hardware.NewMemoryBudget(totalMB, availableMB, reclaimableMB))
			}
			result.StorageTotalBytes, result.StorageAvailableBytes = parseStorageTelemetry(diskText)
			for i := range gpus {
				gpus[i].Node = node.Name
				gpus[i].Remote = true
			}
			result.Reachable = true
			result.GPUs = gpus
			results[i] = result
		}(i, node)
	}
	wg.Wait()
	telemetryCache, telemetryErr, telemetryAt = results, nil, time.Now()
	return results, nil
}

func parseStorageTelemetry(output string) (uint64, uint64) {
	fields := strings.Fields(output)
	if len(fields) < 2 {
		return 0, 0
	}
	total, totalErr := strconv.ParseUint(fields[0], 10, 64)
	available, availableErr := strconv.ParseUint(fields[1], 10, 64)
	if totalErr != nil || availableErr != nil || available > total {
		return 0, 0
	}
	return total, available
}

func localTelemetry(ctx context.Context, name string) *PeerTelemetry {
	gpus, gpuErr := hardware.GPUs(ctx)
	diskOutput, diskErr := run(ctx, nil, nil, "df", "-B1", "--output=size,avail", "/")
	total, available := parseStorageTelemetry(diskOutput)
	if gpuErr != nil && diskErr != nil {
		return nil
	}
	return &PeerTelemetry{
		Connected: true, Reachable: true, Name: name, Host: "localhost",
		GPUs: gpus, StorageTotalBytes: total, StorageAvailableBytes: available,
	}
}

// PeerGPUs is retained for older API consumers and represents the first
// worker. New callers should use ClusterGPUs.
func PeerGPUs(ctx context.Context) (PeerTelemetry, error) {
	peers, err := ClusterGPUs(ctx)
	if err != nil || len(peers) == 0 {
		return PeerTelemetry{}, err
	}
	return peers[0], nil
}

// StartWorker launches every remote Ray rank using the passwordless,
// narrowly-scoped helper installed during enrollment.
func StartWorker(ctx context.Context, image, model string) error {
	state, err := Status(ctx)
	if err != nil {
		return err
	}
	if !state.Configured || !state.ComputeHealthy {
		return errors.New("the selected Spark compute subset is not healthy")
	}
	if !state.ComputeWorkerReady {
		return errors.New("reconnect or replace any selected legacy Spark workers before starting distributed models")
	}
	if strings.TrimSpace(image) == "" || strings.TrimSpace(model) == "" {
		return errors.New("worker image and model are required")
	}
	nodes := selectedNodesForState(state)
	if len(nodes) == 0 {
		return errors.New("select at least one worker Spark for distributed inference")
	}
	headIP := "10.100.0.1"
	if len(state.LocalIPs) > 0 {
		headIP = state.LocalIPs[0]
	}
	image64 := base64.StdEncoding.EncodeToString([]byte(image))
	upgrade64 := base64.StdEncoding.EncodeToString([]byte(workerScript))
	enc := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	errs := make(chan error, len(nodes))
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(node Node) {
			defer wg.Done()
			upgrade := fmt.Sprintf("sudo -n %s upgrade %s", workerPath, upgrade64)
			if _, err := remote(ctx, node.Host, node.Username, "", upgrade, nil); err != nil {
				errs <- fmt.Errorf("update distributed worker on %s: reconnect this Spark once (%w)", node.Name, err)
				return
			}
			workerIP, iface := "", "enP2p1s0f1np1"
			if len(node.IPs) > 0 {
				workerIP = node.IPs[0]
			}
			if len(node.Links) > 0 {
				iface = node.Links[0]
			}
			if workerIP == "" {
				errs <- fmt.Errorf("start distributed worker on %s: missing fabric address", node.Name)
				return
			}
			command := fmt.Sprintf("sudo -n %s start %s %s %s %s", workerPath, image64, enc(headIP), enc(workerIP), enc(iface))
			if _, err := remote(ctx, node.Host, node.Username, "", command, nil); err != nil {
				errs <- fmt.Errorf("start distributed worker on %s: %w", node.Name, err)
			}
		}(node)
	}
	wg.Wait()
	close(errs)
	var failures []string
	for err := range errs {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		_ = StopWorker(cleanupCtx)
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// RemoveContainers removes exact container names from every selected worker
// through the enrolled, sudo-restricted helper. It deliberately cannot run a
// recipe shell command, select an image, mount a path, or execute arbitrary
// Docker arguments, so Doctor can clean up an orphaned untrusted recipe safely.
func RemoveContainers(ctx context.Context, names []string) error {
	state, err := load()
	if err != nil {
		return err
	}
	if !state.Configured || !state.WorkerReady {
		return errors.New("the distributed workers are not configured")
	}
	if len(names) == 0 || len(names) > 32 {
		return errors.New("one through 32 container names are required")
	}
	encoded := make([]string, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if !containerPattern.MatchString(name) || strings.Contains(name, "..") {
			return fmt.Errorf("invalid container name %q", name)
		}
		encoded[i] = base64.StdEncoding.EncodeToString([]byte(name))
	}
	nodes := selectedNodesForState(state)
	if len(nodes) == 0 {
		return errors.New("select at least one worker Spark")
	}
	upgrade64 := base64.StdEncoding.EncodeToString([]byte(workerScript))
	errs := make(chan error, len(nodes))
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(node Node) {
			defer wg.Done()
			if _, err := remote(ctx, node.Host, node.Username, "", fmt.Sprintf("sudo -n %s upgrade %s", workerPath, upgrade64), nil); err != nil {
				errs <- fmt.Errorf("update cleanup helper on %s: %w", node.Name, err)
				return
			}
			for _, name64 := range encoded {
				command := fmt.Sprintf("sudo -n %s remove-container %s", workerPath, name64)
				if _, err := remote(ctx, node.Host, node.Username, "", command, nil); err != nil {
					errs <- fmt.Errorf("remove orphaned container on %s: %w", node.Name, err)
					return
				}
			}
		}(node)
	}
	wg.Wait()
	close(errs)
	var failures []string
	for err := range errs {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// PeerModelProgress reports read-only preparation telemetry from the paired
// Spark through the restricted worker helper installed during pairing.
func PeerModelProgress(ctx context.Context, model string) (ModelProgress, error) {
	all, err := ClusterModelProgress(ctx, model)
	if err != nil || len(all) == 0 {
		return ModelProgress{}, err
	}
	return all[0], nil
}

// ClusterModelProgress returns preparation state for every worker.
func ClusterModelProgress(ctx context.Context, model string) ([]ModelProgress, error) {
	state, err := load()
	if err != nil {
		return nil, err
	}
	if !state.Configured || !state.WorkerReady {
		return nil, errors.New("the distributed workers are not configured")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, errors.New("model is required")
	}
	nodes := selectedNodesForState(state)
	if len(nodes) == 0 {
		return nil, errors.New("select at least one worker Spark for distributed inference")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(model))
	command := fmt.Sprintf("sudo -n %s model-progress %s", workerPath, encoded)
	results := make([]ModelProgress, len(nodes))
	errs := make(chan error, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(i int, node Node) {
			defer wg.Done()
			output, err := remote(ctx, node.Host, node.Username, "", command, nil)
			if err != nil {
				errs <- fmt.Errorf("read model progress from %s: %w", node.Name, err)
				return
			}
			fields := strings.Fields(output)
			if len(fields) != 3 {
				errs <- fmt.Errorf("read model progress from %s: invalid response", node.Name)
				return
			}
			bytes, bytesErr := strconv.ParseInt(fields[0], 10, 64)
			incomplete, incompleteErr := strconv.Atoi(fields[1])
			loaded, loadedErr := strconv.Atoi(fields[2])
			if bytesErr != nil || incompleteErr != nil || loadedErr != nil || bytes < 0 || incomplete < 0 || (loaded != 0 && loaded != 1) {
				errs <- fmt.Errorf("read model progress from %s: invalid values", node.Name)
				return
			}
			results[i] = ModelProgress{Node: node.Name, Host: node.Host, Bytes: bytes, Incomplete: incomplete, WeightsLoaded: loaded == 1}
		}(i, node)
	}
	wg.Wait()
	close(errs)
	var failures []string
	for err := range errs {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		return results, errors.New(strings.Join(failures, "; "))
	}
	return results, nil
}

// StopWorker removes the peer inference worker while leaving the Spark fabric
// configured for later distributed launches.
func StopWorker(ctx context.Context) error {
	state, err := load()
	if err != nil || !state.Configured {
		return err
	}
	command := fmt.Sprintf("sudo -n %s stop", workerPath)
	errs := make(chan error, len(state.Nodes))
	var wg sync.WaitGroup
	for _, node := range state.Nodes {
		wg.Add(1)
		go func(node Node) {
			defer wg.Done()
			if _, err := remote(ctx, node.Host, node.Username, "", command, nil); err != nil {
				errs <- fmt.Errorf("%s: %w", node.Name, err)
			}
		}(node)
	}
	wg.Wait()
	close(errs)
	var failures []string
	for err := range errs {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("stop distributed workers: %s", strings.Join(failures, "; "))
	}
	return nil
}

// CoordinatorSpec converts the normal single-node vLLM container into rank 0
// of the configured N-node Ray topology.
func CoordinatorSpec(spec engine.RunSpec) engine.RunSpec {
	state, _ := load()
	return coordinatorSpec(spec, state)
}

func coordinatorSpec(spec engine.RunSpec, state State) engine.RunSpec {
	headIP, iface := "10.100.0.1", "enP2p1s0f1np1"
	if len(state.LocalIPs) > 0 {
		headIP = state.LocalIPs[0]
	}
	if len(state.LocalLinks) > 0 {
		iface = state.LocalLinks[0]
	}
	spec.Network = "host"
	spec.NetworkAlias = ""
	spec.Ports = nil
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	spec.Env["VLLM_HOST_IP"] = headIP
	spec.Env["UCX_NET_DEVICES"] = iface
	spec.Env["NCCL_SOCKET_IFNAME"] = iface
	spec.Env["GLOO_SOCKET_IFNAME"] = iface
	spec.Env["TP_SOCKET_IFNAME"] = iface
	spec.Env["RAY_memory_monitor_refresh_ms"] = "0"
	spec.EntryPoint = "/bin/bash"
	serve := shellJoin(spec.Args)
	nodes := max(2, computeNodeCountForState(state))
	spec.Args = []string{"-lc", `pip install -q --root-user-action=ignore 'ray[default]>=2.9' && ray start --head --port=6379 --node-ip-address="$VLLM_HOST_IP" --num-gpus=1 && touch /tmp/cloudless-ray-head && while [ ! -f /tmp/cloudless-ray-worker ]; do sleep 1; done; exec ` + serve + fmt.Sprintf(" --distributed-executor-backend ray --tensor-parallel-size %d", nodes)}
	return spec
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "'\"'\"'")+"'")
	}
	return strings.Join(quoted, " ")
}

// ProxySpec preserves the cloudless-ai Docker alias for apps while rank 0 uses
// host networking for NCCL and native vLLM multi-node rendezvous traffic.
func ProxySpec() engine.RunSpec {
	return ProxySpecPort(8000)
}

// ProxySpecPort preserves the stable cloudless-ai alias for a reviewed local
// recipe whose OpenAI-compatible server listens on a different host port.
func ProxySpecPort(port int) engine.RunSpec {
	return ProxySpecTarget("host.docker.internal", port)
}

// ProxySpecTarget preserves the stable cloudless-ai alias while allowing a
// local recipe to define where its OpenAI-compatible server is listening.
func ProxySpecTarget(host string, port int) engine.RunSpec {
	if port < 1 || port > 65535 {
		port = 8000
	}
	if host == "" {
		host = "host.docker.internal"
	}
	spec := engine.RunSpec{
		Name: "cloudless-cluster-engine-proxy", Image: "alpine/socat:latest", RestartPolicy: "no",
		Network: "cloudless", NetworkAlias: "cloudless-ai",
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		Args:       []string{"tcp-listen:8000,fork,reuseaddr", fmt.Sprintf("tcp-connect:%s:%d", host, port)},
	}
	if port != 8000 {
		spec.Ports = map[int]int{8000: 8000}
	}
	return spec
}

// SSHIdentityPaths returns the restricted identity installed during cluster
// enrollment. Local recipe runners use it through a private generated SSH
// config; credentials are never copied into recipe metadata.
func SSHIdentityPaths() (string, string) { return keyPath, knownPath }

// ValidateDisconnectAccess proves every peer administrator credential before
// Cloudless stops distributed inference. Disconnect repeats this check under
// its mutation lock, but this preflight prevents a typo from needlessly
// unloading a healthy cluster model.
func ValidateDisconnectAccess(ctx context.Context, passwords map[string]string) error {
	state, err := load()
	if err != nil {
		return err
	}
	if !state.Configured {
		return nil
	}
	for _, node := range state.Nodes {
		password := strings.TrimSpace(passwords[node.Host])
		if password == "" {
			password = strings.TrimSpace(passwords[node.Name])
		}
		if password == "" {
			password = strings.TrimSpace(passwords["*"])
		}
		if password == "" {
			return fmt.Errorf("administrator password is required for %s", node.Name)
		}
		if _, err := remote(ctx, node.Host, node.Username, password, "sudo -S -p '' true", []byte(password+"\n")); err != nil {
			return fmt.Errorf("verify administrator access on %s before disconnecting: %w", node.Name, err)
		}
	}
	return nil
}

func Disconnect(ctx context.Context, passwords map[string]string) error {
	return DisconnectWithProgress(ctx, passwords, nil)
}

func DisconnectWithProgress(ctx context.Context, passwords map[string]string, report ProgressFunc) (resultErr error) {
	mutationMu.Lock()
	defer mutationMu.Unlock()
	state, err := load()
	if err != nil {
		return err
	}
	if !state.Configured {
		return nil
	}
	publicKey := ""
	if data, readErr := os.ReadFile(keyPath + ".pub"); readErr == nil {
		publicKey = strings.TrimSpace(string(data))
	}
	encodedKey := base64.StdEncoding.EncodeToString([]byte(publicKey))
	type remoteRemoval struct {
		node     Node
		password string
	}
	removals := make([]remoteRemoval, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		password := strings.TrimSpace(passwords[node.Host])
		if password == "" {
			password = strings.TrimSpace(passwords[node.Name])
		}
		if password == "" {
			password = strings.TrimSpace(passwords["*"])
		}
		if password == "" {
			return fmt.Errorf("administrator password is required for %s", node.Name)
		}
		if _, err := remote(ctx, node.Host, node.Username, password, "sudo -S -p '' true", []byte(password+"\n")); err != nil {
			return fmt.Errorf("verify administrator access on %s before disconnecting: %w", node.Name, err)
		}
		removals = append(removals, remoteRemoval{node: node, password: password})
	}
	operation := newOperation("disconnect", "")
	for _, removal := range removals {
		operation.Nodes = append(operation.Nodes, OperationNode{
			Name: removal.node.Name, Host: removal.node.Host, Phase: "pending",
			Message: "Waiting to remove the distributed worker and private fabric.",
		})
	}
	progress := func(phase, message string, percent int) {
		operation.Phase, operation.Message, operation.Percent = phase, message, percent
		state.Operation = operation
		_ = save(state)
		if report != nil {
			report(phase, message, percent)
		}
	}
	progress("stopping-workers", "Stopping distributed workers on every enrolled Spark.", 20)
	defer func() {
		if resultErr == nil {
			return
		}
		operation.Phase = "error"
		operation.Message = "Cluster disconnect needs attention."
		operation.Error = resultErr.Error()
		operation.RollbackRequired = true
		state.Operation = operation
		_ = save(state)
	}()
	type cleanupResult struct {
		host string
		err  error
	}
	results := make(chan cleanupResult, len(removals))
	var wg sync.WaitGroup
	for _, removal := range removals {
		wg.Add(1)
		go func(removal remoteRemoval) {
			defer wg.Done()
			node, password := removal.node, removal.password
			remoteCommand := fmt.Sprintf(`sudo -S -p '' sh -c 'set -eu; if [ -x %s ]; then %s stop; fi; rm -f %s %s %s; home=$(getent passwd "$1" | cut -d: -f6); if [ -n "$home" ] && [ -f "$home/.ssh/authorized_keys" ] && [ -n "$2" ]; then key=$(printf %%s "$2" | base64 -d); tmp=$(mktemp); grep -vxF "$key" "$home/.ssh/authorized_keys" >"$tmp" || true; install -m 600 -o "$1" "$tmp" "$home/.ssh/authorized_keys"; rm -f "$tmp"; fi; netplan generate; netplan apply; %s' sh %s %s`, workerPath, workerPath, configPath, workerPath, sudoersPath, remoteAddressCleanup, node.Username, encodedKey)
			if _, err := remote(ctx, node.Host, node.Username, password, remoteCommand, []byte(password+"\n")); err != nil {
				results <- cleanupResult{host: node.Host, err: fmt.Errorf("disconnect %s: %w", node.Name, err)}
				return
			}
			results <- cleanupResult{host: node.Host}
		}(removal)
	}
	wg.Wait()
	close(results)
	var failures []string
	for result := range results {
		if recordCleanupResult(&operation, result.host, result.err) && result.err != nil {
			failures = append(failures, result.err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	progress("coordinator-network", "Removing the private fabric from this Spark.", 72)
	if err := localPrivilegeClient().Do(ctx, privileged.ActionClusterNetworkRemove); err != nil {
		return err
	}
	operation.Phase = "completed"
	operation.Message = "Every Spark is back in independent mode."
	operation.Percent = 100
	operation.RollbackRequired = false
	cleared := State{Operation: operation}
	if err := save(cleared); err != nil {
		return err
	}
	if report != nil {
		report(operation.Phase, operation.Message, operation.Percent)
	}
	return nil
}

func recordCleanupResult(operation *Operation, host string, cleanupErr error) bool {
	if operation == nil {
		return false
	}
	for index := range operation.Nodes {
		if operation.Nodes[index].Host != host {
			continue
		}
		if cleanupErr != nil {
			operation.Nodes[index].Phase = "cleanup-required"
			operation.Nodes[index].Message = "Remote cleanup could not be verified."
			operation.Nodes[index].Error = cleanupErr.Error()
		} else {
			operation.Nodes[index].Phase = "cleaned"
			operation.Nodes[index].Message = "Worker, restricted identity, and private addresses were removed."
			operation.Nodes[index].Cleaned = true
		}
		return true
	}
	return false
}
