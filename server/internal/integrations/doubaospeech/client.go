package doubaospeech

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	DefaultTTSEndpoint = "wss://openspeech.bytedance.com/api/v3/tts/bidirection"
	DefaultASREndpoint = "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async"
	TTSResourceID      = "seed-tts-2.0"
	ASRResourceID      = "volc.seedasr.sauc.duration"

	PCMContentType = "audio/pcm"
)

type Config struct {
	APIKey      string
	SpeakerID   string
	TTSEndpoint string
	ASREndpoint string
	Timeout     time.Duration
	Dialer      *websocket.Dialer
	ASRPace     func(context.Context, time.Duration) error
}

type Client struct {
	apiKey      string
	speakerID   string
	ttsEndpoint string
	asrEndpoint string
	timeout     time.Duration
	dialer      *websocket.Dialer
	asrPace     func(context.Context, time.Duration) error
}

type SynthesisRequest struct {
	Text       string
	Format     string
	SampleRate int
}

type Audio struct {
	Data       []byte
	Format     string
	SampleRate int
	LogID      string
}

type TranscriptionRequest struct {
	PCM        []byte
	SampleRate int
}

type Transcript struct {
	Text  string `json:"text"`
	LogID string `json:"-"`
}

type ProviderError struct {
	Operation string
	Code      uint32
	LogID     string
	Message   string
	Err       error
}

func (e *ProviderError) Error() string {
	detail := strings.TrimSpace(e.Message)
	if detail == "" && e.Err != nil {
		detail = e.Err.Error()
	}
	if detail == "" {
		detail = "provider request failed"
	}
	if e.Code != 0 {
		return fmt.Sprintf("doubao speech %s failed (code %d): %s", e.Operation, e.Code, detail)
	}
	return fmt.Sprintf("doubao speech %s failed: %s", e.Operation, detail)
}

func (e *ProviderError) Unwrap() error { return e.Err }

func New(config Config) *Client {
	if config.TTSEndpoint == "" {
		config.TTSEndpoint = DefaultTTSEndpoint
	}
	if config.ASREndpoint == "" {
		config.ASREndpoint = DefaultASREndpoint
	}
	if config.Timeout <= 0 {
		config.Timeout = 90 * time.Second
	}
	if config.Dialer == nil {
		config.Dialer = websocket.DefaultDialer
	}
	if config.ASRPace == nil {
		config.ASRPace = waitForContext
	}
	return &Client{
		apiKey:      strings.TrimSpace(config.APIKey),
		speakerID:   strings.TrimSpace(config.SpeakerID),
		ttsEndpoint: config.TTSEndpoint,
		asrEndpoint: config.ASREndpoint,
		timeout:     config.Timeout,
		dialer:      config.Dialer,
		asrPace:     config.ASRPace,
	}
}

func (c *Client) IsConfigured() bool {
	return c != nil && c.apiKey != "" && c.speakerID != ""
}

func (c *Client) configurationError(operation string, requireSpeaker bool) error {
	if c == nil || c.apiKey == "" {
		return &ProviderError{Operation: operation, Message: "DOUBAO_SPEECH_API_KEY is not configured"}
	}
	if requireSpeaker && c.speakerID == "" {
		return &ProviderError{Operation: operation, Message: "DOUBAO_TTS_SPEAKER_ID is not configured"}
	}
	return nil
}

func (c *Client) providerHeaders(resourceID string) http.Header {
	headers := make(http.Header)
	headers.Set("X-Api-Key", c.apiKey)
	headers.Set("X-Api-Resource-Id", resourceID)
	headers.Set("X-Api-Connect-Id", uuid.NewString())
	return headers
}

func (c *Client) dial(ctx context.Context, endpoint, resourceID, operation string) (*websocket.Conn, string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	conn, response, err := c.dialer.DialContext(requestCtx, endpoint, c.providerHeaders(resourceID))
	logID := ""
	if response != nil {
		logID = response.Header.Get("X-Tt-Logid")
	}
	if err != nil {
		message := "websocket connection failed"
		if response != nil {
			message = fmt.Sprintf("websocket connection failed with HTTP %d", response.StatusCode)
		}
		return nil, logID, &ProviderError{Operation: operation, LogID: logID, Message: message, Err: err}
	}
	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		conn.Close()
		return nil, logID, &ProviderError{Operation: operation, LogID: logID, Message: "set websocket read deadline", Err: err}
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		conn.Close()
		return nil, logID, &ProviderError{Operation: operation, LogID: logID, Message: "set websocket write deadline", Err: err}
	}
	return conn, logID, nil
}

func writeBinary(conn *websocket.Conn, payload []byte, operation, logID string) error {
	if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		return &ProviderError{Operation: operation, LogID: logID, Message: "write websocket frame", Err: err}
	}
	return nil
}

func readFrame(conn *websocket.Conn, operation, logID string) (providerFrame, error) {
	messageType, raw, err := conn.ReadMessage()
	if err != nil {
		return providerFrame{}, &ProviderError{Operation: operation, LogID: logID, Message: "read websocket frame", Err: err}
	}
	if messageType != websocket.BinaryMessage {
		return providerFrame{}, &ProviderError{Operation: operation, LogID: logID, Message: "provider returned a non-binary websocket frame"}
	}
	frame, err := parseProviderFrame(raw)
	if err != nil {
		return providerFrame{}, &ProviderError{Operation: operation, LogID: logID, Message: "parse websocket frame", Err: err}
	}
	if frame.messageType == messageError {
		return providerFrame{}, &ProviderError{Operation: operation, Code: frame.errorCode, LogID: logID, Message: providerMessage(frame.payload)}
	}
	return frame, nil
}

func providerMessage(payload []byte) string {
	const maxMessageBytes = 512
	if len(payload) > maxMessageBytes {
		payload = payload[:maxMessageBytes]
	}
	return strings.TrimSpace(string(payload))
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func encodeJSON(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	return payload, nil
}

func closeWebsocket(conn *websocket.Conn) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	_ = conn.Close()
}
