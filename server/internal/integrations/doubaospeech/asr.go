package doubaospeech

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	asrSampleRate      = 16000
	asrBytesPerSample  = 2
	asrChannels        = 1
	asrPacketDuration  = 200 * time.Millisecond
	asrPacketSizeBytes = asrSampleRate * asrBytesPerSample * asrChannels / 5
)

type asrRequest struct {
	User struct {
		UID string `json:"uid"`
	} `json:"user"`
	Audio struct {
		Format  string `json:"format"`
		Codec   string `json:"codec"`
		Rate    int    `json:"rate"`
		Bits    int    `json:"bits"`
		Channel int    `json:"channel"`
	} `json:"audio"`
	Request struct {
		ModelName       string `json:"model_name"`
		EnableITN       bool   `json:"enable_itn"`
		EnablePunc      bool   `json:"enable_punc"`
		ShowUtterances  bool   `json:"show_utterances"`
		EnableNonstream bool   `json:"enable_nonstream"`
	} `json:"request"`
}

type asrResponse struct {
	Result struct {
		Text       string `json:"text"`
		Utterances []struct {
			Text string `json:"text"`
		} `json:"utterances"`
	} `json:"result"`
}

type asrReadResult struct {
	transcript Transcript
	err        error
}

func (c *Client) Transcribe(ctx context.Context, request TranscriptionRequest) (Transcript, error) {
	const operation = "asr"
	if err := c.configurationError(operation, false); err != nil {
		return Transcript{}, err
	}
	if len(request.PCM) == 0 {
		return Transcript{}, fmt.Errorf("doubao speech: PCM audio is required")
	}
	if len(request.PCM)%asrBytesPerSample != 0 {
		return Transcript{}, fmt.Errorf("doubao speech: PCM audio must contain complete 16-bit samples")
	}
	if request.SampleRate == 0 {
		request.SampleRate = asrSampleRate
	}
	if request.SampleRate != asrSampleRate {
		return Transcript{}, fmt.Errorf("doubao speech: ASR requires 16000 Hz PCM, got %d", request.SampleRate)
	}

	conn, logID, err := c.dial(ctx, c.asrEndpoint, ASRResourceID, operation)
	if err != nil {
		return Transcript{}, err
	}
	defer closeWebsocket(conn)

	config := asrRequest{}
	config.User.UID = newRequestID()
	config.Audio.Format = "pcm"
	config.Audio.Codec = "raw"
	config.Audio.Rate = asrSampleRate
	config.Audio.Bits = 16
	config.Audio.Channel = asrChannels
	config.Request.ModelName = "bigmodel"
	config.Request.EnableITN = true
	config.Request.EnablePunc = true
	config.Request.ShowUtterances = true
	config.Request.EnableNonstream = true
	configPayload, err := encodeJSON(config)
	if err != nil {
		return Transcript{}, err
	}
	configFrame, err := marshalASRFullRequest(1, configPayload)
	if err != nil {
		return Transcript{}, err
	}
	if err := writeBinary(conn, configFrame, operation, logID); err != nil {
		return Transcript{}, err
	}

	latestText := ""
	initial, err := readFrame(conn, operation, logID)
	if err != nil {
		return Transcript{}, err
	}
	if text, err := asrFrameText(initial); err != nil {
		return Transcript{}, &ProviderError{Operation: operation, LogID: logID, Message: "decode initial response", Err: err}
	} else if text != "" {
		latestText = text
	}
	if initial.last {
		return Transcript{}, &ProviderError{Operation: operation, LogID: logID, Message: "provider completed before receiving audio"}
	}

	// Optimized streaming ASR emits partial responses while audio is still
	// arriving. A dedicated reader is required: if the client only reads after
	// sending a long recording, the TCP receive buffer can fill and block the
	// provider from reading the remaining audio.
	readResult := make(chan asrReadResult, 1)
	go func(initialText string) {
		transcript, err := readASRResult(conn, operation, logID, initialText)
		readResult <- asrReadResult{transcript: transcript, err: err}
	}(latestText)

	sequence := int32(2)
	for offset := 0; offset < len(request.PCM); offset += asrPacketSizeBytes {
		select {
		case result := <-readResult:
			if result.err != nil {
				return Transcript{}, result.err
			}
			return Transcript{}, &ProviderError{Operation: operation, LogID: logID, Message: "provider completed before the final audio frame"}
		default:
		}
		end := offset + asrPacketSizeBytes
		if end > len(request.PCM) {
			end = len(request.PCM)
		}
		last := end == len(request.PCM)
		frame, err := marshalASRAudio(sequence, last, request.PCM[offset:end])
		if err != nil {
			return Transcript{}, err
		}
		if err := writeBinary(conn, frame, operation, logID); err != nil {
			return Transcript{}, err
		}
		if !last {
			if err := c.asrPace(ctx, asrPacketDuration); err != nil {
				return Transcript{}, &ProviderError{Operation: operation, LogID: logID, Message: "audio stream interrupted", Err: err}
			}
		}
		sequence++
	}

	select {
	case result := <-readResult:
		return result.transcript, result.err
	case <-ctx.Done():
		return Transcript{}, &ProviderError{Operation: operation, LogID: logID, Message: "recognition interrupted", Err: ctx.Err()}
	}
}

func readASRResult(conn *websocket.Conn, operation, logID, latestText string) (Transcript, error) {
	for {
		frame, err := readFrame(conn, operation, logID)
		if err != nil {
			return Transcript{}, err
		}
		text, err := asrFrameText(frame)
		if err != nil {
			return Transcript{}, &ProviderError{Operation: operation, LogID: logID, Message: "decode recognition response", Err: err}
		}
		if text != "" {
			latestText = text
		}
		if frame.last {
			return Transcript{Text: latestText, LogID: logID}, nil
		}
	}
}

func asrFrameText(frame providerFrame) (string, error) {
	if frame.messageType != messageFullServer || len(frame.payload) == 0 {
		return "", nil
	}
	var response asrResponse
	if err := json.Unmarshal(frame.payload, &response); err != nil {
		return "", err
	}
	if text := strings.TrimSpace(response.Result.Text); text != "" {
		return text, nil
	}
	parts := make([]string, 0, len(response.Result.Utterances))
	for _, utterance := range response.Result.Utterances {
		if text := strings.TrimSpace(utterance.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ""), nil
}

func newRequestID() string {
	return uuid.NewString()
}
