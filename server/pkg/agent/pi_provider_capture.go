package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Typed resident-capture event kinds emitted by the application-generated Pi
// capture extension and consumed by the daemon before any upload. These are
// not agent-declared provenance; they are read-only observations of Pi's
// final provider boundary.
const (
	PiCaptureEventProviderRequest = "provider_request"
	PiCaptureEventFinalAssistant  = "final_assistant"
	PiCaptureEventTurnEnd         = "turn_end"
	PiCaptureEventVisibleAction   = "visible_action"
	PiCaptureEventConsumption     = "consumption"
	PiCaptureEventTurnStatus      = "turn_status"
	PiCaptureEventCaptureBatch    = "capture_batch"
	PiCaptureEventProviderCall    = "provider_call"
)

// piCaptureRecord is emitted by the daemon-created Pi extension. Its payload
// is intentionally parsed and redacted by Go before it can leave the daemon.
type piCaptureRecord struct {
	Kind    string          `json:"kind"`
	At      string          `json:"at"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
}

// PiCaptureTypedEvent is the daemon-facing typed view of one resident capture
// JSONL record. Callers may marshal these for diagnostics; trusted upload uses
// ResidentTurnCapture instead.
type PiCaptureTypedEvent struct {
	Kind    string          `json:"kind"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	TurnID  string          `json:"turn_id,omitempty"`
	BatchID string          `json:"capture_batch_id,omitempty"`
	Status  string          `json:"status,omitempty"`
}

func buildResidentTurnCapture(binding PiRunBinding, turnOrdinal, firstCallOrdinal int64, records []piCaptureRecord) (ResidentTurnCapture, error) {
	requests, finalMessages, err := pairPiCaptureProviderCalls(records)
	if err != nil {
		return ResidentTurnCapture{}, err
	}
	if len(requests) == 0 || len(requests) != len(finalMessages) {
		return ResidentTurnCapture{}, fmt.Errorf("incomplete Pi capture: provider_requests=%d final_messages=%d", len(requests), len(finalMessages))
	}
	if turnOrdinal <= 0 || firstCallOrdinal <= 0 {
		return ResidentTurnCapture{}, errors.New("Pi capture requires positive turn and call ordinals")
	}
	capture := residentTurnCaptureIdentity(binding, turnOrdinal)
	turnID := capture.TurnID
	for index, requestRecord := range requests {
		request, err := redactPiCaptureJSON(requestRecord.Payload)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("redact provider request %d: %w", index, err)
		}
		finalRaw := finalMessages[index].Message
		if len(finalRaw) == 0 {
			finalRaw = finalMessages[index].Payload
		}
		finalMessage, err := normalizePiFinalAssistantMessage(finalRaw)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("normalize final assistant message %d: %w", index, err)
		}
		finalMessage, err = redactPiCaptureJSON(finalMessage)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("redact final assistant message %d: %w", index, err)
		}
		var metadata struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		_ = json.Unmarshal(request, &metadata)
		var final struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(finalMessage, &final)
		startedAt, err := time.Parse(time.RFC3339Nano, requestRecord.At)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("parse provider request timestamp %d: %w", index, err)
		}
		completedAt, err := time.Parse(time.RFC3339Nano, finalMessages[index].At)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("parse final assistant timestamp %d: %w", index, err)
		}
		if capture.StartedAt.IsZero() || startedAt.Before(capture.StartedAt) {
			capture.StartedAt = startedAt
		}
		if completedAt.After(capture.CompletedAt) {
			capture.CompletedAt = completedAt
		}
		callOrdinal := firstCallOrdinal + int64(index)
		callID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(turnID+":call:"+fmt.Sprint(callOrdinal))).String()
		capture.ProviderCalls = append(capture.ProviderCalls, ResidentProviderCallCapture{
			CallID:                callID,
			CallOrdinal:           callOrdinal,
			Provider:              metadata.Provider,
			Model:                 metadata.Model,
			APIKind:               "messages",
			RawProviderRequest:    request,
			FinalAssistantMessage: finalMessage,
			Status:                "completed",
			StopReason:            final.StopReason,
			ResponseComplete:      true,
			RequestHash:           sha256JSON(request),
			ResponseHash:          sha256JSON(finalMessage),
			StartedAt:             startedAt,
			CompletedAt:           completedAt,
		})
	}
	integrity, err := json.Marshal(struct {
		RunID           string                        `json:"run_id"`
		RunAgentID      string                        `json:"run_agent_id"`
		SessionID       string                        `json:"pi_session_id"`
		CaptureBoundary string                        `json:"capture_boundary"`
		TurnID          string                        `json:"turn_id"`
		TurnOrdinal     int64                         `json:"turn_ordinal"`
		ProviderCalls   []ResidentProviderCallCapture `json:"provider_calls"`
	}{capture.RunID, capture.RunAgentID, capture.PiSessionID, capture.CaptureBoundary, capture.TurnID, capture.TurnOrdinal, capture.ProviderCalls})
	if err != nil {
		return ResidentTurnCapture{}, fmt.Errorf("canonicalize Pi capture: %w", err)
	}
	capture.PayloadHash = sha256JSON(integrity)
	capture.Complete = true
	return capture, nil
}

