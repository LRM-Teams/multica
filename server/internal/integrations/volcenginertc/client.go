package volcenginertc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	volcbase "github.com/volcengine/volcengine-go-sdk/volcengine/base"
)

const (
	DefaultAPIVersion = "2025-06-01"
	LegacyAPIVersion  = "2024-12-01"

	defaultEndpoint = "https://rtc.volcengineapi.com"
	defaultRegion   = "cn-north-1"
	defaultService  = "rtc"

	maxRequestBytes  = 1 << 20
	maxResponseBytes = 1 << 20
)

type Config struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Endpoint        string
	Region          string
	APIVersion      string
	HTTPClient      *http.Client
}

type Client struct {
	endpoint    *url.URL
	httpClient  *http.Client
	credentials volcbase.Credentials
	apiVersion  string
	now         func() time.Time
}

type StartVoiceChatRequest struct {
	AppID       string          `json:"AppId"`
	RoomID      string          `json:"RoomId"`
	TaskID      string          `json:"TaskId"`
	BusinessID  string          `json:"BusinessId,omitempty"`
	Config      json.RawMessage `json:"Config"`
	AgentConfig json.RawMessage `json:"AgentConfig"`
}

type UpdateCommand string

const (
	UpdateCommandInterrupt            UpdateCommand = "interrupt"
	UpdateCommandFunction             UpdateCommand = "function"
	UpdateCommandExternalTextToSpeech UpdateCommand = "ExternalTextToSpeech"
)

type UpdateVoiceChatRequest struct {
	AppID         string        `json:"AppId"`
	RoomID        string        `json:"RoomId"`
	TaskID        string        `json:"TaskId"`
	Command       UpdateCommand `json:"Command"`
	Message       string        `json:"Message,omitempty"`
	InterruptMode int           `json:"InterruptMode,omitempty"`
}

type StopVoiceChatRequest struct {
	AppID  string `json:"AppId"`
	RoomID string `json:"RoomId"`
	TaskID string `json:"TaskId"`
}

type Response struct {
	RequestID string
}

type ProviderError struct {
	Action     string
	StatusCode int
	RequestID  string
	Code       string
	Message    string
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "volcengine RTC request failed"
	}
	detail := strings.TrimSpace(e.Message)
	if detail == "" {
		detail = http.StatusText(e.StatusCode)
	}
	if detail == "" {
		detail = "provider request failed"
	}
	if e.Code != "" {
		return fmt.Sprintf("volcengine RTC %s failed (%s): %s", e.Action, e.Code, detail)
	}
	return fmt.Sprintf("volcengine RTC %s failed: %s", e.Action, detail)
}

type responseEnvelope struct {
	ResponseMetadata responseMetadata `json:"ResponseMetadata"`
	Code             string           `json:"code"`
	Message          string           `json:"message"`
	Description      string           `json:"description"`
}

type responseMetadata struct {
	RequestID string                 `json:"RequestId"`
	Error     *responseMetadataError `json:"Error"`
}

type responseMetadataError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

func New(config Config) (*Client, error) {
	accessKeyID := strings.TrimSpace(config.AccessKeyID)
	secretAccessKey := strings.TrimSpace(config.SecretAccessKey)
	if accessKeyID == "" {
		return nil, errors.New("volcengine RTC access key id is required")
	}
	if secretAccessKey == "" {
		return nil, errors.New("volcengine RTC secret access key is required")
	}

	endpointValue := strings.TrimSpace(config.Endpoint)
	if endpointValue == "" {
		endpointValue = defaultEndpoint
	}
	endpoint, err := url.Parse(endpointValue)
	if err != nil {
		return nil, fmt.Errorf("parse volcengine RTC endpoint: %w", err)
	}
	if endpoint.Scheme != "https" || endpoint.Host == "" ||
		(endpoint.Path != "" && endpoint.Path != "/") ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.User != nil {
		return nil, errors.New("volcengine RTC endpoint must be an HTTPS origin")
	}
	endpoint.Path = "/"

	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = defaultRegion
	}
	apiVersion := strings.TrimSpace(config.APIVersion)
	if apiVersion == "" {
		apiVersion = DefaultAPIVersion
	}
	switch apiVersion {
	case DefaultAPIVersion, LegacyAPIVersion:
	default:
		return nil, fmt.Errorf(
			"volcengine RTC API version %q is unsupported; use %s or %s",
			apiVersion,
			DefaultAPIVersion,
			LegacyAPIVersion,
		)
	}
	httpClient := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: rejectRedirect,
	}
	if config.HTTPClient != nil {
		httpClient.Transport = config.HTTPClient.Transport
		httpClient.Jar = config.HTTPClient.Jar
		if config.HTTPClient.Timeout > 0 {
			httpClient.Timeout = config.HTTPClient.Timeout
		}
	}
	return &Client{
		endpoint:   endpoint,
		httpClient: httpClient,
		credentials: volcbase.Credentials{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			SessionToken:    strings.TrimSpace(config.SessionToken),
			Service:         defaultService,
			Region:          region,
		},
		apiVersion: apiVersion,
		now:        time.Now,
	}, nil
}

