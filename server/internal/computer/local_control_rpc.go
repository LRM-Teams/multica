package computer

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const localControlMaxFrame = 1 << 20

const (
	LocalControlRestartServiceOperation       = "service:restart"
	LocalControlUpgradeStartOperation         = "upgrade:start"
	LocalControlUpgradeStatusOperation        = "upgrade:status"
	LocalControlUpgradeCancelOperation        = "upgrade:cancel"
	LocalControlServiceStatusOperation        = "service:status"
	LocalControlMachineAttestationOperation   = "machine-attestation"
	LocalControlWorkspaceEnvironmentOperation = "workspace:environment"
	LocalControlWorkspaceCapacityOperation    = "workspace:capacity"
	LocalControlWorkspaceDiagnosticsOperation = "workspace:diagnostics"
	LocalControlComputerControlOperation      = "computer:control"
	LocalControlRunnerReadyOperation          = "runner:ready"
	LocalControlRunnerDrainOperation          = "runner:drain"
	LocalControlRunnerReleaseOperation        = "runner:release"
	LocalControlWorkDigestOperation           = "workspace:work-digest"
	LocalControlWorkJournalOperation          = "workspace:work-journal"
)

type localControlOperationSpec struct {
	Name string
}

var localControlOperationSpecs = []localControlOperationSpec{
	{Name: LocalControlRestartServiceOperation}, {Name: LocalControlUpgradeStartOperation},
	{Name: LocalControlUpgradeStatusOperation}, {Name: LocalControlUpgradeCancelOperation}, {Name: LocalControlServiceStatusOperation}, {Name: LocalControlMachineAttestationOperation},
	{Name: "service:start"}, {Name: "service:stop"}, {Name: "service:diagnostics"},
	{Name: "workspace:list"}, {Name: "workspace:status"}, {Name: "workspace:start"},
	{Name: "workspace:stop"}, {Name: "workspace:restart"}, {Name: "workspace:attach"},
	{Name: "workspace:detach"}, {Name: LocalControlWorkspaceEnvironmentOperation}, {Name: LocalControlWorkspaceCapacityOperation},
	{Name: LocalControlWorkspaceDiagnosticsOperation}, {Name: LocalControlComputerControlOperation},
	{Name: "runner:start"}, {Name: "runner:stop"}, {Name: "runner:restart"},
	{Name: LocalControlRunnerDrainOperation}, {Name: LocalControlRunnerReleaseOperation}, {Name: LocalControlRunnerReadyOperation},
	{Name: LocalControlWorkDigestOperation}, {Name: LocalControlWorkJournalOperation},
}

func localControlOperationSpecFor(name string) (localControlOperationSpec, bool) {
	for _, spec := range localControlOperationSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return localControlOperationSpec{}, false
}

func localControlOperationSpecForMust(name string) localControlOperationSpec {
	spec, ok := localControlOperationSpecFor(name)
	if !ok {
		panic("unknown local control operation: " + name)
	}
	return spec
}

type localControlRPCMessage struct {
	Operation    string            `json:"operation,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Args         []byte            `json:"args,omitempty"`
	OK           bool              `json:"ok"`
	Result       []byte            `json:"result,omitempty"`
	ErrorCode    string            `json:"errorCode,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
}

type localControlClient struct {
	endpoint string
	timeout  time.Duration
}

// LocalControlHandler is the typed boundary for Computer/Daemon control.
// Implementations receive operation arguments and return a JSON-serializable
// result; HTTP request and response types do not cross this boundary.
type LocalControlHandler func(context.Context, map[string]string, json.RawMessage) (any, error)

type LocalControlRegistry struct {
	handlers map[string]LocalControlHandler
}

func NewLocalControlRegistry() *LocalControlRegistry {
	return &LocalControlRegistry{handlers: make(map[string]LocalControlHandler)}
}

func (registry *LocalControlRegistry) Register(operation string, handler LocalControlHandler) error {
	operation = strings.TrimSpace(operation)
	if operation == "" || handler == nil {
		return errors.New("local control operation and handler are required")
	}
	if _, ok := localControlOperationSpecFor(operation); !ok {
		return fmt.Errorf("unknown local control operation %q", operation)
	}
	if _, exists := registry.handlers[operation]; exists {
		return fmt.Errorf("local control operation %q is already registered", operation)
	}
	registry.handlers[operation] = handler
	return nil
}

func (registry *LocalControlRegistry) handler(operation string) (LocalControlHandler, bool) {
	if registry == nil {
		return nil, false
	}
	handler, ok := registry.handlers[operation]
	return handler, ok
}

