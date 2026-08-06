package memorycuration

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const lexicalDuplicateThreshold = 0.78

var tokenRE = regexp.MustCompile(`[\p{Han}]|[\p{L}\p{N}_]+`)

var dedupeStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "for": true, "from": true,
	"in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "that": true, "the": true, "to": true, "with": true,
	"了": true, "的": true, "和": true, "是": true, "在": true, "有": true, "就": true, "都": true, "也": true, "要": true,
}

func semanticDuplicate(a, b string) bool {
	return lexicalSimilarity(a, b) >= lexicalDuplicateThreshold
}

// sameTopicDuplicate is true when both sides share a non-empty normalized topic
// key and belong to the same type/scope bucket. Prefer this over pure lexical
// matching when self-review / missed-write candidates carry topic metadata.
func sameTopicDuplicate(topicA, topicB, typeA, typeB, scopeA, scopeB string) bool {
	topicA = NormalizeTopicKey(topicA)
	topicB = NormalizeTopicKey(topicB)
	if topicA == "" || topicB == "" || topicA != topicB {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(typeA), strings.TrimSpace(typeB)) &&
		strings.EqualFold(strings.TrimSpace(scopeA), strings.TrimSpace(scopeB))
}

// NormalizeTopicKey keeps topic identity stable across slight wording changes.
func NormalizeTopicKey(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_' || r == '-' || unicode.IsSpace(r):
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func lexicalSimilarity(a, b string) float64 {
	wa := weightedTokens(a)
	wb := weightedTokens(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for token, av := range wa {
		dot += av * wb[token]
		normA += av * av
	}
	for _, bv := range wb {
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	cosine := dot / (math.Sqrt(normA) * math.Sqrt(normB))
	jaccard := weightedJaccard(wa, wb)
	return (cosine * 0.7) + (jaccard * 0.3)
}

func weightedTokens(s string) map[string]float64 {
	tokens := dedupeTokens(s)
	weights := map[string]float64{}
	for _, token := range tokens {
		weights[token]++
	}
	return weights
}

func dedupeTokens(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	matches := tokenRE.FindAllString(s, -1)
	out := make([]string, 0, len(matches))
	for _, token := range matches {
		token = strings.TrimFunc(token, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
		if token == "" || dedupeStopWords[token] {
			continue
		}
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

func weightedJaccard(a, b map[string]float64) float64 {
	seen := map[string]bool{}
	var intersection, union float64
	for token, av := range a {
		bv := b[token]
		intersection += math.Min(av, bv)
		union += math.Max(av, bv)
		seen[token] = true
	}
	for token, bv := range b {
		if seen[token] {
			continue
		}
		union += bv
	}
	if union == 0 {
		return 0
	}
	return intersection / union
}