func residentTurnCaptureIdentity(binding PiRunBinding, turnOrdinal int64) ResidentTurnCapture {
	turnID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(binding.RunAgentID+":turn:"+fmt.Sprint(turnOrdinal))).String()
	return ResidentTurnCapture{
		RunID:           binding.RunID,
		RunAgentID:      binding.RunAgentID,
		PiSessionID:     binding.SessionID,
		CaptureBoundary: binding.CaptureBoundary,
		TurnID:          turnID,
		CaptureBatchID:  uuid.NewSHA1(uuid.NameSpaceURL, []byte("capture:"+turnID)).String(),
		TurnOrdinal:     turnOrdinal,
	}
}

func sha256JSON(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func redactPiCaptureJSON(raw json.RawMessage) (json.RawMessage, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid JSON")
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	redactPiCaptureValue(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func redactPiCaptureValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			switch normalized {
			case "authorization", "api_key", "apikey", "access_token", "token", "password", "secret", "x_api_key":
				delete(typed, key)
			default:
				redactPiCaptureValue(child)
			}
		}
	case []any:
		for _, child := range typed {
			redactPiCaptureValue(child)
		}
	}
}

func normalizePiFinalAssistantMessage(raw json.RawMessage) (json.RawMessage, error) {
	redacted, err := redactPiCaptureJSON(raw)
	if err != nil {
		return nil, err
	}
	var message map[string]any
	if err := json.Unmarshal(redacted, &message); err != nil {
		return nil, err
	}
	if _, hasBlocks := message["blocks"]; !hasBlocks {
		if content, ok := message["content"].([]any); ok {
			message["blocks"] = content
			delete(message, "content")
		}
	} else {
		delete(message, "content")
	}
	if role, _ := message["role"].(string); role == "" {
		message["role"] = "assistant"
	}
	return json.Marshal(message)
}

// pairPiCaptureProviderCalls requires one unambiguous provider request for
// each final assistant message. Pi does not provide stable retry identifiers,
// so multiple requests before one final cannot be safely paired.
func pairPiCaptureProviderCalls(records []piCaptureRecord) ([]piCaptureRecord, []piCaptureRecord, error) {
	var requests, finals []piCaptureRecord
	var pending *piCaptureRecord
	for _, record := range records {
		switch record.Kind {
		case PiCaptureEventProviderRequest, PiCaptureEventProviderCall:
			if pending != nil {
				return nil, nil, errors.New("ambiguous Pi capture: multiple provider requests before a final assistant message")
			}
			copy := record
			pending = &copy
		case PiCaptureEventTurnEnd, PiCaptureEventFinalAssistant:
			if pending == nil {
				continue
			}
			requests = append(requests, *pending)
			finals = append(finals, record)
			pending = nil
		}
	}
	if pending != nil {
		return nil, nil, errors.New("incomplete Pi capture: provider request has no final assistant message")
	}
	return requests, finals, nil
}

