package duplexcall

import "encoding/json"

// Client → server event types (FE contract for LRM-949 / LRM-950).
const (
	ClientAudioAppend = "client.audio.append"
	ClientAudioCommit = "client.audio.commit"
	ClientInterrupt   = "client.interrupt"
	ClientClose       = "client.close"
)

// Server → client event types.
const (
	ServerReady      = "duplex.ready"
	ServerASR        = "duplex.asr"
	ServerAudioDelta = "duplex.audio.delta"
	ServerTextDelta  = "duplex.text.delta"
	ServerTool       = "duplex.tool"
	ServerError      = "duplex.error"
	ServerClosed     = "duplex.closed"
)

// ClientEvent is one JSON text frame from the browser.
type ClientEvent struct {
	Type  string `json:"type"`
	Audio string `json:"audio,omitempty"` // base64 PCM s16le 16kHz mono
}

// ServerEvent is one JSON text frame to the browser.
type ServerEvent struct {
	Type        string `json:"type"`
	CallID      string `json:"call_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Transcript  string `json:"transcript,omitempty"`
	Phase       string `json:"phase,omitempty"` // started|completed for ASR
	Audio       string `json:"audio,omitempty"` // base64 PCM
	Text        string `json:"text,omitempty"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status,omitempty"` // started|done|error for tools
	Result      string `json:"result,omitempty"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message,omitempty"`
	SampleRate  int    `json:"sample_rate,omitempty"`
	AudioFormat string `json:"audio_format,omitempty"`
}

func EncodeServerEvent(event ServerEvent) ([]byte, error) {
	return json.Marshal(event)
}

func ParseClientEvent(data []byte) (ClientEvent, error) {
	var event ClientEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return ClientEvent{}, err
	}
	return event, nil
}
