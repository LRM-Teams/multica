package doubaodialog

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Conn is the duplex WebSocket surface used by Session and tests.
type Conn interface {
	WriteMessage(messageType int, data []byte) error
	ReadMessage() (messageType int, data []byte, err error)
	Close() error
}

type Dialer interface {
	DialContext(ctx context.Context, url string, header http.Header) (Conn, *http.Response, error)
}

type gorillaDialer struct {
	inner *websocket.Dialer
}

func (d gorillaDialer) DialContext(ctx context.Context, url string, header http.Header) (Conn, *http.Response, error) {
	conn, resp, err := d.inner.DialContext(ctx, url, header)
	if err != nil {
		return nil, resp, err
	}
	return conn, resp, nil
}

type Client struct {
	config Config
	dialer Dialer
}

func New(config Config) (*Client, error) {
	cfg := config.Normalized()
	if err := cfg.ValidateForDial(); err != nil {
		return nil, err
	}
	if !cfg.IsDuplex() {
		return nil, fmt.Errorf(
			"classic Realtime Dialogue binary protocol is not implemented in this Spike; set %s to the Duplex endpoint (default %s)",
			EnvEndpoint,
			DefaultDuplexEndpoint,
		)
	}
	return &Client{
		config: cfg,
		dialer: gorillaDialer{inner: websocket.DefaultDialer},
	}, nil
}

func (c *Client) handshakeHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("X-Api-Key", c.config.APIKey)
	if c.config.AppID != "" {
		headers.Set("X-Api-App-Id", c.config.AppID)
	}
	headers.Set("X-Api-Connect-Id", uuid.NewString())
	return headers
}

// Session is one Duplex dialogue over a single WebSocket.
type Session struct {
	conn   Conn
	logID  string
	mu     sync.Mutex
	closed bool
}

func (c *Client) OpenSession(ctx context.Context, session SessionConfig) (*Session, error) {
	if strings.TrimSpace(session.Model) == "" {
		session.Model = c.config.Model
	}
	if strings.TrimSpace(session.Audio.Output.Voice) == "" {
		session.Audio.Output.Voice = c.config.Voice
	}
	// Upstream Duplex echoes client-provided session.id (dialog_id). Omitting it
	// yields session.created with an empty id and breaks follow-up turns.
	if strings.TrimSpace(session.ID) == "" {
		session.ID = uuid.NewString()
	}

	dialCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	conn, resp, err := c.dialer.DialContext(dialCtx, c.config.Endpoint, c.handshakeHeaders())
	logID := ""
	if resp != nil {
		logID = resp.Header.Get("X-Tt-Logid")
	}
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		return nil, fmt.Errorf("dial doubao duplex dialogue (HTTP %d, logid=%s): %w", status, logID, err)
	}

	s := &Session{conn: conn, logID: logID}
	if err := s.Send(ctx, ClientEvent{
		Type:    EventSessionCreate,
		EventID: uuid.NewString(),
		Session: &session,
	}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

func (s *Session) LogID() string {
	if s == nil {
		return ""
	}
	return s.logID
}

func (s *Session) Send(ctx context.Context, event ClientEvent) error {
	if s == nil {
		return fmt.Errorf("doubao dialog session is nil")
	}
	payload, err := EncodeClientEvent(event)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("doubao dialog session is closed")
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("write duplex event %s: %w", event.Type, err)
	}
	return nil
}

func (s *Session) Recv(ctx context.Context) (ServerEvent, error) {
	if s == nil {
		return ServerEvent{}, fmt.Errorf("doubao dialog session is nil")
	}
	type result struct {
		event ServerEvent
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			ch <- result{err: fmt.Errorf("read duplex event: %w", err)}
			return
		}
		event, err := ParseServerEvent(data)
		ch <- result{event: event, err: err}
	}()
	select {
	case <-ctx.Done():
		return ServerEvent{}, ctx.Err()
	case out := <-ch:
		return out.event, out.err
	}
}

func (s *Session) SendAudio(ctx context.Context, pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	return s.Send(ctx, ClientEvent{
		Type:    EventInputAudioAppend,
		EventID: uuid.NewString(),
		Audio:   base64.StdEncoding.EncodeToString(pcm),
	})
}

// CommitAudio force-commits the input audio buffer (end of user utterance).
func (s *Session) CommitAudio(ctx context.Context) error {
	return s.Send(ctx, ClientEvent{
		Type:    EventInputAudioCommit,
		EventID: uuid.NewString(),
	})
}

// SendSpeechText asks the service to synthesize the given text (client TTS path).
// Useful in Spike tests to obtain PCM that can be looped back as user audio.
func (s *Session) SendSpeechText(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("speech text is required")
	}
	if err := s.Send(ctx, ClientEvent{
		Type:    "speech_text_buffer.append",
		EventID: uuid.NewString(),
		Text:    text,
	}); err != nil {
		return err
	}
	return s.Send(ctx, ClientEvent{
		Type:    "speech_text_buffer.commit",
		EventID: uuid.NewString(),
	})
}

// SendUserText inserts a user turn into the Duplex conversation context so the
// model can decide whether to call session.tools (including Multica delegate).
func (s *Session) SendUserText(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("user text is required")
	}
	return s.Send(ctx, ClientEvent{
		Type:    EventConversationCreate,
		EventID: uuid.NewString(),
		Items: []ConversationItem{{
			Type: "message",
			Role: "user",
			Content: []ConversationItemContent{{
				Type: "input_text",
				Text: text,
			}},
		}},
	})
}

func (s *Session) CancelResponse(ctx context.Context) error {
	return s.Send(ctx, ClientEvent{
		Type:    EventResponseCancel,
		EventID: uuid.NewString(),
	})
}

func (s *Session) SendFunctionCallOutputs(ctx context.Context, outputs []FunctionCallOutput) error {
	if len(outputs) == 0 {
		return fmt.Errorf("function call outputs are required")
	}
	items := make([]ConversationItem, 0, len(outputs))
	for _, output := range outputs {
		callID := strings.TrimSpace(output.CallID)
		text := strings.TrimSpace(output.Output)
		if callID == "" {
			return fmt.Errorf("function call output call_id is required")
		}
		if text == "" {
			return fmt.Errorf("function call output text is required")
		}
		items = append(items, ConversationItem{
			CallID: callID,
			Role:   "tool",
			Content: []ConversationItemContent{{
				Type: "input_text",
				Text: text,
			}},
		})
	}
	return s.Send(ctx, ClientEvent{
		Type:    EventConversationCreate,
		EventID: uuid.NewString(),
		Items:   items,
	})
}

func (s *Session) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	_ = s.Send(ctx, ClientEvent{
		Type:    EventSessionClose,
		EventID: uuid.NewString(),
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.conn.Close()
}

// FunctionCallOutput is returned to the model after Multica execution.
type FunctionCallOutput struct {
	CallID string
	Output string
}
