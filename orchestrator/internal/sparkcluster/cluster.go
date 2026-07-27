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
    docker volume create cloudless-hf >/dev/null
    docker run -d --name cloudless-cluster-worker --restart unless-stopped --network host --ipc host --device nvidia.com/gpu=all --ulimit memlock=-1 --ulimit stack=67108864 -v cloudless-hf:/root/.cache/huggingface -e VLLM_HOST_IP="$worker_ip" -e UCX_NET_DEVICES="$iface" -e NCCL_SOCKET_IFNAME="$iface" -e GLOO_SOCKET_IFNAME="$iface" -e TP_SOCKET_IFNAME="$iface" -e RAY_memory_monitor_refresh_ms=0 --entrypoint /bin/bash "$image" -lc "pip install -q --root-user-action=ignore 'ray[default]>=2.9' && exec ray start --block --address=$head_ip:6379 --node-ip-address=$worker_ip --num-gpus=1"
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
    mount=$(docker volume inspect cloudless-hf --format '{{.Mountpoint}}' 2>/dev/null || true)
    path=$mount/hub/$cache_name
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
  upgrade)
    payload=$(printf %s "$2" | base64 -d)
    tmp=$(mktemp)
    printf %s "$payload" >"$tmp"
    chmod 755 "$tmp"
    mv -f "$tmp" /usr/lib/cloudless/cloudless-cluster-worker
    ;;
  *)
    echo "usage: cloudless-cluster-worker start IMAGE_B64 HEAD_IP_B64 WORKER_IP_B64 IFACE_B64 | stop | status | model-progress MODEL_B64 | upgrade SCRIPT_B64" >&2
    exit 2
    ;;
esac
`
)

var (
	hostPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	userPattern    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	ifacePattern   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	commandContext = exec.CommandContext
	now            = time.Now
	mutationMu     sync.Mutex
	telemetryMu    sync.Mutex
	telemetryCache []PeerTelemetry
	telemetryErr   error
	telemetryAt    time.Time
)

type PeerTelemetry struct {
	Connected bool           `json:"connected"`
	Reachable bool           `json:"reachable"`
	Name      string         `json:"name,omitempty"`
	Host      string         `json:"host,omitempty"`
	GPUs      []hardware.GPU `json:"gpus,omitempty"`
	Error     string         `json:"error,omitempty"`
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

// Node is one remote Spark enrolled into the coordinator's cluster. Passwords
// are deliberately never persisted; the installed, restricted SSH identity is
// used for normal operation.
type Node struct {
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	Username    string   `json:"username"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Links       []string `json:"links,omitempty"`
	IPs         []string `json:"ips,omitempty"`
	WorkerReady bool     `json:"workerReady"`
	Healthy     bool     `json:"healthy"`
}

type State struct {
	Configured  bool      `json:"configured"`
	Healthy     bool      `json:"healthy"`
	WorkerReady bool      `json:"workerReady"`
	Role        string    `json:"role,omitempty"`
	LocalName   string    `json:"localName,omitempty"`
	PeerName    string    `json:"peerName,omitempty"`
	PeerHost    string    `json:"peerHost,omitempty"`
	Username    string    `json:"username,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	LocalLinks  []string  `json:"localLinks,omitempty"`
	PeerLinks   []string  `json:"peerLinks,omitempty"`
	LocalIPs    []string  `json:"localIps,omitempty"`
	PeerIPs     []string  `json:"peerIps,omitempty"`
	Nodes       []Node    `json:"nodes,omitempty"`
	NodeCount   int       `json:"nodeCount,omitempty"`
	Topology    string    `json:"topology,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
	Checks      []Check   `json:"checks,omitempty"`
	Error       string    `json:"error,omitempty"`
}

func normalizeState(state State) State {
	if len(state.Nodes) == 0 && strings.TrimSpace(state.PeerHost) != "" {
		state.Nodes = []Node{{
			Name: state.PeerName, Host: state.PeerHost, Username: state.Username,
			Fingerprint: state.Fingerprint, Links: state.PeerLinks, IPs: state.PeerIPs,
			WorkerReady: state.WorkerReady,
		}}
	}
	state.NodeCount = 1 + len(state.Nodes)
	if state.NodeCount < 2 && !state.Configured {
		state.NodeCount = 1
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
		// Previously this returned `sshpass -e -o ...`, so sshpass parsed the
		// SSH options itself and every password login failed before SSH ran.
		return "sshpass", append([]string{"-e", "ssh"}, args...)
	}
	args = append([]string{"-i", keyPath, "-o", "IdentitiesOnly=yes"}, args...)
	return "ssh", args
}