func (c *Client) StartVoiceChat(ctx context.Context, request StartVoiceChatRequest) (Response, error) {
	if err := validateCallIdentity(request.AppID, request.RoomID, request.TaskID); err != nil {
		return Response{}, err
	}
	if err := requireJSONObject("Config", request.Config); err != nil {
		return Response{}, err
	}
	if err := validateAgentConfig(request.AgentConfig); err != nil {
		return Response{}, err
	}
	return c.call(ctx, "StartVoiceChat", request)
}

func (c *Client) UpdateVoiceChat(ctx context.Context, request UpdateVoiceChatRequest) (Response, error) {
	if err := validateCallIdentity(request.AppID, request.RoomID, request.TaskID); err != nil {
		return Response{}, err
	}
	if strings.TrimSpace(string(request.Command)) == "" {
		return Response{}, errors.New("volcengine RTC Command is required")
	}
	switch request.Command {
	case UpdateCommandInterrupt:
		if strings.TrimSpace(request.Message) != "" || request.InterruptMode != 0 {
			return Response{}, errors.New(
				"volcengine RTC interrupt command must not include Message or InterruptMode",
			)
		}
	case UpdateCommandFunction:
		if request.InterruptMode != 0 {
			return Response{}, errors.New(
				"volcengine RTC function command must not include InterruptMode",
			)
		}
		if err := validateFunctionResultMessage(request.Message); err != nil {
			return Response{}, err
		}
	case UpdateCommandExternalTextToSpeech:
		message := strings.TrimSpace(request.Message)
		if message == "" {
			return Response{}, errors.New(
				"volcengine RTC ExternalTextToSpeech Message is required",
			)
		}
		if !utf8.ValidString(message) || len([]rune(message)) > 200 {
			return Response{}, errors.New(
				"volcengine RTC ExternalTextToSpeech Message exceeds 200 characters",
			)
		}
		if request.InterruptMode < 1 || request.InterruptMode > 3 {
			return Response{}, errors.New(
				"volcengine RTC ExternalTextToSpeech InterruptMode must be 1, 2, or 3",
			)
		}
	default:
		return Response{}, fmt.Errorf("volcengine RTC Command %q is not supported", request.Command)
	}
	return c.call(ctx, "UpdateVoiceChat", request)
}

func validateFunctionResultMessage(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("volcengine RTC function command Message is required")
	}
	if len(message) > maxRequestBytes {
		return fmt.Errorf(
			"volcengine RTC function command Message exceeds %d bytes",
			maxRequestBytes,
		)
	}
	var result struct {
		ToolCallID string `json:"ToolCallID"`
		Content    string `json:"Content"`
	}
	decoder := json.NewDecoder(strings.NewReader(message))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("decode volcengine RTC function command Message: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("decode volcengine RTC function command Message: %w", err)
	}
	if strings.TrimSpace(result.ToolCallID) == "" {
		return errors.New(
			"volcengine RTC function command Message ToolCallID is required",
		)
	}
	if strings.TrimSpace(result.Content) == "" {
		return errors.New(
			"volcengine RTC function command Message Content is required",
		)
	}
	return nil
}

func (c *Client) StopVoiceChat(ctx context.Context, request StopVoiceChatRequest) (Response, error) {
	if err := validateCallIdentity(request.AppID, request.RoomID, request.TaskID); err != nil {
		return Response{}, err
	}
	return c.call(ctx, "StopVoiceChat", request)
}

