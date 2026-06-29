package handler

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var identityHandleInvalidChars = regexp.MustCompile(`[^a-z0-9]+`)
var errIdentityHandleExhausted = errors.New("identity handle generation exhausted")

func identityHandleBase(value, fallback string) string {
	base := identityHandleInvalidChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "_")
	base = strings.Trim(base, "_")
	if base != "" {
		return base
	}
	base = identityHandleInvalidChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(fallback)), "_")
	base = strings.Trim(base, "_")
	if base != "" {
		return base
	}
	return "actor"
}

func identityHandleCandidate(base string, attempt int) string {
	if attempt <= 1 {
		return base
	}
	return base + "_" + strconv.Itoa(attempt)
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
	displayName := strings.TrimSpace(displaySeed)
	if displayName == "" {
		displayName = strings.TrimSpace(handleSeed)
	}
	if displayName == "" {
		displayName = "Agent"
	}
	base := identityHandleBase(handleSeed, displayName)
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

func emailLocalPart(email string) string {
	email = strings.TrimSpace(email)
	if at := strings.Index(email, "@"); at > 0 {
		return email[:at]
	}
	return email
}
