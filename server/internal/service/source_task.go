package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type SourceTaskType string

const (
	SourceTaskIssue   SourceTaskType = "issue"
	SourceTaskMessage SourceTaskType = "message"
)

type SourceTask struct {
	ID          string
	WorkspaceID string
	Type        SourceTaskType
	Payload     json.RawMessage
	ContentHash string
}

type SourceTaskInput struct {
	Type        SourceTaskType
	Payload     json.RawMessage
	ContentHash string
}

type sourceTaskIssuePayload struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	RepoURL            string   `json:"repo_url,omitempty"`
	BaseCommit         string   `json:"base_commit,omitempty"`
	IssueDate          string   `json:"issue_date,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	FailToPass         []string `json:"fail_to_pass,omitempty"`
	PassToPass         []string `json:"pass_to_pass,omitempty"`
}

type sourceTaskMessagePayload struct {
	Content string `json:"content"`
}

// IssueInput materializes this source task as an issue dispatch payload. It
// rejects an unexpected source type and re-validates the canonical payload so
// callers never create local content from unchecked JSON.
func (s SourceTask) IssueInput() (*IssueInput, error) {
	if s.Type != SourceTaskIssue {
		return nil, fmt.Errorf("source task type %q cannot materialize an issue", s.Type)
	}
	parsed, err := ParseSourceTask(string(s.Type), s.Payload)
	if err != nil {
		return nil, err
	}
	var payload sourceTaskIssuePayload
	if err := json.Unmarshal(parsed.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode canonical issue source task: %w", err)
	}
	return &IssueInput{
		Title:              payload.Title,
		Description:        payload.Description,
		AcceptanceCriteria: payload.AcceptanceCriteria,
		FailToPass:         payload.FailToPass,
		PassToPass:         payload.PassToPass,
	}, nil
}

// MessageInput materializes this source task as a message dispatch payload.
func (s SourceTask) MessageInput() (*MessageInput, error) {
	if s.Type != SourceTaskMessage {
		return nil, fmt.Errorf("source task type %q cannot materialize a message", s.Type)
	}
	parsed, err := ParseSourceTask(string(s.Type), s.Payload)
	if err != nil {
		return nil, err
	}
	var payload sourceTaskMessagePayload
	if err := json.Unmarshal(parsed.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode canonical message source task: %w", err)
	}
	return &MessageInput{Content: payload.Content}, nil
}

// ParseSourceTask validates, canonicalizes, and hashes immutable source-task
// input. The hash includes both the type and canonical payload so a source task
// has one stable workspace-scoped identity across repeated registrations.
func ParseSourceTask(taskType string, payload json.RawMessage) (SourceTaskInput, error) {
	parsedType := SourceTaskType(taskType)
	if parsedType != SourceTaskIssue && parsedType != SourceTaskMessage {
		return SourceTaskInput{}, fmt.Errorf("source task type must be issue or message")
	}
	if !isJSONObject(payload) {
		return SourceTaskInput{}, fmt.Errorf("source task payload must be a JSON object")
	}

	var canonical any
	switch parsedType {
	case SourceTaskIssue:
		var issue sourceTaskIssuePayload
		if err := json.Unmarshal(payload, &issue); err != nil {
			return SourceTaskInput{}, fmt.Errorf("decode issue source task payload: %w", err)
		}
		if strings.TrimSpace(issue.Title) == "" {
			return SourceTaskInput{}, fmt.Errorf("issue source task title is required")
		}
		if strings.TrimSpace(issue.Description) == "" {
			return SourceTaskInput{}, fmt.Errorf("issue source task description is required")
		}
		if issue.IssueDate != "" {
			if _, err := time.Parse(time.RFC3339, issue.IssueDate); err != nil {
				return SourceTaskInput{}, fmt.Errorf("issue source task issue_date must be RFC3339")
			}
		}
		canonical = issue
	case SourceTaskMessage:

		var message sourceTaskMessagePayload
		if err := json.Unmarshal(payload, &message); err != nil {
			return SourceTaskInput{}, fmt.Errorf("decode message source task payload: %w", err)
		}
		if strings.TrimSpace(message.Content) == "" {
			return SourceTaskInput{}, fmt.Errorf("message source task content is required")
		}
		canonical = message
	}

	canonicalPayload, err := json.Marshal(canonical)
	if err != nil {
		return SourceTaskInput{}, fmt.Errorf("canonicalize source task payload: %w", err)
	}
	hash := sha256.Sum256(append(append([]byte(parsedType), '\n'), canonicalPayload...))
	return SourceTaskInput{
		Type:        parsedType,
		Payload:     canonicalPayload,
		ContentHash: hex.EncodeToString(hash[:]),
	}, nil
}

func isJSONObject(payload json.RawMessage) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return false
	}
	return true
}
