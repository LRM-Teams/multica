package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MemoryProviderPurpose identifies one external model-call purpose governed
// by the Workspace memory provider policy.
type MemoryProviderPurpose string

const (
	ProviderAtomize     MemoryProviderPurpose = "atomize"
	ProviderEmbed       MemoryProviderPurpose = "embed"
	ProviderRerank      MemoryProviderPurpose = "rerank"
	ProviderDive        MemoryProviderPurpose = "dive"
	ProviderConsolidate MemoryProviderPurpose = "consolidate"
)

// ResolvedMemoryProvider is the complete server-approved external call
// identity. Callers must not replace any field after resolution.
type ResolvedMemoryProvider struct {
	Provider      string
	Model         string
	Region        string
	PolicyVersion string
}

// MemoryProviderDegradation is the deterministic action prescribed when a
// purpose is disabled or its selected provider is unavailable.
type MemoryProviderDegradation string

const (
	DegradeNone                MemoryProviderDegradation = ""
	DegradeFallbackAtom        MemoryProviderDegradation = "fallback_atom"
	DegradeBM25                MemoryProviderDegradation = "bm25"
	DegradeDeterministicFusion MemoryProviderDegradation = "deterministic_fusion"
	DegradeJudgeUnavailable    MemoryProviderDegradation = "judge_unavailable"
	DegradeDelayConsolidation  MemoryProviderDegradation = "delay_consolidation"
)

// MemoryProviderPolicyErrorKind classifies policy failures without exposing
// Workspace settings or provider response content.
type MemoryProviderPolicyErrorKind string

const (
	MemoryProviderMissing     MemoryProviderPolicyErrorKind = "missing"
	MemoryProviderDisabled    MemoryProviderPolicyErrorKind = "disabled"
	MemoryProviderDisallowed  MemoryProviderPolicyErrorKind = "disallowed"
	MemoryProviderUnavailable MemoryProviderPolicyErrorKind = "unavailable"
	MemoryProviderInvalid     MemoryProviderPolicyErrorKind = "invalid"
)

// MemoryProviderPolicyError is fail-closed unless Degradation is non-empty.
type MemoryProviderPolicyError struct {
	Purpose     MemoryProviderPurpose
	Kind        MemoryProviderPolicyErrorKind
	Degradation MemoryProviderDegradation
	cause       error
}

func (e *MemoryProviderPolicyError) Error() string {
	if e == nil {
		return "memory provider policy error"
	}
	return fmt.Sprintf("memory provider policy: purpose %q is %s", e.Purpose, e.Kind)
}

func (e *MemoryProviderPolicyError) Unwrap() error { return e.cause }

// MemoryProviderPolicyAllowsDegradation reports whether err is exactly the
// requested purpose-specific degradation. Missing, invalid, and disallowed
// policy never qualify.
func MemoryProviderPolicyAllowsDegradation(err error, purpose MemoryProviderPurpose, degradation MemoryProviderDegradation) bool {
	var policyErr *MemoryProviderPolicyError
	return errors.As(err, &policyErr) && policyErr.Purpose == purpose && policyErr.Degradation == degradation
}

// MemoryProviderWorkspaceReader is implemented by db.Queries.
type MemoryProviderWorkspaceReader interface {
	GetWorkspace(ctx context.Context, id pgtype.UUID) (db.Workspace, error)
}

// MemoryProviderPolicyAllowlist is server-owned authorization for one
// purpose. Empty allowlists deny all values.
type MemoryProviderPolicyAllowlist struct {
	Providers []string
	Regions   []string
}

// MemoryProviderPolicyResolverConfig contains server-owned policy gates.
// CheckAvailable must only inspect the already-selected identity; it must not
// return or select an alternative.
type MemoryProviderPolicyResolverConfig struct {
	Allow          map[MemoryProviderPurpose]MemoryProviderPolicyAllowlist
	CheckAvailable func(context.Context, ResolvedMemoryProvider) error
}

// MemoryProviderPolicyResolver resolves Workspace settings against
// server-owned provider/region allowlists and availability.
type MemoryProviderPolicyResolver struct {
	workspaces MemoryProviderWorkspaceReader
	config     MemoryProviderPolicyResolverConfig
}

func NewMemoryProviderPolicyResolver(workspaces MemoryProviderWorkspaceReader, config MemoryProviderPolicyResolverConfig) *MemoryProviderPolicyResolver {
	return &MemoryProviderPolicyResolver{workspaces: workspaces, config: config}
}

type workspaceMemoryProviderSettings struct {
	MemoryProviderPolicy *workspaceMemoryProviderPolicy `json:"memory_provider_policy"`
}

type workspaceMemoryProviderPolicy struct {
	Version  string                                            `json:"version"`
	Purposes map[MemoryProviderPurpose]workspaceProviderChoice `json:"purposes"`
}