func remote(ctx context.Context, host, username, password, command string, stdin []byte) (string, error) {
	program, args := sshCommand(host, username, password != "")
	args = append(args, command)
	if password != "" {
		return run(ctx, []string{"SSHPASS=" + password}, stdin, program, args...)
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

func applyLocal(ctx context.Context, content string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	tmp := configPath + ".new"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, configPath); err != nil {
		return err
	}
	if _, err := run(ctx, nil, nil, "netplan", "generate"); err != nil {
		rollbackLocal()
		return err
	}
	if _, err := run(ctx, nil, nil, "netplan", "apply"); err != nil {
		rollbackLocal()
		return err
	}
	return nil
}

func rollbackLocal() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = os.Remove(configPath)
	_, _ = run(ctx, nil, nil, "netplan", "generate")
	_, _ = run(ctx, nil, nil, "netplan", "apply")
	removeLocalClusterAddresses(ctx)
}

func isClusterAddress(cidr string) bool {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	v4 := ip.To4()
	return v4 != nil && v4[0] == 10 && v4[1] == 100 && (v4[2] == 0 || v4[2] == 1)
}

func removeLocalClusterAddresses(ctx context.Context) {
	output, err := run(ctx, nil, nil, "ip", "-o", "-4", "addr", "show")
	if err != nil {
		return
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != "inet" || !isClusterAddress(fields[3]) {
			continue
		}
		_, _ = run(ctx, nil, nil, "ip", "addr", "del", fields[3], "dev", fields[1])
	}
}

func rollbackRemote(host, username, password, publicKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	encodedKey := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(publicKey)))
	command := fmt.Sprintf(`sudo -S -p '' sh -c 'set -eu; if [ -x %s ]; then %s stop; fi; rm -f %s %s %s; home=$(getent passwd "$1" | cut -d: -f6); if [ -n "$home" ] && [ -f "$home/.ssh/authorized_keys" ] && [ -n "$2" ]; then key=$(printf %%s "$2" | base64 -d); tmp=$(mktemp); grep -vxF "$key" "$home/.ssh/authorized_keys" >"$tmp" || true; install -m 600 -o "$1" "$tmp" "$home/.ssh/authorized_keys"; rm -f "$tmp"; fi; netplan generate; netplan apply; %s' sh %s %s`, workerPath, workerPath, configPath, workerPath, sudoersPath, remoteAddressCleanup, username, encodedKey)
	_, _ = remote(ctx, host, username, password, command, []byte(password+"\n"))
}

