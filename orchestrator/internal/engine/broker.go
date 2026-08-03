package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	DefaultBrokerSocket = "/run/cloudless/engine.sock"
	maxBrokerRequest    = 2 << 20
)

type brokerRequest struct {
	Action      string                   `json:"action"`
	Name        string                   `json:"name,omitempty"`
	Other       string                   `json:"other,omitempty"`
	Key         string                   `json:"key,omitempty"`
	Value       string                   `json:"value,omitempty"`
	Image       string                   `json:"image,omitempty"`
	ContextDir  string                   `json:"contextDir,omitempty"`
	Destination string                   `json:"destination,omitempty"`
	Args        []string                 `json:"args,omitempty"`
	Lines       int                      `json:"lines,omitempty"`
	Spec        RunSpec                  `json:"spec,omitempty"`
	Recipe      ReviewedRecipeDockerSpec `json:"recipe,omitempty"`
}

type brokerResponse struct {
	Done       bool              `json:"done,omitempty"`
	Event      string            `json:"event,omitempty"`
	Line       string            `json:"line,omitempty"`
	Error      string            `json:"error,omitempty"`
	Output     string            `json:"output,omitempty"`
	Bool       bool              `json:"bool,omitempty"`
	Strings    []string          `json:"strings,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Container  *Container        `json:"container,omitempty"`
	Containers []Container       `json:"containers,omitempty"`
	Image      ImageInfo         `json:"image,omitempty"`
	Images     []ImageDigestRef  `json:"images,omitempty"`
	IO         ContainerIO       `json:"io,omitempty"`
}

type BrokerClient struct {
	SocketPath  string
	DialTimeout time.Duration
}

func NewBrokerClient(socketPath string) *BrokerClient {
	return &BrokerClient{SocketPath: socketPath}
}

func (c *BrokerClient) call(ctx context.Context, req brokerRequest, onLine func(string)) (brokerResponse, error) {
	path := strings.TrimSpace(c.SocketPath)
	if path == "" {
		path = DefaultBrokerSocket
	}
	timeout := c.DialTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "unix", path)
	if err != nil {
		return brokerResponse{}, fmt.Errorf("connect to container broker: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return brokerResponse{}, fmt.Errorf("send container request: %w", err)
	}
	decoder := json.NewDecoder(bufio.NewReader(conn))
	for {
		var response brokerResponse
		if err := decoder.Decode(&response); err != nil {
			if ctx.Err() != nil {
				return brokerResponse{}, ctx.Err()
			}
			return brokerResponse{}, fmt.Errorf("read container response: %w", err)
		}
		if response.Event == "line" {
			if onLine != nil {
				onLine(response.Line)
			}
			continue
		}
		if !response.Done {
			return brokerResponse{}, errors.New("container broker returned an invalid response")
		}
		if response.Error != "" {
			return brokerResponse{}, errors.New(response.Error)
		}
		return response, nil
	}
}

func (c *BrokerClient) Available(ctx context.Context) error {
	_, err := c.call(ctx, brokerRequest{Action: "available"}, nil)
	return err
}
func (c *BrokerClient) EnsureNetwork(ctx context.Context, name string) error {
	_, err := c.call(ctx, brokerRequest{Action: "network.ensure", Name: name}, nil)
	return err
}
func (c *BrokerClient) EnsureVolume(ctx context.Context, name string) error {
	_, err := c.call(ctx, brokerRequest{Action: "volume.ensure", Name: name}, nil)
	return err
}
func (c *BrokerClient) VolumeMountpoint(ctx context.Context, name string) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "volume.mountpoint", Name: name}, nil)
	return response.Output, err
}
func (c *BrokerClient) ListVolumes(ctx context.Context) ([]string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "volume.list"}, nil)
	return response.Strings, err
}
func (c *BrokerClient) RemoveVolume(ctx context.Context, name string) error {
	_, err := c.call(ctx, brokerRequest{Action: "volume.remove", Name: name}, nil)
	return err
}
func (c *BrokerClient) ConnectNetwork(ctx context.Context, network, container string) error {
	_, err := c.call(ctx, brokerRequest{Action: "network.connect", Name: network, Other: container}, nil)
	return err
}
func (c *BrokerClient) HasAlias(ctx context.Context, container, alias string) (bool, error) {
	response, err := c.call(ctx, brokerRequest{Action: "network.has-alias", Name: container, Other: alias}, nil)
	return response.Bool, err
}
func (c *BrokerClient) ContainerEnvironment(ctx context.Context, container string) (map[string]string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.environment", Name: container}, nil)
	return response.Env, err
}
func (c *BrokerClient) HermesConfigValue(ctx context.Context, container, key string) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.hermes-config", Name: container, Key: key}, nil)
	return response.Output, err
}
func (c *BrokerClient) HasNVIDIARuntime(ctx context.Context) (bool, error) {
	response, err := c.call(ctx, brokerRequest{Action: "runtime.has-nvidia"}, nil)
	return response.Bool, err
}
func (c *BrokerClient) ContainerNamesByLabel(ctx context.Context, key, value string) ([]string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.by-label", Key: key, Value: value}, nil)
	return response.Strings, err
}
func (c *BrokerClient) ContainerNamesByAncestor(ctx context.Context, image string) ([]string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.by-ancestor", Image: image}, nil)
	return response.Strings, err
}
func (c *BrokerClient) LogsTail(ctx context.Context, container string, lines int) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.logs-tail", Name: container, Lines: lines}, nil)
	return response.Output, err
}
func (c *BrokerClient) Exec(ctx context.Context, container string, args ...string) error {
	_, err := c.call(ctx, brokerRequest{Action: "container.exec", Name: container, Args: args}, nil)
	return err
}
func (c *BrokerClient) Pull(ctx context.Context, image string) error {
	_, err := c.call(ctx, brokerRequest{Action: "image.pull", Image: image}, nil)
	return err
}
func (c *BrokerClient) PullStream(ctx context.Context, image string, onLine func(string)) error {
	_, err := c.call(ctx, brokerRequest{Action: "image.pull-stream", Image: image}, onLine)
	return err
}
func (c *BrokerClient) Build(ctx context.Context, image, contextDir string, onLine func(string)) error {
	_, err := c.call(ctx, brokerRequest{Action: "image.build", Image: image, ContextDir: contextDir}, onLine)
	return err
}
func (c *BrokerClient) RemoveImage(ctx context.Context, image string) error {
	_, err := c.call(ctx, brokerRequest{Action: "image.remove", Image: image}, nil)
	return err
}
func (c *BrokerClient) InspectImage(ctx context.Context, image string) (ImageInfo, error) {
	response, err := c.call(ctx, brokerRequest{Action: "image.inspect", Image: image}, nil)
	return response.Image, err
}
func (c *BrokerClient) TagImage(ctx context.Context, source, target string) error {
	_, err := c.call(ctx, brokerRequest{Action: "image.tag", Image: source, Other: target}, nil)
	return err
}
func (c *BrokerClient) RemoteImageManifest(ctx context.Context, image string) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "image.remote-manifest", Image: image}, nil)
	return response.Output, err
}
func (c *BrokerClient) RemoteImageConfig(ctx context.Context, image string) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "image.remote-config", Image: image}, nil)
	return response.Output, err
}
func (c *BrokerClient) ExportImage(ctx context.Context, image, destination string) error {
	_, err := c.call(ctx, brokerRequest{Action: "image.export", Image: image, Destination: destination}, nil)
	return err
}
func (c *BrokerClient) ListImageDigests(ctx context.Context) ([]ImageDigestRef, error) {
	response, err := c.call(ctx, brokerRequest{Action: "image.list-digests"}, nil)
	return response.Images, err
}
func (c *BrokerClient) Run(ctx context.Context, spec RunSpec) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.run", Spec: spec}, nil)
	return response.Output, err
}
func (c *BrokerClient) RunTransient(ctx context.Context, spec RunSpec) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.run-transient", Spec: spec}, nil)
	return response.Output, err
}
func (c *BrokerClient) Stop(ctx context.Context, name string) error {
	_, err := c.call(ctx, brokerRequest{Action: "container.stop", Name: name}, nil)
	return err
}
func (c *BrokerClient) Remove(ctx context.Context, name string) error {
	_, err := c.call(ctx, brokerRequest{Action: "container.remove", Name: name}, nil)
	return err
}
func (c *BrokerClient) List(ctx context.Context) ([]Container, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.list"}, nil)
	return response.Containers, err
}
func (c *BrokerClient) Find(ctx context.Context, name string) (*Container, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.find", Name: name}, nil)
	return response.Container, err
}
func (c *BrokerClient) Logs(ctx context.Context, name string) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.logs", Name: name}, nil)
	return response.Output, err
}
func (c *BrokerClient) ContainerIO(ctx context.Context, name string) (ContainerIO, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.io", Name: name}, nil)
	return response.IO, err
}
func (c *BrokerClient) ImageDigest(ctx context.Context, image string) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "image.digest", Image: image}, nil)
	return response.Output, err
}
func (c *BrokerClient) RemoteDigest(ctx context.Context, image string) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "image.remote-digest", Image: image}, nil)
	return response.Output, err
}
func (c *BrokerClient) ContainerImageDigest(ctx context.Context, name string) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "container.image-digest", Name: name}, nil)
	return response.Output, err
}

// ReviewedRecipeDocker submits one Docker invocation from the narrow
// source-scripts-v1 compatibility adapter. The broker must independently
// authenticate the durable operation and signed checkout before execution.
func (c *BrokerClient) ReviewedRecipeDocker(ctx context.Context, spec ReviewedRecipeDockerSpec) (string, error) {
	response, err := c.call(ctx, brokerRequest{Action: "recipe.docker", Recipe: spec}, nil)
	return response.Output, err
}

type BrokerAuthorizer func(net.Conn) error

type ReviewedRecipeDockerExecutor func(context.Context, ReviewedRecipeDockerSpec) (string, error)

func ServeBroker(ctx context.Context, listener net.Listener, authorize BrokerAuthorizer, runtime Engine) error {
	return ServeBrokerWithRecipeExecutor(ctx, listener, authorize, runtime, nil)
}

func ServeBrokerWithRecipeExecutor(
	ctx context.Context,
	listener net.Listener,
	authorize BrokerAuthorizer,
	runtime Engine,
	recipeExecutor ReviewedRecipeDockerExecutor,
) error {
	if listener == nil || authorize == nil || runtime == nil {
		return errors.New("listener, authorizer, and runtime are required")
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
		go handleBrokerConnection(ctx, conn, authorize, runtime, recipeExecutor)
	}
}

func handleBrokerConnection(
	ctx context.Context,
	conn net.Conn,
	authorize BrokerAuthorizer,
	runtime Engine,
	recipeExecutor ReviewedRecipeDockerExecutor,
) {
	defer conn.Close()
	encoder := json.NewEncoder(conn)
	if err := authorize(conn); err != nil {
		_ = encoder.Encode(brokerResponse{Done: true, Error: "caller is not authorized"})
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(io.LimitReader(conn, maxBrokerRequest+1))
	line, err := reader.ReadBytes('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil && !errors.Is(err, io.EOF) || len(line) > maxBrokerRequest {
		_ = encoder.Encode(brokerResponse{Done: true, Error: "invalid container request"})
		return
	}
	var request brokerRequest
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		_ = encoder.Encode(brokerResponse{Done: true, Error: "invalid container request"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		_ = encoder.Encode(brokerResponse{Done: true, Error: "invalid container request"})
		return
	}
	response, actionErr := executeBrokerAction(ctx, runtime, request, recipeExecutor, func(line string) {
		_ = encoder.Encode(brokerResponse{Event: "line", Line: line})
	})
	response.Done = true
	if actionErr != nil {
		response = brokerResponse{Done: true, Error: actionErr.Error()}
	}
	_ = encoder.Encode(response)
}

func executeBrokerAction(
	ctx context.Context,
	runtime Engine,
	request brokerRequest,
	recipeExecutor ReviewedRecipeDockerExecutor,
	onLine func(string),
) (brokerResponse, error) {
	var response brokerResponse
	var err error
	switch request.Action {
	case "available":
		err = runtime.Available(ctx)
	case "network.ensure":
		err = runtime.EnsureNetwork(ctx, request.Name)
	case "volume.ensure":
		err = runtime.EnsureVolume(ctx, request.Name)
	case "volume.mountpoint":
		response.Output, err = runtime.VolumeMountpoint(ctx, request.Name)
	case "volume.list":
		response.Strings, err = runtime.ListVolumes(ctx)
	case "volume.remove":
		err = runtime.RemoveVolume(ctx, request.Name)
	case "network.connect":
		err = runtime.ConnectNetwork(ctx, request.Name, request.Other)
	case "network.has-alias":
		response.Bool, err = runtime.HasAlias(ctx, request.Name, request.Other)
	case "container.environment":
		response.Env, err = runtime.ContainerEnvironment(ctx, request.Name)
	case "container.hermes-config":
		response.Output, err = runtime.HermesConfigValue(ctx, request.Name, request.Key)
	case "runtime.has-nvidia":
		response.Bool, err = runtime.HasNVIDIARuntime(ctx)
	case "container.by-label":
		response.Strings, err = runtime.ContainerNamesByLabel(ctx, request.Key, request.Value)
	case "container.by-ancestor":
		response.Strings, err = runtime.ContainerNamesByAncestor(ctx, request.Image)
	case "container.logs-tail":
		response.Output, err = runtime.LogsTail(ctx, request.Name, request.Lines)
	case "container.exec":
		err = runtime.Exec(ctx, request.Name, request.Args...)
	case "image.pull":
		err = runtime.Pull(ctx, request.Image)
	case "image.pull-stream":
		err = runtime.PullStream(ctx, request.Image, onLine)
	case "image.build":
		err = runtime.Build(ctx, request.Image, request.ContextDir, onLine)
	case "image.remove":
		err = runtime.RemoveImage(ctx, request.Image)
	case "image.inspect":
		response.Image, err = runtime.InspectImage(ctx, request.Image)
	case "image.tag":
		err = runtime.TagImage(ctx, request.Image, request.Other)
	case "image.remote-manifest":
		response.Output, err = runtime.RemoteImageManifest(ctx, request.Image)
	case "image.remote-config":
		response.Output, err = runtime.RemoteImageConfig(ctx, request.Image)
	case "image.export":
		err = runtime.ExportImage(ctx, request.Image, request.Destination)
	case "image.list-digests":
		response.Images, err = runtime.ListImageDigests(ctx)
	case "container.run":
		response.Output, err = runtime.Run(ctx, request.Spec)
	case "container.run-transient":
		response.Output, err = runtime.RunTransient(ctx, request.Spec)
	case "container.stop":
		err = runtime.Stop(ctx, request.Name)
	case "container.remove":
		err = runtime.Remove(ctx, request.Name)
	case "container.list":
		response.Containers, err = runtime.List(ctx)
	case "container.find":
		response.Container, err = runtime.Find(ctx, request.Name)
	case "container.logs":
		response.Output, err = runtime.Logs(ctx, request.Name)
	case "container.io":
		reader, ok := runtime.(ContainerIOReader)
		if !ok {
			err = errors.New("container IO telemetry is unavailable")
		} else {
			response.IO, err = reader.ContainerIO(ctx, request.Name)
		}
	case "image.digest":
		response.Output, err = runtime.ImageDigest(ctx, request.Image)
	case "image.remote-digest":
		response.Output, err = runtime.RemoteDigest(ctx, request.Image)
	case "container.image-digest":
		response.Output, err = runtime.ContainerImageDigest(ctx, request.Name)
	case "recipe.docker":
		if recipeExecutor == nil {
			err = errors.New("reviewed recipe Docker adapter is unavailable")
		} else {
			response.Output, err = recipeExecutor(ctx, request.Recipe)
		}
	default:
		err = errors.New("unsupported container action")
	}
	return response, err
}
