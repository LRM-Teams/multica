package doubaodialog

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Duplex event type constants (JSON text frames).
const (
	EventSessionCreate  = "session.create"
	EventSessionUpdate  = "session.update"
	EventSessionClose   = "session.close"
	EventSessionCreated = "session.created"
	EventSessionUpdated = "session.updated"
	EventSessionClosed  = "session.closed"

	EventInputAudioAppend   = "input_audio_buffer.append"
	EventInputAudioCommit   = "input_audio_buffer.commit"
	EventResponseCancel     = "response.cancel"
	EventConversationCreate = "conversation.item.create"

	EventFunctionCallArgumentsDone = "response.function_call_arguments.done"
	EventOutputAudioDelta          = "response.output_audio.delta"
	EventOutputAudioDone           = "response.output_audio.done"
	EventOutputTextDelta           = "response.output_text.delta"
	EventOutputTextDone            = "response.output_text.done"
	EventResponseDone              = "response.done"
	EventASRStarted                = "conversation.item.input_audio_transcription.started"
	EventASRCompleted              = "conversation.item.input_audio_transcription.completed"
	EventError                     = "error"
)

// Tool is a Duplex session.tools entry. Upstream uses a flat OpenAI-like shape
// (name/description/parameters at the top level), not nested `function`.
type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type SessionAudioFormat struct {
	Type       string `json:"type"`
	SampleRate int    `json:"rate,omitempty"`
}

type SessionAudioInput struct {
	Format SessionAudioFormat `json:"format"`
}

type SessionAudioOutput struct {
	Format   SessionAudioFormat `json:"format"`
	Voice    string             `json:"voice,omitempty"`
	Speed    *float64           `json:"speed,omitempty"`
	Loudness *float64           `json:"loudness,omitempty"`
}

type SessionAudio struct {
	Input  SessionAudioInput  `json:"input"`
	Output SessionAudioOutput `json:"output"`
}

type SessionConfig struct {
	ID           string       `json:"id,omitempty"`
	Model        string       `json:"model"`
	Instructions string       `json:"instructions,omitempty"`
	Audio        SessionAudio `json:"audio"`
	Tools        []Tool       `json:"tools,omitempty"`
}

type ClientEvent struct {
	Type    string             `json:"type"`
	EventID string             `json:"event_id,omitempty"`
	Session *SessionConfig     `json:"session,omitempty"`
	Audio   string             `json:"audio,omitempty"`
	Text    string             `json:"text,omitempty"`
	Items   []ConversationItem `json:"items,omitempty"`
}

type ConversationItem struct {
	ID      string                    `json:"id,omitempty"`
	Type    string                    `json:"type,omitempty"`
	Role    string                    `json:"role,omitempty"`
	CallID  string                    `json:"call_id,omitempty"`
	Status  string                    `json:"status,omitempty"`
	Content []ConversationItemContent `json:"content,omitempty"`
}

type ConversationItemContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type FunctionCall struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ServerEvent struct {
	Type    string          `json:"type"`
	EventID string          `json:"event_id,omitempty"`
	Raw     json.RawMessage `json:"-"`

	SessionID  string `json:"-"`
	Delta      string `json:"-"`
	Text       string `json:"-"`
	Audio      []byte `json:"-"`
	Transcript string `json:"-"`

	FunctionCalls []FunctionCall `json:"-"`
	ErrorMessage  string         `json:"-"`
}

func DefaultSessionConfig(model, voice, instructions string, tools []Tool) SessionConfig {
	return SessionConfig{
		Model:        model,
		Instructions: instructions,
		Audio: SessionAudio{
			Input: SessionAudioInput{
				Format: SessionAudioFormat{Type: "pcm", SampleRate: 16000},
			},
			Output: SessionAudioOutput{
				Format: SessionAudioFormat{Type: "pcm_s16le", SampleRate: 24000},
				Voice:  voice,
			},
		},
		Tools: tools,
	}
}

func ParseServerEvent(raw []byte) (ServerEvent, error) {
	var envelope struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
		Session *struct {
			ID string `json:"id"`
		} `json:"session"`
		Delta      string         `json:"delta"`
		Text       string         `json:"text"`
		Transcript string         `json:"transcript"`
		Audio      string         `json:"audio"`
		Items      []FunctionCall `json:"items"`
		Error      *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ServerEvent{}, fmt.Errorf("parse duplex event: %w", err)
	}
	event := ServerEvent{
		Type:       strings.TrimSpace(envelope.Type),
		EventID:    strings.TrimSpace(envelope.EventID),
		Raw:        append(json.RawMessage(nil), raw...),
		Delta:      envelope.Delta,
		Text:       envelope.Text,
		Transcript: envelope.Transcript,
	}
	if envelope.Session != nil {
		event.SessionID = strings.TrimSpace(envelope.Session.ID)
	}
	audioB64 := strings.TrimSpace(envelope.Audio)
	if audioB64 == "" && envelope.Type == EventOutputAudioDelta {
		// Upstream puts PCM base64 in `delta` for response.output_audio.delta.
		audioB64 = strings.TrimSpace(envelope.Delta)
	}
	if audioB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(audioB64)
		if err != nil {
			return ServerEvent{}, fmt.Errorf("decode duplex audio delta: %w", err)
		}
		event.Audio = decoded
	}
	if envelope.Type == EventFunctionCallArgumentsDone {
		event.FunctionCalls = append([]FunctionCall(nil), envelope.Items...)
	}
	if envelope.Error != nil {
		event.ErrorMessage = strings.TrimSpace(envelope.Error.Message)
		if event.ErrorMessage == "" {
			event.ErrorMessage = strings.TrimSpace(envelope.Error.Code)
		}
	}
	if event.ErrorMessage == "" {
		event.ErrorMessage = strings.TrimSpace(envelope.Message)
	}
	return event, nil
}

func EncodeClientEvent(event ClientEvent) ([]byte, error) {
	if strings.TrimSpace(event.Type) == "" {
		return nil, fmt.Errorf("client event type is required")
	}
	return json.Marshal(event)
}
