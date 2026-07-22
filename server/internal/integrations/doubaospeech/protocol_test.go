package doubaospeech

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestASRAudioFrameRoundTrip(t *testing.T) {
	payload := []byte{0, 1, 2, 3, 4, 5}
	raw, err := marshalASRAudio(7, true, payload)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := parseProviderFrame(raw)
	if err != nil {
		t.Fatal(err)
	}
	if frame.messageType != messageAudioClient || frame.sequence != -7 || !frame.last {
		t.Fatalf("unexpected frame metadata: %+v", frame)
	}
	if !bytes.Equal(frame.payload, payload) {
		t.Fatalf("payload = %v, want %v", frame.payload, payload)
	}
}

func TestTTSEventFrameRoundTrip(t *testing.T) {
	payload := []byte(`{"req_params":{"text":"hello"}}`)
	raw := marshalTTSEvent(eventTaskRequest, "session-1", payload)
	frame, err := parseProviderFrame(raw)
	if err != nil {
		t.Fatal(err)
	}
	if frame.event != eventTaskRequest || frame.sessionID != "session-1" {
		t.Fatalf("unexpected event frame: %+v", frame)
	}
	if !bytes.Equal(frame.payload, payload) {
		t.Fatalf("payload = %q, want %q", frame.payload, payload)
	}
}

func TestParseProviderFrameRejectsTruncatedPayload(t *testing.T) {
	raw := frameHeader(messageFullServer, 0, serializationJSON, compressionNone)
	raw = appendUint32(raw, 20)
	raw = append(raw, []byte(`{"short":true}`)...)
	_, err := parseProviderFrame(raw)
	if !errors.Is(err, errMalformedFrame) {
		t.Fatalf("error = %v, want errMalformedFrame", err)
	}
}

func marshalASRServerResponse(t *testing.T, sequence int32, last bool, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := gzipBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	flags := flagPositiveSequence
	if last {
		flags = flagSequence
		sequence = -sequence
	}
	raw := frameHeader(messageFullServer, flags, serializationJSON, compressionGZIP)
	raw = appendInt32(raw, sequence)
	return appendSizedBytes(raw, compressed)
}

func marshalTTSServerEvent(messageType byte, event int32, sessionID, connectionID string, payload []byte) []byte {
	serialization := serializationJSON
	if messageType == messageAudioServer {
		serialization = serializationNone
	}
	raw := frameHeader(messageType, flagEvent, serialization, compressionNone)
	raw = appendInt32(raw, event)
	if !isConnectionEvent(event) {
		raw = appendSizedBytes(raw, []byte(sessionID))
	}
	if event == eventConnectionStarted || event == eventConnectionFailed || event == eventConnectionFinished {
		raw = appendSizedBytes(raw, []byte(connectionID))
	}
	return appendSizedBytes(raw, payload)
}
