package researchrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf16"
)

// marshalV6CanonicalJSON implements the RFC 8785 profile used by the Ronaldo
// contract: I-JSON values, ECMAScript-compatible number rendering, UTF-16 key
// ordering and JSON strings without HTML escaping.
func marshalV6CanonicalJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	if err := appendV6CanonicalJSON(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func appendV6CanonicalJSON(out *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(typed))
	case string:
		return appendV6JSONString(out, typed)
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return fmt.Errorf("invalid I-JSON number %q", typed)
		}
		if number == 0 {
			out.WriteByte('0')
			return nil
		}
		formatted := strconv.FormatFloat(number, 'g', -1, 64)
		if exponent := bytes.IndexByte([]byte(formatted), 'e'); exponent >= 0 && exponent+1 < len(formatted) && formatted[exponent+1] != '-' && formatted[exponent+1] != '+' {
			formatted = formatted[:exponent+1] + "+" + formatted[exponent+1:]
		}
		out.WriteString(formatted)
	case float64:
		return appendV6CanonicalJSON(out, json.Number(strconv.FormatFloat(typed, 'g', -1, 64)))
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return lessV6UTF16(keys[i], keys[j]) })
		out.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := appendV6JSONString(out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := appendV6CanonicalJSON(out, typed[key]); err != nil {
				return fmt.Errorf("field %q: %w", key, err)
			}
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := appendV6CanonicalJSON(out, item); err != nil {
				return fmt.Errorf("item %d: %w", index, err)
			}
		}
		out.WriteByte(']')
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		decoded, err := decodeSingleV6JSON(raw)
		if err != nil {
			return err
		}
		return appendV6CanonicalJSON(out, decoded)
	}
	return nil
}

func appendV6JSONString(out *bytes.Buffer, value string) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	out.Write(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
	return nil
}

func lessV6UTF16(left, right string) bool {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for index := 0; index < limit; index++ {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return len(a) < len(b)
}
