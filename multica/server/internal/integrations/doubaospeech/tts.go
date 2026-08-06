package doubaospeech

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const maxSynthesizedAudioBytes = 32 << 20

type ttsSessionRequest struct {
	User struct {
		UID string `json:"uid"`
	} `json:"user"`
	Namespace string `json:"namespace"`
	ReqParams struct {
		Speaker     string `json:"speaker"`
		AudioParams struct {
			Format     string `json:"format"`
			SampleRate int    `json:"sample_rate"`
		} `json:"audio_params"`
	} `json:"req_params"`
}

type ttsTaskRequest struct {
	ReqParams struct {
		Text string `json:"text"`
	} `json:"req_params"`
}

func (c *Client) Synthesize(ctx context.Context, request SynthesisRequest) (Audio, error) {
	const operation = "tts"
	if err := c.configurationError(operation, true); err != nil {
		return Audio{}, err
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" {
		return Audio{}, fmt.Errorf("doubao speech: TTS text is required")
	}
	if request.Format == "" {
		request.Format = "mp3"
	}
	if request.Format != "mp3" && request.Format != "pcm" {
		return Audio{}, fmt.Errorf("doubao speech: unsupported TTS format %q", request.Format)
	}
	if request.SampleRate == 0 {
		request.SampleRate = 24000
	}
	if request.SampleRate != 16000 && request.SampleRate != 24000 {
		return Audio{}, fmt.Errorf("doubao speech: unsupported TTS sample rate %d", request.SampleRate)
	}

	conn, logID, err := c.dial(ctx, c.ttsEndpoint, TTSResourceID, operation)
	if err != nil {
		return Audio{}, err
	}
	defer closeWebsocket(conn)

	emptyPayload := []byte("{}")
	if err := writeBinary(conn, marshalTTSEvent(eventStartConnection, "", emptyPayload), operation, logID); err != nil {
		return Audio{}, err
	}
	if _, err := expectTTSEvent(conn, eventConnectionStarted, operation, logID); err != nil {
		return Audio{}, err
	}

	sessionID := uuid.NewString()
	sessionRequest := ttsSessionRequest{Namespace: "BidirectionalTTS"}
	sessionRequest.User.UID = uuid.NewString()
	sessionRequest.ReqParams.Speaker = c.speakerID
	sessionRequest.ReqParams.AudioParams.Format = request.Format
	sessionRequest.ReqParams.AudioParams.SampleRate = request.SampleRate
	sessionPayload, err := encodeJSON(sessionRequest)
	if err != nil {
		return Audio{}, err
	}
	if err := writeBinary(conn, marshalTTSEvent(eventStartSession, sessionID, sessionPayload), operation, logID); err != nil {
		return Audio{}, err
	}
	if _, err := expectTTSEvent(conn, eventSessionStarted, operation, logID); err != nil {
		return Audio{}, err
	}

	taskRequest := ttsTaskRequest{}
	taskRequest.ReqParams.Text = request.Text
	taskPayload, err := encodeJSON(taskRequest)
	if err != nil {
		return Audio{}, err
	}
	if err := writeBinary(conn, marshalTTSEvent(eventTaskRequest, sessionID, taskPayload), operation, logID); err != nil {
		return Audio{}, err
	}
	if err := writeBinary(conn, marshalTTSEvent(eventFinishSession, sessionID, emptyPayload), operation, logID); err != nil {
		return Audio{}, err
	}

	audio := make([]byte, 0, 64*1024)
	for {
		frame, err := readFrame(conn, operation, logID)
		if err != nil {
			return Audio{}, err
		}
		if frame.sessionID != "" && frame.sessionID != sessionID {
			return Audio{}, &ProviderError{Operation: operation, LogID: logID, Message: "provider returned a different session ID"}
		}
		switch {
		case frame.messageType == messageAudioServer && frame.event == eventTTSResponse:
			if len(frame.payload) > maxSynthesizedAudioBytes-len(audio) {
				return Audio{}, &ProviderError{Operation: operation, LogID: logID, Message: "provider audio exceeds 32 MiB"}
			}
			audio = append(audio, frame.payload...)
		case frame.event == eventSessionFailed:
			return Audio{}, ttsFailure(operation, logID, frame.payload)
		case frame.event == eventSessionFinished:
			if len(audio) == 0 {
				return Audio{}, &ProviderError{Operation: operation, LogID: logID, Message: "provider completed without audio"}
			}
			if err := writeBinary(conn, marshalTTSEvent(eventFinishConnection, "", emptyPayload), operation, logID); err != nil {
				return Audio{}, err
			}
			if _, err := expectTTSEvent(conn, eventConnectionFinished, operation, logID); err != nil {
				return Audio{}, err
			}
			return Audio{Data: audio, Format: request.Format, SampleRate: request.SampleRate, LogID: logID}, nil
		}
	}
}

func expectTTSEvent(conn *websocket.Conn, expected int32, operation, logID string) (providerFrame, error) {
	for {
		frame, err := readFrame(conn, operation, logID)
		if err != nil {
			return providerFrame{}, err
		}
		if frame.event == eventConnectionFailed || frame.event == eventSessionFailed {
			return providerFrame{}, ttsFailure(operation, logID, frame.payload)
		}
		if frame.event == expected {
			return frame, nil
		}
	}
}

func ttsFailure(operation, logID string, payload []byte) error {
	var response struct {
		Code    uint32 `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &response); err == nil && (response.Code != 0 || response.Message != "") {
		return &ProviderError{Operation: operation, Code: response.Code, LogID: logID, Message: response.Message}
	}
	return &ProviderError{Operation: operation, LogID: logID, Message: providerMessage(payload)}
}
