package computer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

const localControlMaxFrame = 1 << 20

type localControlOperationSpec struct {
	Name   string
	Method string
	Path   string
}

var localControlOperationSpecs = []localControlOperationSpec{
	{Name: "machine-attestation", Method: http.MethodGet, Path: MachineAttestationPath},
	{Name: "restart-service", Method: http.MethodPost, Path: "/shutdown"},
	{Name: "upgrade-start", Method: http.MethodPost, Path: "/machine-upgrades"},
	{Name: "upgrade-status", Method: http.MethodGet, Path: "/machine-upgrades"},
	{Name: "upgrade-cancel", Method: http.MethodPost, Path: "/machine-upgrades/cancel"},
	{Name: "service-status", Method: http.MethodGet, Path: "/health"},
	{Name: "service-start", Method: http.MethodPost, Path: "/service-start"},
	{Name: "service-stop", Method: http.MethodPost, Path: "/shutdown"},
	{Name: "service-diagnostics", Method: http.MethodGet, Path: "/diagnostics"},
	{Name: "workspace-list", Method: http.MethodGet, Path: "/workspace-list"},
	{Name: "workspace-status", Method: http.MethodGet, Path: "/health"},
	{Name: "workspace-start", Method: http.MethodPost, Path: "/workspace-start"},
	{Name: "workspace-stop", Method: http.MethodPost, Path: "/workspace-stop"},
	{Name: "workspace-restart", Method: http.MethodPost, Path: "/workspace-restart"},
	{Name: "workspace-attach", Method: http.MethodPost, Path: "/workspace-attach"},
	{Name: "workspace-detach", Method: http.MethodPost, Path: "/workspace-detach"},
	{Name: "workspace-environment", Method: http.MethodPost, Path: "/environment-switch/prepare"},
	{Name: "workspace-capacity", Method: http.MethodGet, Path: bindingChildCapacityPath},
	{Name: "workspace-diagnostics", Method: http.MethodGet, Path: bindingChildDiagnosticPath},
	{Name: "runner-attestation", Method: http.MethodGet, Path: "/runner-attestation"},
	{Name: "runner-status", Method: http.MethodGet, Path: "/runner-status"},
	{Name: "runner-start", Method: http.MethodPost, Path: "/runner-start"},
	{Name: "runner-stop", Method: http.MethodPost, Path: "/runner-stop"},
	{Name: "runner-restart", Method: http.MethodPost, Path: "/runner-restart"},
	{Name: "runner-drain", Method: http.MethodPost, Path: "/runner-drain"},
	{Name: "runner-release", Method: http.MethodPost, Path: "/runner-release"},
	{Name: "runner-ready", Method: http.MethodPost, Path: "/runner-ready"},
}

func localControlOperationSpecFor(name string) (localControlOperationSpec, bool) {
	for _, spec := range localControlOperationSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return localControlOperationSpec{}, false
}

func localControlOperationForPath(path string) string {
	for _, spec := range localControlOperationSpecs {
		if spec.Path == path {
			return spec.Name
		}
	}
	switch path {
	case bindingChildCapacityPath:
		return "workspace-capacity"
	case bindingChildDiagnosticPath:
		return "workspace-diagnostics"
	case bindingChildLifecycleDiagnosticPath:
		return "runner-status"
	case bindingChildMachineActionsPath:
		return "runner-attestation"
	case bindingChildPrepareUpgradePath:
		return "runner-drain"
	case BindingReleaseMachineUpgradePath:
		return "runner-release"
	case bindingChildComputerUpgradePath:
		return "upgrade-start"
	case BindingComputerUpgradeEventPath:
		return "upgrade-status"
	case BindingPrepareEnvironmentSwitchPath, BindingReleaseEnvironmentSwitchPath:
		return "workspace-environment"
	case BindingReregisterRuntimePath:
		return "runner-ready"
	case bindingChildRuntimeSetPath:
		return "runner-ready"
	default:
		return ""
	}
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
	ErrorCode    string            `json:"error_code,omitempty"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

type localControlClient struct {
	endpoint   string
	timeout    time.Duration
	httpClient *http.Client
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
	spec, ok := localControlOperationSpecFor(operation)
	if !ok {
		return fmt.Errorf("unknown local control operation %q", operation)
	}
	return client.callAt(ctx, operation, spec.Path, headers, args, result)
}

func (client *localControlClient) callAt(ctx context.Context, operation, testPath string, headers map[string]string, args, result any) error {
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
	if client.httpClient != nil {
		spec, _ := localControlOperationSpecFor(operation)
		method := spec.Method
		if testPath != spec.Path {
			method = http.MethodPost
		}
		request, err := http.NewRequestWithContext(ctx, method, client.endpoint+testPath, bytes.NewReader(body))
		if err != nil {
			return err
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := client.httpClient.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, localControlMaxFrame))
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("local control operation %s returned %s: %s", operation, response.Status, strings.TrimSpace(string(responseBody)))
		}
		if result != nil && response.StatusCode != http.StatusNoContent && len(responseBody) > 0 {
			return json.Unmarshal(responseBody, result)
		}
		return nil
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
	client, _, err := localControlClientFor(endpoint, timeout)
	if err != nil {
		return err
	}
	return client.Call(ctx, operation, headers, args, result)
}

func callLocalJSONAt(ctx context.Context, endpoint, operation, testPath string, timeout time.Duration, headers map[string]string, args, result any) error {
	client, _, err := localControlClientFor(endpoint, timeout)
	if err != nil {
		return err
	}
	return client.callAt(ctx, operation, testPath, headers, args, result)
}

func (client *localControlClient) Do(request *http.Request) (*http.Response, error) {
	return nil, errors.New("local control clients do not accept HTTP requests; use Call")
}

func ServeLocalControl(ctx context.Context, listener net.Listener, handler http.Handler) error {
	if listener == nil || handler == nil {
		return errors.New("local control listener and handler are required")
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
		go serveLocalControlConnection(conn, handler)
	}
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

func serveLocalControlConnection(conn net.Conn, handler http.Handler) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		var request localControlRPCMessage
		if err := readLocalControlFrame(reader, &request); err != nil {
			return
		}
		if request.Operation != "" {
			spec, ok := localControlOperationSpecFor(request.Operation)
			if !ok {
				_ = writeLocalControlFrame(conn, localControlRPCMessage{Operation: request.Operation, ErrorCode: "unknown_operation", ErrorMessage: "unknown operation"})
				return
			}
			requestMethod, requestPath := spec.Method, spec.Path
			httpRequest := httptest.NewRequest(requestMethod, "http://local-control"+requestPath, bytes.NewReader(request.Args))
			for key, value := range request.Headers {
				httpRequest.Header.Set(key, value)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httpRequest)
			result := recorder.Result()
			responseHeaders := make(map[string]string, len(result.Header))
			for key, values := range result.Header {
				if len(values) > 0 {
					responseHeaders[key] = values[0]
				}
			}
			body, err := io.ReadAll(io.LimitReader(result.Body, localControlMaxFrame))
			result.Body.Close()
			if err != nil || len(body) >= localControlMaxFrame {
				return
			}
			response := localControlRPCMessage{Operation: request.Operation, Headers: responseHeaders, Result: body, OK: result.StatusCode >= 200 && result.StatusCode < 300}
			if !response.OK {
				response.ErrorCode = fmt.Sprintf("http_%d", result.StatusCode)
				response.ErrorMessage = strings.TrimSpace(string(body))
			}
			if err := writeLocalControlFrame(conn, response); err != nil {
				return
			}
			continue
		}
		return
	}
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