func (client *localControlClient) Call(ctx context.Context, operation string, headers map[string]string, args, result any) error {
	if _, ok := localControlOperationSpecFor(operation); !ok {
		return fmt.Errorf("unknown local control operation %q", operation)
	}
	return client.call(ctx, operation, headers, args, result)
}

func (client *localControlClient) call(ctx context.Context, operation string, headers map[string]string, args, result any) error {
	body, err := json.Marshal(args)
	if err != nil {
		return err
	}
	if args == nil {
		body = nil
	}
	if len(body) >= localControlMaxFrame {
		return errors.New("local control request is too large")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if client.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, client.timeout)
		defer cancel()
	}
	conn, err := dialLocalControl(ctx, client.endpoint)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := writeLocalControlFrame(conn, localControlRPCMessage{Operation: operation, Headers: headers, Args: body}); err != nil {
		return err
	}
	var response localControlRPCMessage
	if err := readLocalControlFrame(conn, &response); err != nil {
		return err
	}
	if !response.OK {
		if response.ErrorCode != "" {
			return fmt.Errorf("local control operation %s failed (%s): %s", operation, response.ErrorCode, response.ErrorMessage)
		}
		return fmt.Errorf("local control operation %s failed: %s", operation, response.ErrorMessage)
	}
	if result != nil && len(response.Result) > 0 {
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode local control operation %s: %w", operation, err)
		}
	}
	return nil
}

func callLocalJSON(ctx context.Context, endpoint, operation string, timeout time.Duration, headers map[string]string, args, result any) error {
	client, err := localControlClientFor(endpoint, timeout)
	if err != nil {
		return err
	}
	return client.Call(ctx, operation, headers, args, result)
}

func callLocalJSONWithTimeout(ctx context.Context, endpoint, operation string, timeout time.Duration, headers map[string]string, args, result any) error {
	client, err := localControlClientFor(endpoint, timeout)
	if err != nil {
		return err
	}
	return client.Call(ctx, operation, headers, args, result)
}

// ServeLocalControlRPC serves the production operation registry. Unlike the
// legacy HTTP adapter, this path never constructs an HTTP request.
func ServeLocalControlRPC(ctx context.Context, listener net.Listener, registry *LocalControlRegistry) error {
	if listener == nil || registry == nil {
		return errors.New("local control listener and registry are required")
	}
	if ctx == nil {
		ctx = context.Background()
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
		go serveLocalControlRPCConnection(ctx, conn, registry)
	}
}

func serveLocalControlRPCConnection(ctx context.Context, conn net.Conn, registry *LocalControlRegistry) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		var request localControlRPCMessage
		if err := readLocalControlFrame(reader, &request); err != nil {
			return
		}
		handler, ok := registry.handler(request.Operation)
		if !ok {
			_ = writeLocalControlFrame(conn, localControlRPCMessage{
				Operation: request.Operation, ErrorCode: "unknown_operation", ErrorMessage: "unknown operation",
			})
			continue
		}
		result, err := handler(ctx, request.Headers, request.Args)
		response := localControlRPCMessage{Operation: request.Operation, OK: err == nil}
		if err != nil {
			response.ErrorCode, response.ErrorMessage = localControlError(err)
		} else if result != nil {
			response.Result, err = json.Marshal(result)
			if err != nil {
				response.OK = false
				response.ErrorCode, response.ErrorMessage = "encode_result", err.Error()
			}
		}
		if err := writeLocalControlFrame(conn, response); err != nil {
			return
		}
	}
}

type localControlCodedError interface {
	ControlCode() string
}

type localControlErrorWithCode struct {
	code string
	err  error
}

func (err localControlErrorWithCode) Error() string       { return err.err.Error() }
func (err localControlErrorWithCode) Unwrap() error       { return err.err }
func (err localControlErrorWithCode) ControlCode() string { return err.code }

func withLocalControlCode(code string, err error) error {
	if err == nil {
		return nil
	}
	return localControlErrorWithCode{code: code, err: err}
}

func localControlError(err error) (string, string) {
	code := "operation_failed"
	var coded localControlCodedError
	if errors.As(err, &coded) && strings.TrimSpace(coded.ControlCode()) != "" {
		code = coded.ControlCode()
	}
	return code, err.Error()
}

func writeLocalControlFrame(writer io.Writer, message localControlRPCMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(data) > localControlMaxFrame {
		return errors.New("local control frame is too large")
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	if _, err := writer.Write(length[:]); err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

func readLocalControlFrame(reader io.Reader, target any) error {
	var length [4]byte
	if _, err := io.ReadFull(reader, length[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 || size > localControlMaxFrame {
		return errors.New("local control frame length is invalid")
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