type workspaceProviderChoice struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Region   string `json:"region"`
}

// Resolve returns the exact Workspace-selected identity for purpose. It never
// searches another Workspace, purpose, provider, model, or region.
func (r *MemoryProviderPolicyResolver) Resolve(ctx context.Context, workspaceID pgtype.UUID, purpose MemoryProviderPurpose) (ResolvedMemoryProvider, error) {
	if r == nil || r.workspaces == nil {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderMissing, DegradeNone, nil)
	}
	if !validMemoryProviderPurpose(purpose) {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderInvalid, DegradeNone, nil)
	}
	if !workspaceID.Valid {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderInvalid, DegradeNone, nil)
	}
	workspace, err := r.workspaces.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderMissing, DegradeNone, err)
	}
	var settings workspaceMemoryProviderSettings
	if len(workspace.Settings) == 0 || json.Unmarshal(workspace.Settings, &settings) != nil || settings.MemoryProviderPolicy == nil {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderMissing, DegradeNone, nil)
	}
	policy := settings.MemoryProviderPolicy
	version := strings.TrimSpace(policy.Version)
	choice, ok := policy.Purposes[purpose]
	if version == "" || !ok {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderMissing, DegradeNone, nil)
	}
	if !choice.Enabled {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderDisabled, degradationForPurpose(purpose), nil)
	}
	resolved := ResolvedMemoryProvider{
		Provider:      strings.TrimSpace(choice.Provider),
		Model:         strings.TrimSpace(choice.Model),
		Region:        strings.TrimSpace(choice.Region),
		PolicyVersion: version,
	}
	if resolved.Provider == "" || resolved.Model == "" || resolved.Region == "" {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderInvalid, DegradeNone, nil)
	}
	allow, ok := r.config.Allow[purpose]
	if !ok || !containsExact(allow.Providers, resolved.Provider) || !containsExact(allow.Regions, resolved.Region) {
		return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderDisallowed, DegradeNone, nil)
	}
	if r.config.CheckAvailable != nil {
		if err := r.config.CheckAvailable(ctx, resolved); err != nil {
			return ResolvedMemoryProvider{}, policyFailure(purpose, MemoryProviderUnavailable, degradationForPurpose(purpose), err)
		}
	}
	return resolved, nil
}

func validMemoryProviderPurpose(purpose MemoryProviderPurpose) bool {
	switch purpose {
	case ProviderAtomize, ProviderEmbed, ProviderRerank, ProviderDive, ProviderConsolidate:
		return true
	default:
		return false
	}
}

func degradationForPurpose(purpose MemoryProviderPurpose) MemoryProviderDegradation {
	switch purpose {
	case ProviderAtomize:
		return DegradeFallbackAtom
	case ProviderEmbed:
		return DegradeBM25
	case ProviderRerank:
		return DegradeDeterministicFusion
	case ProviderDive:
		return DegradeJudgeUnavailable
	case ProviderConsolidate:
		return DegradeDelayConsolidation
	default:
		return DegradeNone
	}
}

func policyFailure(purpose MemoryProviderPurpose, kind MemoryProviderPolicyErrorKind, degradation MemoryProviderDegradation, cause error) error {
	return &MemoryProviderPolicyError{Purpose: purpose, Kind: kind, Degradation: degradation, cause: cause}
}

func containsExact(values []string, value string) bool {
	for _, candidate := range values {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}

// LoadMemoryProviderPolicyResolverConfig loads purpose-specific server
// allowlists. Missing environment values intentionally deny all policies.
func LoadMemoryProviderPolicyResolverConfig(getenv func(string) string) MemoryProviderPolicyResolverConfig {
	config := MemoryProviderPolicyResolverConfig{Allow: make(map[MemoryProviderPurpose]MemoryProviderPolicyAllowlist)}
	for _, purpose := range []MemoryProviderPurpose{ProviderAtomize, ProviderEmbed, ProviderRerank, ProviderDive, ProviderConsolidate} {
		prefix := "MULTICA_MEMORY_" + strings.ToUpper(string(purpose))
		config.Allow[purpose] = MemoryProviderPolicyAllowlist{
			Providers: splitMemoryProviderList(getenv(prefix + "_PROVIDERS")),
			Regions:   splitMemoryProviderList(getenv(prefix + "_REGIONS")),
		}
	}
	return config
}

func splitMemoryProviderList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func resolvedMemoryProviderScope(workspaceID string, purpose MemoryProviderPurpose, resolved ResolvedMemoryProvider) memorygraph.ProviderScope {
	return memorygraph.ProviderScope{
		WorkspaceID: workspaceID, Purpose: memorygraph.ProviderPurpose(purpose),
		Provider: resolved.Provider, Model: resolved.Model, Region: resolved.Region, PolicyVersion: resolved.PolicyVersion,
	}
}
