// Package identityhandle owns the stable ASCII username contract shared by
// request validation and one-time identity backfills.
package identityhandle

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mozillazg/go-pinyin"
	"golang.org/x/text/unicode/norm"
)

const MaxLength = 32

var ErrInvalid = errors.New("identity username is invalid")

var pattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func Base(value, fallback string) string {
	for _, seed := range []string{value, fallback} {
		if slug := Slug(seed); slug != "" {
			return Truncate(slug, MaxLength)
		}
	}
	return "agent"
}

func Validate(handle string) error {
	if len(handle) > MaxLength || !pattern.MatchString(handle) {
		return ErrInvalid
	}
	return nil
}

func Candidate(base string, attempt int) string {
	suffix := ""
	if attempt > 1 {
		suffix = "-" + strconv.Itoa(attempt)
	}
	return Truncate(base, MaxLength-len(suffix)) + suffix
}

// Slug makes a human-facing display label into the stable ASCII username
// grammar. Han characters become tone-free pinyin; Latin letters and digits
// survive lowercase; every other separator becomes one hyphen.
func Slug(value string) string {
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

func Truncate(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) > limit {
		value = string([]rune(value)[:limit])
	}
	// A slug can be cut immediately after one of its separators. Trim that
	// dangling separator so every generated candidate remains in the username
	// grammar instead of turning a valid long display label into an invalid
	// trailing-hyphen handle.
	return strings.TrimRight(value, "-")
}
