package duplexcall

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/integrations/doubaodialog"
)

// DialogClient opens Doubao Duplex sessions.
type DialogClient interface {
	OpenSession(ctx context.Context, session doubaodialog.SessionConfig) (*doubaodialog.Session, error)
}

// MulticaExecutor is satisfied by doubaodialog.MulticaExecutor.
type MulticaExecutor = doubaodialog.MulticaExecutor

// Emitter delivers one FE-facing event (typically a WebSocket write).
type Emitter func(ServerEvent) error

// Session is one live Duplex media bridge for a voice_call_session id.
type Session struct {
	CallID    string
	SessionID string

	dialog  *doubaodialog.Session
	bridge  *doubaodialog.MulticaToolBridge
	emit    Emitter
	cancel  context.CancelFunc
	done    chan struct{}
	closeOnce sync.Once
}

// Gateway tracks in-flight Duplex sessions keyed by Multica voice call id.
type Gateway struct {
	client   DialogClient
	config   doubaodialog.Config
	mu       sync.Mutex
	sessions map[string]*Session
	pending  map[string]struct{} // activated via HTTP before WS connects
}

func NewGateway(client DialogClient, config doubaodialog.Config) (*Gateway, error) {
	if client == nil {
		return nil, errors.New("duplex gateway requires a dialog client")
	}
	cfg := config.Normalized()
	if err := cfg.ValidateForDial(); err != nil {
		return nil, err
	}
	return &Gateway{
		client:   client,
		config:   cfg,
		sessions: make(map[string]*Session),
		pending:  make(map[string]struct{}),
	}, nil
}

func (g *Gateway) Configured() bool {
	return g != nil && g.client != nil
}

// MarkPending records that callID chose Duplex media (no RTC VoiceChat).
// Stop must skip provider.Stop even before the browser opens the WS.
func (g *Gateway) MarkPending(callID string) {
	if g == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pending[callID] = struct{}{}
}

func (g *Gateway) Has(callID string) bool {
	if g == nil {
		return false
	}
	callID = strings.TrimSpace(callID)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.sessions[callID]; ok {
		return true
	}
	_, ok := g.pending[callID]
	return ok
}