func (c *Client) call(ctx context.Context, action string, body any) (Response, error) {
	if c == nil || c.endpoint == nil || c.httpClient == nil || c.now == nil {
		return Response{}, errors.New("volcengine RTC client is not initialized")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("encode volcengine RTC %s request: %w", action, err)
	}
	if len(payload) > maxRequestBytes {
		return Response{}, fmt.Errorf("volcengine RTC %s request exceeds %d bytes", action, maxRequestBytes)
	}
	requestURL := *c.endpoint
	query := requestURL.Query()
	query.Set("Action", action)
	query.Set("Version", c.apiVersion)
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("create volcengine RTC %s request: %w", action, err)
	}
	request.Host = request.URL.Host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Date", c.now().UTC().Format("20060102T150405Z"))
	c.credentials.Sign(request)

	httpResponse, err := c.httpClient.Do(request)
	if err != nil {
		return Response{}, fmt.Errorf("send volcengine RTC %s request: %w", action, err)
	}
	defer httpResponse.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("read volcengine RTC %s response: %w", action, err)
	}
	if len(responseBody) > maxResponseBytes {
		return Response{}, fmt.Errorf("volcengine RTC %s response exceeds %d bytes", action, maxResponseBytes)
	}

	var envelope responseEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
			return Response{}, &ProviderError{
				Action:     action,
				StatusCode: httpResponse.StatusCode,
				Code:       fmt.Sprintf("HTTP_%d", httpResponse.StatusCode),
				Message:    http.StatusText(httpResponse.StatusCode),
			}
		}
		return Response{}, fmt.Errorf("decode volcengine RTC %s response: %w", action, err)
	}
	if envelope.ResponseMetadata.Error != nil {
		return Response{}, &ProviderError{
			Action:     action,
			StatusCode: httpResponse.StatusCode,
			RequestID:  envelope.ResponseMetadata.RequestID,
			Code:       strings.TrimSpace(envelope.ResponseMetadata.Error.Code),
			Message:    strings.TrimSpace(envelope.ResponseMetadata.Error.Message),
		}
	}
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		code := strings.TrimSpace(envelope.Code)
		if code == "" {
			code = fmt.Sprintf("HTTP_%d", httpResponse.StatusCode)
		}
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = strings.TrimSpace(envelope.Description)
		}
		if message == "" {
			message = http.StatusText(httpResponse.StatusCode)
		}
		return Response{}, &ProviderError{
			Action:     action,
			StatusCode: httpResponse.StatusCode,
			RequestID:  envelope.ResponseMetadata.RequestID,
			Code:       code,
			Message:    message,
		}
	}
	return Response{RequestID: envelope.ResponseMetadata.RequestID}, nil
}

func validateCallIdentity(appID, roomID, taskID string) error {
	for _, input := range []struct {
		field string
		value string
	}{
		{field: "AppId", value: appID},
		{field: "RoomId", value: roomID},
		{field: "TaskId", value: taskID},
	} {
		if strings.TrimSpace(input.value) == "" {
			return fmt.Errorf("volcengine RTC %s is required", input.field)
		}
	}
	return nil
}

func rejectRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func requireJSONObject(field string, value json.RawMessage) error {
	if len(value) == 0 {
		return fmt.Errorf("volcengine RTC %s is required", field)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil {
		return fmt.Errorf("volcengine RTC %s must be a JSON object: %w", field, err)
	}
	if object == nil {
		return fmt.Errorf("volcengine RTC %s must be a JSON object", field)
	}
	return nil
}

func validateAgentConfig(value json.RawMessage) error {
	if err := requireJSONObject("AgentConfig", value); err != nil {
		return err
	}
	var config struct {
		TargetUserIDs []string `json:"TargetUserId"`
		UserID        string   `json:"UserId"`
	}
	if err := json.Unmarshal(value, &config); err != nil {
		return fmt.Errorf("decode volcengine RTC AgentConfig: %w", err)
	}
	if len(config.TargetUserIDs) != 1 || strings.TrimSpace(config.TargetUserIDs[0]) == "" {
		return errors.New("volcengine RTC AgentConfig requires exactly one TargetUserId")
	}
	if strings.TrimSpace(config.UserID) == "" {
		return errors.New("volcengine RTC AgentConfig UserId is required")
	}
	if config.TargetUserIDs[0] == config.UserID {
		return errors.New("volcengine RTC AgentConfig UserId must differ from TargetUserId")
	}
	return nil
}