// typedPiCaptureEvents projects raw JSONL records into the resident RPC typed
// event surface used by capture-batch assembly and diagnostics.
func typedPiCaptureEvents(records []piCaptureRecord) ([]PiCaptureTypedEvent, error) {
	events := make([]PiCaptureTypedEvent, 0, len(records))
	for _, record := range records {
		at, err := time.Parse(time.RFC3339Nano, record.At)
		if err != nil {
			return nil, fmt.Errorf("parse capture event timestamp: %w", err)
		}
		kind := record.Kind
		switch kind {
		case PiCaptureEventTurnEnd:
			kind = PiCaptureEventFinalAssistant
		case PiCaptureEventProviderRequest:
			kind = PiCaptureEventProviderCall
		}
		events = append(events, PiCaptureTypedEvent{
			Kind:    kind,
			At:      at,
			Payload: record.Payload,
			Message: record.Message,
		})
	}
	return events, nil
}

// newPiCaptureExtension writes a read-only Pi extension that records only the
// final provider request and final assistant message boundaries. It must never
// register tools, mutate events, or subscribe to SSE/token streams.
func newPiCaptureExtension() (extensionPath string, captureLogPath string, err error) {
	logFile, err := os.CreateTemp("", "multica-pi-capture-*.jsonl")
	if err != nil {
		return "", "", fmt.Errorf("create Pi capture log: %w", err)
	}
	captureLogPath = logFile.Name()
	if err := logFile.Close(); err != nil {
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("close Pi capture log: %w", err)
	}
	if err := os.Chmod(captureLogPath, 0o600); err != nil {
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("secure Pi capture log: %w", err)
	}
	extension, err := os.CreateTemp("", "multica-pi-capture-extension-*.mjs")
	if err != nil {
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("create Pi capture extension: %w", err)
	}
	extensionPath = extension.Name()
	encodedPath, err := json.Marshal(captureLogPath)
	if err != nil {
		_ = extension.Close()
		_ = os.Remove(extensionPath)
		_ = os.Remove(captureLogPath)
		return "", "", err
	}
	source := fmt.Sprintf(`import { appendFileSync } from "node:fs";
const capturePath = %s;
const record = (kind, fields) => {
  try { appendFileSync(capturePath, JSON.stringify({ kind, at: new Date().toISOString(), ...fields }) + "\\n", { encoding: "utf8", mode: 0o600 }); } catch (_) {}
};
export default function (pi) {
  // Read-only final-boundary capture. Do not register tools, mutate events,
  // or subscribe to streaming/token deltas.
  pi.on("before_provider_request", (event) => { record("provider_request", { payload: event.payload }); });
  pi.on("turn_end", (event) => { record("turn_end", { message: event.message }); });
}
`, string(encodedPath))
	if _, err := io.WriteString(extension, source); err != nil {
		_ = extension.Close()
		_ = os.Remove(extensionPath)
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("write Pi capture extension: %w", err)
	}
	if err := extension.Close(); err != nil {
		_ = os.Remove(extensionPath)
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("close Pi capture extension: %w", err)
	}
	if err := os.Chmod(extensionPath, 0o600); err != nil {
		_ = os.Remove(extensionPath)
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("secure Pi capture extension: %w", err)
	}
	return extensionPath, captureLogPath, nil
}

func readPiCaptureRecords(path string, offset int64) ([]piCaptureRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024), 32*1024*1024)
	var records []piCaptureRecord
	for scanner.Scan() {
		var record piCaptureRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("decode Pi capture record: %w", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
