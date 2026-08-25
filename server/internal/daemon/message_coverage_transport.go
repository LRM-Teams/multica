package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
)

const (
	MessageCoverageReceiptField = "_coverage_receipt"
	MessageCoverageCommitPath   = "/credential-proxy/messages/coverage/commit"
)

var (
	ErrCoverageReceiptScope = errors.New("coverage receipt belongs to another Inbox")
)

type credentialProxyCoverageCommitRequest struct {
	ReceiptID string `json:"receipt_id"`
}

// CommitCoverage authenticates one fixed Inbox before resolving the opaque
// receipt. Callers cannot select a Workspace or Agent through request fields.
func (p *CredentialProxy) CommitCoverage(agentProxyToken, receiptID string) error {
	if p == nil || p.daemon == nil {
		return errors.New("Credential Proxy is unavailable")
	}
	credential, err := p.daemon.authenticateAgentProxyToken(agentProxyToken)
	if err != nil {
		return err
	}
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" {
		err = ErrCoverageReceiptInvalid
		p.daemon.recordCoverageCommitDiagnostic(credential, err)
		return err
	}
	runner := p.daemon.currentWorkspaceDaemon(credential.Inbox.WorkspaceID)
	if runner == nil {
		err = ErrCoverageReceiptInvalid
		p.daemon.recordCoverageCommitDiagnostic(credential, err)
		return err
	}
	err = runner.commitMessageCoverage(credential.Inbox, receiptID)
	if errors.Is(err, ErrCoverageReceiptInvalid) {
		for _, candidateRunner := range p.daemon.currentWorkspaceDaemons() {
			if candidateRunner == runner {
				continue
			}
			if candidateRunner.ownsMessageCoverageReceipt(receiptID) {
				err = ErrCoverageReceiptScope
				p.daemon.recordCoverageCommitDiagnostic(credential, err)
				return err
			}
		}
	}
	p.daemon.recordCoverageCommitDiagnostic(credential, err)
	return err
}

func (c *MessageCoordinator) hasInboxKey(key InboxKey) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.key == key
}

func (d *Daemon) recordCoverageCommitDiagnostic(credential authenticatedAgentProxy, err error) {
	outcome := "accepted"
	reasonCode := ""
	switch {
	case errors.Is(err, ErrCoverageReceiptScope):
		outcome, reasonCode = "rejected", "coverage_receipt_scope_mismatch"
	case errors.Is(err, ErrCoverageReceiptInvalid):
		outcome, reasonCode = "rejected", "coverage_receipt_invalid"
	case errors.Is(err, ErrCoverageReceiptExpired):
		outcome, reasonCode = "rejected", "coverage_receipt_expired"
	case errors.Is(err, ErrCoverageReceiptInProgress):
		outcome, reasonCode = "deferred", "coverage_commit_in_progress"
	case err != nil:
		outcome, reasonCode = "failed", "context_boundary_persist_failed"
	}
	d.recordRunnerDiagnostic(credential.Inbox.WorkspaceID, diagnosticlog.Event{
		Name:      diagnosticlog.EventDeliveryStateChanged,
		Level:     diagnosticLevel(outcome),
		Component: "credential_proxy",
		Identity: diagnosticlog.Identity{
			AgentID:   credential.Inbox.AgentID,
			RuntimeID: credential.RuntimeID,
		},
		Fields: diagnosticlog.Fields{
			Phase: "coverage_commit", Outcome: outcome, ReasonCode: reasonCode,
		},
	})
}

func (d *Daemon) credentialProxyMessageCoverageCommitHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request credentialProxyCoverageCommitRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		request.ReceiptID = strings.TrimSpace(request.ReceiptID)
		if request.ReceiptID == "" {
			http.Error(w, "receipt_id is required", http.StatusBadRequest)
			return
		}
		err := d.CredentialProxy().CommitCoverage(r.Header.Get(AgentProxyTokenHeader), request.ReceiptID)
		switch {
		case errors.Is(err, ErrAgentProxyCredentialInvalid):
			if d.logger != nil {
				d.logger.Warn("Agent Proxy coverage commit rejected", "reason", "invalid_agent_proxy_credential")
			}
			http.Error(w, "invalid Agent Proxy credential", http.StatusUnauthorized)
			return
		case errors.Is(err, ErrCoverageReceiptScope):
			http.Error(w, "coverage receipt is outside the authenticated Inbox", http.StatusForbidden)
			return
		case errors.Is(err, ErrCoverageReceiptInvalid):
			http.Error(w, "invalid coverage receipt", http.StatusBadRequest)
			return
		case errors.Is(err, ErrCoverageReceiptExpired):
			http.Error(w, "coverage receipt expired", http.StatusGone)
			return
		case errors.Is(err, ErrCoverageReceiptInProgress):
			http.Error(w, "coverage receipt commit is in progress", http.StatusConflict)
			return
		case err != nil:
			http.Error(w, "coverage receipt commit failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "committed"})
	}
}
