// Package conversationhandle is the shared grammar for CLI / Activity / chat
// targets. It matches Raft: #channel, #channel:<6-8 hex thread id>,
// dm:@peer, dm:@peer:<6-8 hex>.
package conversationhandle

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Kind string

const (
	KindChannel Kind = "channel"
	KindDM      Kind = "dm"
)

type Handle struct {
	Kind          Kind
	Name          string
	MessagePrefix string
}

type Match struct {
	Handle Handle
	Raw    string
	Start  int
	End    int
}

// Thread short ids are the first 6–8 hex chars of a message UUID (Raft
// shortMessageId = id.slice(0, 8); scanner accepts 6–8).
var (
	channelNameRe = regexp.MustCompile(`^[\p{L}\p{N}_-]+$`)
	dmPeerRe      = regexp.MustCompile(`^[\w.-]+$`)
	hexPrefixRe   = regexp.MustCompile(`^[0-9a-f]{6,8}$`)
	scanRe        = regexp.MustCompile(`(?i)#([\p{L}\p{N}_-]+)(?::([0-9a-f]{6,8}))?|dm:@([\w.-]+)(?::([0-9a-f]{6,8}))?`)
)

func Parse(raw string) (Handle, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "dm:@") {
		return parseNamed(KindDM, strings.TrimPrefix(raw, "dm:@"), dmPeerRe)
	}
	if strings.HasPrefix(raw, "#") {
		return parseNamed(KindChannel, strings.TrimPrefix(raw, "#"), channelNameRe)
	}
	return Handle{}, false
}

func parseNamed(kind Kind, rest string, nameRe *regexp.Regexp) (Handle, bool) {
	name, suffix, found := strings.Cut(rest, ":")
	name = strings.TrimSpace(name)
	if name == "" || !nameRe.MatchString(name) {
		return Handle{}, false
	}
	if !found {
		return Handle{Kind: kind, Name: name}, true
	}
	if strings.Contains(suffix, ":") {
		return Handle{}, false
	}
	prefix := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(suffix), "-", ""))
	if !hexPrefixRe.MatchString(prefix) {
		return Handle{}, false
	}
	return Handle{Kind: kind, Name: name, MessagePrefix: prefix}, true
}

// Find returns every well-formed handle in text, longest-first at each
// position so #channel:deadbeef is one hit, not a channel plus leftover.
func Find(text string) []Match {
	indexes := scanRe.FindAllStringSubmatchIndex(text, -1)
	if len(indexes) == 0 {
		return nil
	}
	out := make([]Match, 0, len(indexes))
	for _, idx := range indexes {
		raw := text[idx[0]:idx[1]]
		if before := idx[0]; before > 0 {
			prev, _ := utf8.DecodeLastRuneInString(text[:before])
			if isHandleRune(prev) {
				continue
			}
		}
		handle, ok := Parse(raw)
		if !ok {
			continue
		}
		out = append(out, Match{Handle: handle, Raw: raw, Start: idx[0], End: idx[1]})
	}
	return out
}

func isHandleRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-'
}
