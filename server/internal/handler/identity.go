package handler

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mozillazg/go-pinyin"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"golang.org/x/text/unicode/norm"
)

var errIdentityHandleExhausted = errors.New("identity handle generation exhausted")
var errIdentityHandleInvalid = errors.New("identity username is invalid")
var identityHandlePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const maxIdentityHandleLength = 32

func identityHandleBase(value, fallback string) string {
	for _, seed := range []string{value, fallback} {
		if slug := identityHandleSlug(seed); slug != "" {
			return truncateRunes(slug, maxIdentityHandleLength)
		}
	}
	return "agent"
}

func validateIdentityHandle(handle string) error {
	if len(handle) > maxIdentityHandleLength || !identityHandlePattern.MatchString(handle) {
		return errIdentityHandleInvalid
	}
	return nil
}

func identityHandleCandidate(base string, attempt int) string {
	suffix := ""
	if attempt > 1 {
		suffix = "-" + strconv.Itoa(attempt)
	}
	return truncateRunes(base, maxIdentityHandleLength-len(suffix)) + suffix
}

// identityHandleSlug makes a human-entered display label into the single ASCII
// username grammar accepted by common IM clients. Han characters are
// transliterated to tone-free pinyin; ASCII words and decomposed Latin letters
// are preserved in lowercase. Every other separator collapses to one hyphen.
func identityHandleSlug(value string) string {
	args := pinyin.NewArgs()
	parts := make([]string, 0, len(value))
	var ascii strings.Builder
	flushASCII := func() {
		if ascii.Len() > 0 {
			parts = append(parts, ascii.String())
			ascii.Reset()
		}
	}
	for _, r := range norm.NFD.String(value) {
		switch {
		case unicode.Is(unicode.Han, r):
			flushASCII()
			if readings := pinyin.SinglePinyin(r, args); len(readings) > 0 && readings[0] != "" {
				parts = append(parts, readings[0])
			}
		case r <= unicode.MaxASCII && unicode.IsLetter(r):
			ascii.WriteRune(unicode.ToLower(r))
		case r <= unicode.MaxASCII && unicode.IsDigit(r):
			ascii.WriteRune(r)
		case unicode.Is(unicode.Mn, r):
			// Ignore a combining mark after its Latin base character.
		default:
			flushASCII()
		}
	}
	flushASCII()
	return strings.Join(parts, "-")
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
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
	base = truncateRunes(base, maxIdentityHandleLength)
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
	displayName := strings.TrimSpace(displaySeed)
	if displayName == "" {
		displayName = strings.TrimSpace(handleSeed)
	}
	if displayName == "" {
		displayName = "Agent"
	}
	base := identityHandleBase(handleSeed, displayName)
	base = truncateRunes(base, maxIdentityHandleLength)
	if err := validateIdentityHandle(base); err != nil {
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

func emailLocalPart(email string) string {
	email = strings.TrimSpace(email)
	if at := strings.Index(email, "@"); at > 0 {
		return email[:at]
	}
	return email
}
