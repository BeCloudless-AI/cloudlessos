// Package desktop defines the narrow protocol between cloudlessd and the
// graphical session owned by the cloudless desktop user.
package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
)

const DefaultSocket = "/run/cloudless-desktop/agent.sock"

type Action string

const (
	ActionDisplayQuery Action = "display.query"
	ActionDisplayApply Action = "display.apply"
	ActionInputKey     Action = "input.key"
)

var outputNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

type Request struct {
	Action    Action `json:"action"`
	Output    string `json:"output,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	KeyAction string `json:"keyAction,omitempty"`
	Value     string `json:"value,omitempty"`
}

type Response struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Executor interface {
	QueryDisplay(context.Context) (string, error)
	ApplyDisplay(context.Context, string, int, int) error
	EmitKey(context.Context, string, string) error
}

type Authorizer func(net.Conn) error

func (r Request) Validate() error {
	switch r.Action {
	case ActionDisplayQuery:
		if r.Output != "" || r.Width != 0 || r.Height != 0 || r.KeyAction != "" || r.Value != "" {
			return errors.New("display query does not accept parameters")
		}
	case ActionDisplayApply:
		if !outputNamePattern.MatchString(r.Output) {
			return errors.New("display output is invalid")
		}
		if r.Width < 320 || r.Width > 16384 || r.Height < 200 || r.Height > 16384 {
			return errors.New("display dimensions are invalid")
		}
		if r.KeyAction != "" || r.Value != "" {
			return errors.New("display change contains unrelated parameters")
		}
	case ActionInputKey:
		if r.Output != "" || r.Width != 0 || r.Height != 0 {
			return errors.New("input request contains unrelated parameters")
		}
		if err := validateKey(r.KeyAction, r.Value); err != nil {
			return err
		}
	default:
		return errors.New("desktop action is not admitted")
	}
	return nil
}

func validateKey(action, value string) error {
	switch action {
	case "text":
		if len(value) != 1 || value[0] < 0x20 || value[0] > 0x7e {
			return errors.New("virtual keyboard text must be one printable ASCII character")
		}
	case "backspace", "left", "right", "enter", "shift-enter":
		if value != "" {
			return errors.New("virtual keyboard action does not accept text")
		}
	default:
		return errors.New("virtual keyboard action is not admitted")
	}
	return nil
}

type Client struct {
	Socket string
}

func NewClient() *Client {
	return &Client{Socket: DefaultSocket}
}

func (c *Client) QueryDisplay(ctx context.Context) (string, error) {
	response, err := c.call(ctx, Request{Action: ActionDisplayQuery})
	return response.Output, err
}

func (c *Client) ApplyDisplay(ctx context.Context, output string, width, height int) error {
	_, err := c.call(ctx, Request{Action: ActionDisplayApply, Output: output, Width: width, Height: height})
	return err
}

func (c *Client) EmitKey(ctx context.Context, action, value string) error {
	_, err := c.call(ctx, Request{Action: ActionInputKey, KeyAction: action, Value: value})
	return err
}

func (c *Client) call(ctx context.Context, request Request) (Response, error) {
	var response Response
	if err := request.Validate(); err != nil {
		return response, err
	}
	socket := c.Socket
	if strings.TrimSpace(socket) == "" {
		socket = DefaultSocket
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return response, fmt.Errorf("desktop session is unavailable: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	decoder := json.NewDecoder(io.LimitReader(connection, 2<<20))
	decoder.DisallowUnknownFields()
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		// Peer authorization happens immediately after accept. A rejected Unix
		// peer may receive the typed error and close before this write wins the
		// race, producing EPIPE even though a useful response is already
		// buffered for us. Prefer that response when it is available.
		if decodeErr := decoder.Decode(&response); decodeErr == nil && response.Error != "" {
			return response, errors.New(response.Error)
		}
		return response, err
	}
	if err := decoder.Decode(&response); err != nil {
		return response, err
	}
	if response.Error != "" {
		return response, errors.New(response.Error)
	}
	return response, nil
}

func Serve(ctx context.Context, listener net.Listener, authorize Authorizer, executor Executor) error {
	if listener == nil || authorize == nil || executor == nil {
		return errors.New("desktop agent is incompletely configured")
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConnection(ctx, connection, authorize, executor)
	}
}

func serveConnection(ctx context.Context, connection net.Conn, authorize Authorizer, executor Executor) {
	defer connection.Close()
	response := Response{}
	if err := authorize(connection); err != nil {
		response.Error = "desktop client is not authorized"
		_ = json.NewEncoder(connection).Encode(response)
		return
	}
	reader := bufio.NewReader(io.LimitReader(connection, 4096))
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		response.Error = "invalid desktop request"
		_ = json.NewEncoder(connection).Encode(response)
		return
	}
	if err := request.Validate(); err != nil {
		response.Error = err.Error()
		_ = json.NewEncoder(connection).Encode(response)
		return
	}
	var err error
	switch request.Action {
	case ActionDisplayQuery:
		response.Output, err = executor.QueryDisplay(ctx)
	case ActionDisplayApply:
		err = executor.ApplyDisplay(ctx, request.Output, request.Width, request.Height)
	case ActionInputKey:
		err = executor.EmitKey(ctx, request.KeyAction, request.Value)
	}
	if err != nil {
		response.Error = err.Error()
	}
	_ = json.NewEncoder(connection).Encode(response)
}