func Create(ctx context.Context, request CreateRequest) (State, error) {
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
	publicKey, err := ensureKey(ctx)
	if err != nil {
		return State{}, fmt.Errorf("create cluster identity: %w", err)
	}
	remoteConfig := base64.StdEncoding.EncodeToString([]byte(netplan(preflight.PeerLinks, preflight.NodeIndex)))
	remoteKey := base64.StdEncoding.EncodeToString([]byte(publicKey + "\n"))
	remoteWorker := base64.StdEncoding.EncodeToString([]byte(workerScript))
	remoteSudoers := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s ALL=(root) NOPASSWD: %s *\n", request.Username, workerPath)))
	remoteCommand := fmt.Sprintf(`sudo -S -p '' sh -c 'set -eu; printf %%s "$1" | base64 -d > %s; chmod 600 %s; home=$(getent passwd "$2" | cut -d: -f6); test -n "$home"; install -d -m 700 -o "$2" "$home/.ssh"; touch "$home/.ssh/authorized_keys"; chown "$2" "$home/.ssh/authorized_keys"; chmod 600 "$home/.ssh/authorized_keys"; key=$(printf %%s "$3" | base64 -d); grep -qxF "$key" "$home/.ssh/authorized_keys" || printf "%%s\\n" "$key" >> "$home/.ssh/authorized_keys"; install -d -m 755 /usr/lib/cloudless; printf %%s "$4" | base64 -d > %s; chown root:root %s; chmod 755 %s; printf %%s "$5" | base64 -d > %s; chown root:root %s; chmod 440 %s; visudo -cf %s >/dev/null; netplan generate; netplan apply' sh %s %s %s %s %s`, configPath, configPath, workerPath, workerPath, workerPath, sudoersPath, sudoersPath, sudoersPath, sudoersPath, remoteConfig, request.Username, remoteKey, remoteWorker, remoteSudoers)
	if _, err := remote(ctx, request.Host, request.Username, request.Password, remoteCommand, []byte(request.Password+"\n")); err != nil {
		rollbackRemote(request.Host, request.Username, request.Password, publicKey)
		return State{}, fmt.Errorf("configure the other Spark: %w", err)
	}
	if !existing.Configured {
		if err := applyLocal(ctx, netplan(preflight.LocalLinks, 1)); err != nil {
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
	return waitForHealthy(ctx, 15*time.Second)
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
	return normalizeState(state), nil
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

func nodeCountForState(state State) int { return max(1, normalizeState(state).NodeCount) }

func Status(ctx context.Context) (State, error) {
	state, err := load()
	if err != nil || !state.Configured {
		return state, err
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
			checks := make([]Check, 0, max(1, len(node.IPs)))
			if len(node.IPs) != 2 {
				checks = append(checks, Check{ID: "addresses-" + node.Host, Label: node.Name + " fabric addresses", OK: false, Details: "Reconnect this Spark to assign both private fabric paths."})
			}
			for _, ip := range node.IPs {
				// Netplan can return before address discovery on the new fabric has
				// completely settled. A short burst prevents that normal warm-up from
				// being presented as packet loss immediately after cluster creation.
				_, pingErr := run(ctx, nil, nil, "ping", fabricPingArguments(ip)...)
				check := Check{ID: "ping-" + ip, Label: node.Name + " · ConnectX-7 path " + ip, OK: pingErr == nil}
				if pingErr != nil {
					check.Details = pingErr.Error()
				}
				checks = append(checks, check)
			}
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
	state = normalizeState(state)
	return state, nil
}

func fabricPingArguments(ip string) []string {
	return []string{"-c", "3", "-i", "0.25", "-W", "1", ip}
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
			command := `nvidia-smi --query-gpu=index,name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw,power.limit,driver_version --format=csv,noheader,nounits; printf '\n__CLOUDLESS_MEM__\n'; awk '/MemTotal:/{t=$2}/MemAvailable:/{a=$2}END{printf "%d %d\n",t/1024,a/1024}' /proc/meminfo`
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
			gpus := hardware.ParseGPUsCSV(strings.TrimSpace(gpuText))
			memFields := strings.Fields(memText)
			if len(memFields) >= 2 {
				totalMB, _ := strconv.Atoi(memFields[0])
				availableMB, _ := strconv.Atoi(memFields[1])
				hardware.ApplyUnifiedMemory(gpus, totalMB, availableMB)
			}
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
	if !state.Configured || !state.Healthy {
		return errors.New("the Spark cluster connection is not healthy")
	}
	if !state.WorkerReady {
		return errors.New("reconnect any legacy Spark workers once to enable distributed models")
	}
	if strings.TrimSpace(image) == "" || strings.TrimSpace(model) == "" {
		return errors.New("worker image and model are required")
	}
	headIP := "10.100.0.1"
	if len(state.LocalIPs) > 0 {
		headIP = state.LocalIPs[0]
	}
	image64 := base64.StdEncoding.EncodeToString([]byte(image))
	upgrade64 := base64.StdEncoding.EncodeToString([]byte(workerScript))
	enc := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	errs := make(chan error, len(state.Nodes))
	var wg sync.WaitGroup
	for _, node := range state.Nodes {
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
	encoded := base64.StdEncoding.EncodeToString([]byte(model))
	command := fmt.Sprintf("sudo -n %s model-progress %s", workerPath, encoded)
	results := make([]ModelProgress, len(state.Nodes))
	errs := make(chan error, len(state.Nodes))
	var wg sync.WaitGroup
	for i, node := range state.Nodes {
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
	nodes := max(2, nodeCountForState(state))
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
	return engine.RunSpec{
		Name: "cloudless-cluster-engine-proxy", Image: "alpine/socat:latest",
		Network: "cloudless", NetworkAlias: "cloudless-ai",
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		Args:       []string{"tcp-listen:8000,fork,reuseaddr", "tcp-connect:host.docker.internal:8000"},
	}
}

func Disconnect(ctx context.Context, passwords map[string]string) error {
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
	errs := make(chan error, len(removals))
	var wg sync.WaitGroup
	for _, removal := range removals {
		wg.Add(1)
		go func(removal remoteRemoval) {
			defer wg.Done()
			node, password := removal.node, removal.password
			remoteCommand := fmt.Sprintf(`sudo -S -p '' sh -c 'set -eu; if [ -x %s ]; then %s stop; fi; rm -f %s %s %s; home=$(getent passwd "$1" | cut -d: -f6); if [ -n "$home" ] && [ -f "$home/.ssh/authorized_keys" ] && [ -n "$2" ]; then key=$(printf %%s "$2" | base64 -d); tmp=$(mktemp); grep -vxF "$key" "$home/.ssh/authorized_keys" >"$tmp" || true; install -m 600 -o "$1" "$tmp" "$home/.ssh/authorized_keys"; rm -f "$tmp"; fi; netplan generate; netplan apply; %s' sh %s %s`, workerPath, workerPath, configPath, workerPath, sudoersPath, remoteAddressCleanup, node.Username, encodedKey)
			if _, err := remote(ctx, node.Host, node.Username, password, remoteCommand, []byte(password+"\n")); err != nil {
				errs <- fmt.Errorf("disconnect %s: %w", node.Name, err)
			}
		}(removal)
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
	if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := run(ctx, nil, nil, "netplan", "generate"); err != nil {
		return err
	}
	if _, err := run(ctx, nil, nil, "netplan", "apply"); err != nil {
		return err
	}
	removeLocalClusterAddresses(ctx)
	return os.Remove(statePath)
}