// Start opens Doubao Duplex for callID and begins pumping provider events to emit.
// executor runs Multica delegate_work_to_multica_agent; must be scoped to callID.
func (g *Gateway) Start(
	ctx context.Context,
	callID string,
	executor MulticaExecutor,
	emit Emitter,
) (*Session, error) {
	if g == nil {
		return nil, errors.New("duplex gateway is nil")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, errors.New("duplex call id is required")
	}
	if executor == nil {
		return nil, errors.New("duplex multica executor is required")
	}
	if emit == nil {
		return nil, errors.New("duplex emitter is required")
	}

	g.mu.Lock()
	if _, exists := g.sessions[callID]; exists {
		g.mu.Unlock()
		return nil, fmt.Errorf("duplex session already active for call %s", callID)
	}
	g.mu.Unlock()

	dialog, err := g.client.OpenSession(ctx, doubaodialog.DefaultSessionConfig(
		g.config.Model,
		g.config.Voice,
		doubaodialog.DefaultDialogInstructions(),
		[]doubaodialog.Tool{doubaodialog.MulticaDelegateTool()},
	))
	if err != nil {
		return nil, fmt.Errorf("open duplex dialog: %w", err)
	}

	bridge, err := doubaodialog.NewMulticaToolBridge(executor, dialog)
	if err != nil {
		_ = dialog.Close(context.Background())
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	session := &Session{
		CallID: callID,
		dialog: dialog,
		bridge: bridge,
		emit:   emit,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	g.mu.Lock()
	if _, exists := g.sessions[callID]; exists {
		g.mu.Unlock()
		cancel()
		_ = dialog.Close(context.Background())
		return nil, fmt.Errorf("duplex session already active for call %s", callID)
	}
	delete(g.pending, callID)
	g.sessions[callID] = session
	g.mu.Unlock()

	go session.pump(runCtx, g)

	return session, nil
}

func (g *Gateway) Get(callID string) (*Session, bool) {
	if g == nil {
		return nil, false
	}
	callID = strings.TrimSpace(callID)
	g.mu.Lock()
	defer g.mu.Unlock()
	session, ok := g.sessions[callID]
	return session, ok
}

// Close stops an active Duplex media session for callID, if any, and clears pending.
func (g *Gateway) Close(callID string) {
	callID = strings.TrimSpace(callID)
	session, ok := g.Get(callID)
	if ok {
		session.Close()
	}
	g.mu.Lock()
	delete(g.pending, callID)
	g.mu.Unlock()
}

func (g *Gateway) detach(callID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sessions, callID)
	delete(g.pending, callID)
}

func (s *Session) pump(ctx context.Context, gateway *Gateway) {
	defer close(s.done)
	defer gateway.detach(s.CallID)
	defer func() {
		_ = s.dialog.Close(context.Background())
	}()

	for {
		event, err := s.dialog.Recv(ctx)
		if err != nil {
			if ctx.Err() != nil {
				_ = s.safeEmit(ServerEvent{Type: ServerClosed, CallID: s.CallID})
				return
			}
			_ = s.safeEmit(ServerEvent{
				Type:    ServerError,
				CallID:  s.CallID,
				Code:    "duplex_provider_read_failed",
				Message: "duplex provider connection closed",
			})
			_ = s.safeEmit(ServerEvent{Type: ServerClosed, CallID: s.CallID})
			return
		}
		if err := s.handleProviderEvent(ctx, event); err != nil {
			slog.Warn("duplex session event handling failed", "call_id", s.CallID, "error", err)
			_ = s.safeEmit(ServerEvent{
				Type:    ServerError,
				CallID:  s.CallID,
				Code:    "duplex_event_failed",
				Message: err.Error(),
			})
		}
	}
}

func (s *Session) handleProviderEvent(ctx context.Context, event doubaodialog.ServerEvent) error {
	switch event.Type {
	case doubaodialog.EventSessionCreated:
		s.SessionID = strings.TrimSpace(event.SessionID)
		return s.safeEmit(ServerEvent{
			Type:        ServerReady,
			CallID:      s.CallID,
			SessionID:   s.SessionID,
			SampleRate:  24000, // Duplex TTS output rate; client mic ingress remains 16 kHz PCM
			AudioFormat: "pcm_s16le",
		})
	case doubaodialog.EventASRStarted:
		_, _ = s.bridge.HandleServerEvent(ctx, event)
		return s.safeEmit(ServerEvent{
			Type:       ServerASR,
			CallID:     s.CallID,
			SessionID:  s.SessionID,
			Phase:      "started",
			Transcript: strings.TrimSpace(event.Transcript + event.Text + event.Delta),
		})
	case doubaodialog.EventASRCompleted:
		return s.safeEmit(ServerEvent{
			Type:       ServerASR,
			CallID:     s.CallID,
			SessionID:  s.SessionID,
			Phase:      "completed",
			Transcript: strings.TrimSpace(event.Transcript + event.Text + event.Delta),
		})
	case doubaodialog.EventOutputAudioDelta:
		audio := event.Audio
		if len(audio) == 0 && strings.TrimSpace(event.Delta) != "" {
			raw, err := base64.StdEncoding.DecodeString(event.Delta)
			if err == nil {
				audio = raw
			}
		}
		if len(audio) == 0 {
			return nil
		}
		return s.safeEmit(ServerEvent{
			Type:      ServerAudioDelta,
			CallID:    s.CallID,
			SessionID: s.SessionID,
			Audio:     base64.StdEncoding.EncodeToString(audio),
		})
	case doubaodialog.EventOutputTextDelta, doubaodialog.EventOutputTextDone:
		text := strings.TrimSpace(event.Text + event.Delta + event.Transcript)
		if text == "" {
			return nil
		}
		return s.safeEmit(ServerEvent{
			Type:      ServerTextDelta,
			CallID:    s.CallID,
			SessionID: s.SessionID,
			Text:      text,
		})
	case doubaodialog.EventFunctionCallArgumentsDone:
		for _, call := range event.FunctionCalls {
			_ = s.safeEmit(ServerEvent{
				Type:   ServerTool,
				CallID: s.CallID,
				Name:   call.Name,
				Status: "started",
			})
		}
		handled, err := s.bridge.HandleServerEvent(ctx, event)
		if err != nil {
			_ = s.safeEmit(ServerEvent{
				Type:    ServerTool,
				CallID:  s.CallID,
				Name:    doubaodialog.MulticaDelegateToolName,
				Status:  "error",
				Result:  err.Error(),
			})
			return err
		}
		if handled {
			_ = s.safeEmit(ServerEvent{
				Type:   ServerTool,
				CallID: s.CallID,
				Name:   doubaodialog.MulticaDelegateToolName,
				Status: "done",
			})
		}
		return nil
	case doubaodialog.EventError:
		return s.safeEmit(ServerEvent{
			Type:    ServerError,
			CallID:  s.CallID,
			Code:    "duplex_provider_error",
			Message: strings.TrimSpace(event.ErrorMessage),
		})
	case doubaodialog.EventSessionClosed:
		return s.safeEmit(ServerEvent{Type: ServerClosed, CallID: s.CallID})
	default:
		_, err := s.bridge.HandleServerEvent(ctx, event)
		return err
	}
}

func (s *Session) HandleClientEvent(ctx context.Context, event ClientEvent) error {
	if s == nil || s.dialog == nil {
		return errors.New("duplex session is closed")
	}
	switch strings.TrimSpace(event.Type) {
	case ClientAudioAppend:
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(event.Audio))
		if err != nil {
			return fmt.Errorf("decode client audio: %w", err)
		}
		return s.dialog.SendAudio(ctx, raw)
	case ClientAudioCommit:
		return s.dialog.CommitAudio(ctx)
	case ClientInterrupt:
		return s.dialog.CancelResponse(ctx)
	case ClientClose:
		s.Close()
		return nil
	default:
		return fmt.Errorf("unsupported duplex client event %q", event.Type)
	}
}

func (s *Session) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

func (s *Session) Wait() {
	if s == nil || s.done == nil {
		return
	}
	<-s.done
}

func (s *Session) safeEmit(event ServerEvent) error {
	if s == nil || s.emit == nil {
		return nil
	}
	if err := s.emit(event); err != nil {
		slog.Debug("duplex emit failed", "call_id", s.CallID, "type", event.Type, "error", err)
		return err
	}
	return nil
}

// MapProviderEventForTest exposes handleProviderEvent for unit tests.
func MapProviderEventForTest(s *Session, ctx context.Context, event doubaodialog.ServerEvent) error {
	return s.handleProviderEvent(ctx, event)
}
