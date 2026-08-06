package doubaospeech

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSynthesizeUsesTTS2ProtocolAndReturnsAudio(t *testing.T) {
	const apiKey = "test-api-key"
	const speakerID = "test-speaker"
	wantAudio := []byte("fake-mp3-audio")

	server := newWebsocketTestServer(t, func(conn *websocket.Conn, request *http.Request) {
		assertProviderHeaders(t, request, apiKey, TTSResourceID)
		startConnection := readTestFrame(t, conn)
		if startConnection.event != eventStartConnection {
			t.Fatalf("event = %d, want StartConnection", startConnection.event)
		}
		writeTestFrame(t, conn, marshalTTSServerEvent(messageFullServer, eventConnectionStarted, "", "connection-1", []byte(`{}`)))

		startSession := readTestFrame(t, conn)
		if startSession.event != eventStartSession || startSession.sessionID == "" {
			t.Fatalf("unexpected start-session frame: %+v", startSession)
		}
		var sessionRequest ttsSessionRequest
		if err := json.Unmarshal(startSession.payload, &sessionRequest); err != nil {
			t.Fatal(err)
		}
		if sessionRequest.ReqParams.Speaker != speakerID || sessionRequest.ReqParams.AudioParams.Format != "mp3" {
			t.Fatalf("unexpected session request: %+v", sessionRequest)
		}
		writeTestFrame(t, conn, marshalTTSServerEvent(messageFullServer, eventSessionStarted, startSession.sessionID, "", []byte(`{}`)))

		task := readTestFrame(t, conn)
		finishSession := readTestFrame(t, conn)
		if task.event != eventTaskRequest || finishSession.event != eventFinishSession {
			t.Fatalf("unexpected task events: %d, %d", task.event, finishSession.event)
		}
		var taskRequest ttsTaskRequest
		if err := json.Unmarshal(task.payload, &taskRequest); err != nil {
			t.Fatal(err)
		}
		if taskRequest.ReqParams.Text != "你好" {
			t.Fatalf("task text = %q", taskRequest.ReqParams.Text)
		}
		writeTestFrame(t, conn, marshalTTSServerEvent(messageAudioServer, eventTTSResponse, startSession.sessionID, "", wantAudio))
		writeTestFrame(t, conn, marshalTTSServerEvent(messageFullServer, eventSessionFinished, startSession.sessionID, "", []byte(`{}`)))

		finishConnection := readTestFrame(t, conn)
		if finishConnection.event != eventFinishConnection {
			t.Fatalf("event = %d, want FinishConnection", finishConnection.event)
		}
		writeTestFrame(t, conn, marshalTTSServerEvent(messageFullServer, eventConnectionFinished, "", "connection-1", []byte(`{}`)))
	})
	defer server.Close()

	client := New(Config{
		APIKey: apiKey, SpeakerID: speakerID,
		TTSEndpoint: websocketURL(server.URL),
		Timeout:     3 * time.Second,
	})
	audio, err := client.Synthesize(context.Background(), SynthesisRequest{Text: "你好", Format: "mp3", SampleRate: 24000})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(audio.Data, wantAudio) || audio.Format != "mp3" || audio.SampleRate != 24000 {
		t.Fatalf("unexpected audio response: %+v", audio)
	}
}

func TestTranscribeUsesASR2ProtocolAndReturnsLatestTranscript(t *testing.T) {
	const apiKey = "test-api-key"
	pcm := make([]byte, asrPacketSizeBytes+200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}

	server := newWebsocketTestServer(t, func(conn *websocket.Conn, request *http.Request) {
		assertProviderHeaders(t, request, apiKey, ASRResourceID)
		configFrame := readTestFrame(t, conn)
		if configFrame.sequence != 1 || configFrame.messageType != messageFullClient {
			t.Fatalf("unexpected config frame: %+v", configFrame)
		}
		var config asrRequest
		if err := json.Unmarshal(configFrame.payload, &config); err != nil {
			t.Fatal(err)
		}
		if config.Audio.Format != "pcm" || config.Audio.Rate != 16000 || config.Audio.Bits != 16 || config.Audio.Channel != 1 || config.Request.ModelName != "bigmodel" || !config.Request.EnableNonstream {
			t.Fatalf("unexpected ASR config: %+v", config)
		}
		writeTestFrame(t, conn, marshalASRServerResponse(t, 1, false, map[string]any{"result": map[string]any{}}))

		var received []byte
		packetCount := 0
		for {
			frame := readTestFrame(t, conn)
			received = append(received, frame.payload...)
			packetCount++
			if packetCount == 1 {
				writeTestFrame(t, conn, marshalASRServerResponse(t, 2, false, map[string]any{
					"result": map[string]any{"text": "你好"},
				}))
			}
			if frame.last {
				break
			}
		}
		if !bytes.Equal(received, pcm) {
			t.Fatalf("received %d PCM bytes, want %d", len(received), len(pcm))
		}
		writeTestFrame(t, conn, marshalASRServerResponse(t, 4, true, map[string]any{
			"result": map[string]any{"text": "你好，贝克汉姆。"},
		}))
	})
	defer server.Close()

	client := New(Config{
		APIKey:      apiKey,
		ASREndpoint: websocketURL(server.URL),
		Timeout:     3 * time.Second,
		ASRPace:     func(context.Context, time.Duration) error { return nil },
	})
	transcript, err := client.Transcribe(context.Background(), TranscriptionRequest{PCM: pcm, SampleRate: 16000})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "你好，贝克汉姆。" {
		t.Fatalf("text = %q", transcript.Text)
	}
}

func TestProviderErrorsDoNotExposeAPIKey(t *testing.T) {
	const apiKey = "must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := New(Config{APIKey: apiKey, SpeakerID: "speaker", TTSEndpoint: websocketURL(server.URL), Timeout: time.Second})
	_, err := client.Synthesize(context.Background(), SynthesisRequest{Text: "test"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("error exposed API key: %v", err)
	}
}

func newWebsocketTestServer(t *testing.T, handle func(*websocket.Conn, *http.Request)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		handle(conn, request)
	}))
}

func assertProviderHeaders(t *testing.T, request *http.Request, apiKey, resourceID string) {
	t.Helper()
	if got := request.Header.Get("X-Api-Key"); got != apiKey {
		t.Fatalf("X-Api-Key = %q", got)
	}
	if got := request.Header.Get("X-Api-Resource-Id"); got != resourceID {
		t.Fatalf("X-Api-Resource-Id = %q, want %q", got, resourceID)
	}
	if request.Header.Get("X-Api-Connect-Id") == "" {
		t.Fatal("X-Api-Connect-Id is empty")
	}
}

func readTestFrame(t *testing.T, conn *websocket.Conn) providerFrame {
	t.Helper()
	messageType, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("websocket message type = %d", messageType)
	}
	frame, err := parseProviderFrame(raw)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func writeTestFrame(t *testing.T, conn *websocket.Conn, raw []byte) {
	t.Helper()
	if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatal(err)
	}
}

func websocketURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
