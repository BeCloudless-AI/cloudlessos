// Package privileged implements the narrow protocol used by the unprivileged
// Cloudless daemon to request explicitly reviewed host mutations.
package privileged

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSocketPath = "/run/cloudless/privileged.sock"
	maxRequestBytes   = 1024
)

type Action string

const (
	ActionPowerOff             Action = "power.shutdown"
	ActionReboot               Action = "power.restart"
	ActionSystemUpdateCheck    Action = "update.system.check"
	ActionSystemUpdateApply    Action = "update.system.apply"
	ActionNVIDIAUpdateCheck    Action = "update.nvidia.check"
	ActionNVIDIAUpdateApply    Action = "update.nvidia.apply"
	ActionTailscaleInstaller   Action = "tailscale.install"
	ActionTimezoneSet          Action = "timezone.set"
	ActionClusterNetworkApply  Action = "cluster.network.apply"
	ActionClusterNetworkRemove Action = "cluster.network.remove"
)

func (a Action) Valid() bool {
	switch a {
	case ActionPowerOff, ActionReboot,
		ActionSystemUpdateCheck, ActionSystemUpdateApply,
		ActionNVIDIAUpdateCheck, ActionNVIDIAUpdateApply,
		ActionTailscaleInstaller, ActionTimezoneSet,
		ActionClusterNetworkApply, ActionClusterNetworkRemove:
		return true
	default:
		return false
	}
}

type request struct {
	Action Action `json:"action"`
	Value  string `json:"value,omitempty"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type Client struct {
	SocketPath string
	Timeout    time.Duration
}

func (c Client) Do(ctx context.Context, action Action) error {
	return c.DoValue(ctx, action, "")
}

func (c Client) DoValue(ctx context.Context, action Action, value string) error {
	if !action.Valid() {
		return fmt.Errorf("unsupported privileged action %q", action)
	}
	path := c.SocketPath
	if path == "" {
		path = DefaultSocketPath
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("connect to privileged broker: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(request{Action: action, Value: value}); err != nil {
		return fmt.Errorf("send privileged request: %w", err)
	}
	var reply response
	if err := json.NewDecoder(io.LimitReader(conn, maxRequestBytes)).Decode(&reply); err != nil {
		return fmt.Errorf("read privileged response: %w", err)
	}
	if !reply.OK {
		if reply.Error == "" {
			reply.Error = "request rejected"
		}
		return errors.New(reply.Error)
	}
	return nil
}

func (c Client) ConfigureClusterNetwork(ctx context.Context, links []string, nodeIndex int) error {
	if len(links) != 2 {
		return errors.New("cluster networking requires exactly two interfaces")
	}
	value := strconv.Itoa(nodeIndex) + "|" + strings.TrimSpace(links[0]) + "|" + strings.TrimSpace(links[1])
	return c.DoValue(ctx, ActionClusterNetworkApply, value)
}

type Executor func(context.Context, Action, string) error
type Authorizer func(net.Conn) error

func Serve(ctx context.Context, listener net.Listener, authorize Authorizer, execute Executor) error {
	if listener == nil || authorize == nil || execute == nil {
		return errors.New("listener, authorizer, and executor are required")
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handleConnection(ctx, conn, authorize, execute)
	}
}

func handleConnection(ctx context.Context, conn net.Conn, authorize Authorizer, execute Executor) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	encoder := json.NewEncoder(conn)
	if err := authorize(conn); err != nil {
		_ = encoder.Encode(response{Error: "caller is not authorized"})
		return
	}
	reader := bufio.NewReader(io.LimitReader(conn, maxRequestBytes+1))
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		_ = encoder.Encode(response{Error: "invalid request"})
		return
	}
	if len(line) > maxRequestBytes {
		_ = encoder.Encode(response{Error: "request is too large"})
		return
	}
	var req request
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		_ = encoder.Encode(response{Error: "unsupported privileged action"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		_ = encoder.Encode(response{Error: "unsupported privileged action"})
		return
	}
	req.Value = strings.TrimSpace(req.Value)
	if validateRequest(req) != nil {
		_ = encoder.Encode(response{Error: "unsupported privileged action"})
		return
	}
	if err := execute(ctx, req.Action, req.Value); err != nil {
		_ = encoder.Encode(response{Error: "privileged action failed"})
		return
	}
	_ = encoder.Encode(response{OK: true})
}

func validateRequest(req request) error {
	req.Value = strings.TrimSpace(req.Value)
	switch req.Action {
	case ActionTimezoneSet:
		if req.Value == "" || len(req.Value) > 128 || strings.ContainsRune(req.Value, '\x00') {
			return errors.New("invalid timezone")
		}
		if _, err := time.LoadLocation(req.Value); err != nil {
			return errors.New("invalid timezone")
		}
		return nil
	case ActionClusterNetworkApply:
		_, _, _, err := ParseClusterNetworkValue(req.Value)
		return err
	}
	if req.Value != "" {
		return errors.New("action does not accept a value")
	}
	if !req.Action.Valid() {
		return errors.New("invalid action")
	}
	return nil
}

var clusterInterfaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

func ParseClusterNetworkValue(value string) (nodeIndex int, first, second string, err error) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return 0, "", "", errors.New("invalid cluster network request")
	}
	nodeIndex, err = strconv.Atoi(parts[0])
	if err != nil || nodeIndex < 1 || nodeIndex > 8 {
		return 0, "", "", errors.New("cluster node index must be between 1 and 8")
	}
	first, second = parts[1], parts[2]
	if !clusterInterfaceName.MatchString(first) || !clusterInterfaceName.MatchString(second) ||
		first == second || first == "lo" || second == "lo" {
		return 0, "", "", errors.New("invalid cluster network interface")
	}
	return nodeIndex, first, second, nil
}
