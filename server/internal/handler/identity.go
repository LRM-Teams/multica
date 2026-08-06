package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/identityhandle"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var errIdentityHandleExhausted = errors.New("identity handle generation exhausted")
var errIdentityHandleInvalid = identityhandle.ErrInvalid

const maxIdentityHandleLength = identityhandle.MaxLength

func identityHandleBase(value, fallback string) string {
	return identityhandle.Base(value, fallback)
}

func validateIdentityHandle(handle string) error {
	return identityhandle.Validate(handle)
}

func identityHandleCandidate(base string, attempt int) string {
	return identityhandle.Candidate(base, attempt)
}

func identityUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func userDisplayName(u db.User) string {
	if name := strings.TrimSpace(u.DisplayName); name != "" {
		return name
	}
	if name := strings.TrimSpace(u.Name); name != "" {
		return name
	}
	return strings.TrimSpace(u.Email)
}

// userName resolves the display name used by handler-side system messages.
// It belongs with the shared identity helpers rather than a particular chat
// transport: membership and other system events continue to need it after the
// retired agent-DM transport is removed.
func (h *Handler) userName(ctx context.Context, id pgtype.UUID) string {
	var name string
	_ = h.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(to_jsonb(u)->>'display_name', ''), NULLIF(u.name, ''), u.email, '') FROM "user" u WHERE id = $1`, id).Scan(&name)
	return name
}

func agentDisplayName(a db.Agent) string {
	if name := strings.TrimSpace(a.DisplayName); name != "" {
		return name
	}
	if name := strings.TrimSpace(a.Name); name != "" {
		return name
	}
	return "Agent"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (h *Handler) createUserWithIdentity(ctx context.Context, email, displaySeed string, avatar pgtype.Text) (db.User, error) {
	displayName := strings.TrimSpace(displaySeed)
	if displayName == "" {
		displayName = emailLocalPart(email)
	}
	if displayName == "" {
		displayName = strings.TrimSpace(email)
	}
	base := identityHandleBase(emailLocalPart(email), "user")
	base = identityhandle.Truncate(base, maxIdentityHandleLength)
	if err := validateIdentityHandle(base); err != nil {
		return db.User{}, err
	}
	for attempt := 1; attempt <= 100; attempt++ {
		created, err := h.Queries.CreateUser(ctx, db.CreateUserParams{
			Name:        identityHandleCandidate(base, attempt),
			DisplayName: displayName,
			Email:       email,
			AvatarUrl:   avatar,
		})
		if err == nil {
			return created, nil
		}
		if !identityUniqueViolation(err, "user_name_unique") {
			return db.User{}, err
		}
	}
	return db.User{}, errIdentityHandleExhausted
}

func (h *Handler) createAgentWithIdentity(ctx context.Context, q *db.Queries, params db.CreateAgentParams, handleSeed, displaySeed string) (db.Agent, error) {
	base, displayName, err := agentIdentityPlan(handleSeed, displaySeed)
	if err != nil {
		return db.Agent{}, err
	}
	for attempt := 1; attempt <= 100; attempt++ {
		params.Name = identityHandleCandidate(base, attempt)
		params.DisplayName = displayName
		created, err := q.CreateAgent(ctx, params)
		if err == nil {
			return created, nil
		}
		if !identityUniqueViolation(err, "agent_workspace_name_unique") {
			return db.Agent{}, err
		}
	}
	return db.Agent{}, errIdentityHandleExhausted
}

// createAgentWithIdentityTx keeps a duplicate handle attempt from aborting its
// caller's larger provisioning transaction. Agent creation now commonly shares
// that transaction with #general membership and a first-start intent, so a
// PostgreSQL savepoint is required before retrying a unique-constraint conflict.
func (h *Handler) createAgentWithIdentityTx(ctx context.Context, tx pgx.Tx, q *db.Queries, params db.CreateAgentParams, handleSeed, displaySeed string) (db.Agent, error) {
	base, displayName, err := agentIdentityPlan(handleSeed, displaySeed)
	if err != nil {
		return db.Agent{}, err
	}
	for attempt := 1; attempt <= 100; attempt++ {
		if _, err := tx.Exec(ctx, "SAVEPOINT create_agent_identity"); err != nil {
			return db.Agent{}, err
		}

		params.Name = identityHandleCandidate(base, attempt)
		params.DisplayName = displayName
		created, createErr := q.CreateAgent(ctx, params)
		if createErr == nil {
			if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT create_agent_identity"); err != nil {
				return db.Agent{}, err
			}
			return created, nil
		}

		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT create_agent_identity"); err != nil {
			return db.Agent{}, err
		}
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT create_agent_identity"); err != nil {
			return db.Agent{}, err
		}
		if !identityUniqueViolation(createErr, "agent_workspace_name_unique") {
			return db.Agent{}, createErr
		}
	}
	return db.Agent{}, errIdentityHandleExhausted
}

func agentIdentityPlan(handleSeed, displaySeed string) (base, displayName string, err error) {
	displayName = strings.TrimSpace(displaySeed)
	if displayName == "" {
		displayName = strings.TrimSpace(handleSeed)
	}
	if displayName == "" {
		displayName = "Agent"
	}
	base = identityHandleBase(handleSeed, displayName)
	base = identityhandle.Truncate(base, maxIdentityHandleLength)
	if err := validateIdentityHandle(base); err != nil {
		return "", "", err
	}
	return base, displayName, nil
}

func emailLocalPart(email string) string {
	email = strings.TrimSpace(email)
	if at := strings.Index(email, "@"); at > 0 {
		return email[:at]
	}
	return email
}
