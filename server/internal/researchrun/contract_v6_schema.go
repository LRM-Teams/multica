package researchrun

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type v6SchemaNode struct {
	Ref                  string                     `json:"$ref"`
	Type                 json.RawMessage            `json:"type"`
	Const                any                        `json:"const"`
	Enum                 []any                      `json:"enum"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	Items                json.RawMessage            `json:"items"`
	MinProperties        *int                       `json:"minProperties"`
	MinItems             *int                       `json:"minItems"`
	MaxItems             *int                       `json:"maxItems"`
	UniqueItems          bool                       `json:"uniqueItems"`
	MinLength            *int                       `json:"minLength"`
	MaxLength            *int                       `json:"maxLength"`
	Pattern              string                     `json:"pattern"`
	Format               string                     `json:"format"`
	Minimum              *float64                   `json:"minimum"`
	Maximum              *float64                   `json:"maximum"`
	ExclusiveMinimum     *float64                   `json:"exclusiveMinimum"`
}

func validateV6SchemaValue(value any, raw json.RawMessage, definitions map[string]json.RawMessage, path string) error {
	var schema v6SchemaNode
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("%s: invalid schema: %w", path, err)
	}
	if schema.Ref != "" {
		const prefix = "#/$defs/"
		if len(schema.Ref) <= len(prefix) || schema.Ref[:len(prefix)] != prefix {
			return fmt.Errorf("%s: unsupported schema reference %q", path, schema.Ref)
		}
		target, exists := definitions[schema.Ref[len(prefix):]]
		if !exists {
			return fmt.Errorf("%s: unresolved schema reference %q", path, schema.Ref)
		}
		return validateV6SchemaValue(value, target, definitions, path)
	}
	if schema.Const != nil && !v6JSONEqual(value, schema.Const) {
		return fmt.Errorf("%s: value must equal %v", path, schema.Const)
	}
	if len(schema.Enum) > 0 {
		matched := false
		for _, candidate := range schema.Enum {
			if v6JSONEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value is not in the allowed enum", path)
		}
	}
	typeName := ""
	if len(schema.Type) > 0 {
		if err := json.Unmarshal(schema.Type, &typeName); err != nil {
			return fmt.Errorf("%s: unsupported schema type", path)
		}
		if !v6HasJSONType(value, typeName) {
			return fmt.Errorf("%s: expected %s", path, typeName)
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		if schema.MinProperties != nil && len(typed) < *schema.MinProperties {
			return fmt.Errorf("%s: requires at least %d properties", path, *schema.MinProperties)
		}
		for _, required := range schema.Required {
			member, exists := typed[required]
			if !exists || member == nil {
				return fmt.Errorf("%s.%s: required field is missing or null", path, required)
			}
		}
		additionalAllowed := true
		var additionalSchema json.RawMessage
		if len(schema.AdditionalProperties) > 0 {
			if string(schema.AdditionalProperties) == "false" {
				additionalAllowed = false
			} else if string(schema.AdditionalProperties) != "true" {
				additionalSchema = schema.AdditionalProperties
			}
		}
		for key, member := range typed {
			propertySchema, exists := schema.Properties[key]
			if exists {
				if err := validateV6SchemaValue(member, propertySchema, definitions, path+"."+key); err != nil {
					return err
				}
				continue
			}
			if !additionalAllowed {
				return fmt.Errorf("%s.%s: unknown field", path, key)
			}
			if len(additionalSchema) > 0 {
				if err := validateV6SchemaValue(member, additionalSchema, definitions, path+"."+key); err != nil {
					return err
				}
			}
		}
	case []any:
		if schema.MinItems != nil && len(typed) < *schema.MinItems {
			return fmt.Errorf("%s: requires at least %d items", path, *schema.MinItems)
		}
		if schema.MaxItems != nil && len(typed) > *schema.MaxItems {
			return fmt.Errorf("%s: exceeds %d items", path, *schema.MaxItems)
		}
		seen := map[string]bool{}
		for index, item := range typed {
			if len(schema.Items) > 0 {
				if err := validateV6SchemaValue(item, schema.Items, definitions, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
			if schema.UniqueItems {
				canonical, err := MarshalArtifactCanonicalJSON(item)
				if err != nil {
					return fmt.Errorf("%s[%d]: canonicalize unique item: %w", path, index, err)
				}
				key := string(canonical)
				if seen[key] {
					return fmt.Errorf("%s[%d]: duplicate item", path, index)
				}
				seen[key] = true
			}
		}
	case string:
		length := len([]rune(typed))
		if schema.MinLength != nil && length < *schema.MinLength {
			return fmt.Errorf("%s: shorter than %d characters", path, *schema.MinLength)
		}
		if schema.MaxLength != nil && length > *schema.MaxLength {
			return fmt.Errorf("%s: longer than %d characters", path, *schema.MaxLength)
		}
		if schema.Pattern != "" {
			if !matchesV6SchemaPattern(schema.Pattern, typed) {
				return fmt.Errorf("%s: does not match required pattern", path)
			}
		}
		switch schema.Format {
		case "uuid":
			if uuid.Validate(typed) != nil {
				return fmt.Errorf("%s: invalid UUID", path)
			}
		case "date-time":
			if _, err := time.Parse(time.RFC3339, typed); err != nil {
				return fmt.Errorf("%s: invalid date-time", path)
			}
		}
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return fmt.Errorf("%s: invalid number", path)
		}
		if schema.Minimum != nil && number < *schema.Minimum {
			return fmt.Errorf("%s: less than minimum", path)
		}
		if schema.Maximum != nil && number > *schema.Maximum {
			return fmt.Errorf("%s: greater than maximum", path)
		}
		if schema.ExclusiveMinimum != nil && number <= *schema.ExclusiveMinimum {
			return fmt.Errorf("%s: not greater than exclusive minimum", path)
		}
	}
	return nil
}

func matchesV6SchemaPattern(pattern, value string) bool {
	compiled, err := regexp.Compile(pattern)
	if err == nil {
		return compiled.MatchString(value)
	}
	// Draft 2020-12 patterns use ECMA-262. The frozen Report path is the only
	// expression that needs lookahead, which RE2 intentionally does not expose.
	if pattern != "^(?!.*(?:^|/)\\.\\.(?:/|$))(?!.*//)[A-Za-z0-9](?:[A-Za-z0-9._/-]*[A-Za-z0-9._-])?$" {
		return false
	}
	if value == "" || strings.Contains(value, "//") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." || segment == "" {
			return false
		}
	}
	valid := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*[A-Za-z0-9._-]$|^[A-Za-z0-9]$`)
	return valid.MatchString(value)
}

func v6HasJSONType(value any, expected string) bool {
	switch expected {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := strconv.ParseInt(string(number), 10, 64)
		return err == nil
	case "null":
		return value == nil
	default:
		return false
	}
}

func v6JSONEqual(left, right any) bool {
	leftBytes, leftErr := MarshalArtifactCanonicalJSON(left)
	rightBytes, rightErr := MarshalArtifactCanonicalJSON(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}
