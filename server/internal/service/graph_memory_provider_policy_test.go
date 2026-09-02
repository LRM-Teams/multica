package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeMemoryProviderWorkspaceReader struct {
	settings map[string][]byte
}

func (f fakeMemoryProviderWorkspaceReader) GetWorkspace(_ context.Context, id pgtype.UUID) (db.Workspace, error) {
	settings, ok := f.settings[util.UUIDToString(id)]
	if !ok {
		return db.Workspace{}, pgx.ErrNoRows
	}
	return db.Workspace{ID: id, Settings: settings}, nil
}

func testMemoryProviderSettings(version string, entries string) []byte {
	return []byte(`{"memory_provider_policy":{"version":"` + version + `","purposes":{` + entries + `}}}`)
}

func testProviderPolicyConfig(check func(context.Context, ResolvedMemoryProvider) error) MemoryProviderPolicyResolverConfig {
	allow := make(map[MemoryProviderPurpose]MemoryProviderPolicyAllowlist)
	for _, purpose := range []MemoryProviderPurpose{ProviderAtomize, ProviderEmbed, ProviderRerank, ProviderDive, ProviderConsolidate} {
		allow[purpose] = MemoryProviderPolicyAllowlist{
			Providers: []string{"approved", "unused-fallback"},
			Regions:   []string{"eu-central-1", "us-east-1"},
		}
	}
	return MemoryProviderPolicyResolverConfig{Allow: allow, CheckAvailable: check}
}

func TestMemoryProviderPolicyAllowedProviderRegion(t *testing.T) {
	workspaceID := util.MustParseUUID("10000000-0000-4000-8000-000000000001")
	resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{
		util.UUIDToString(workspaceID): testMemoryProviderSettings("policy-7", `"embed":{"enabled":true,"provider":"approved","model":"embed-v3","region":"eu-central-1"}`),
	}}, testProviderPolicyConfig(nil))

	got, err := resolver.Resolve(context.Background(), workspaceID, ProviderEmbed)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := (ResolvedMemoryProvider{Provider: "approved", Model: "embed-v3", Region: "eu-central-1", PolicyVersion: "policy-7"})
	if got != want {
		t.Fatalf("Resolve = %+v, want %+v", got, want)
	}
}

func TestMemoryProviderPolicyOutageUsesPurposeDegradation(t *testing.T) {
	workspaceID := util.MustParseUUID("10000000-0000-4000-8000-000000000002")
	entries := `"atomize":{"enabled":true,"provider":"approved","model":"a","region":"us-east-1"},` +
		`"embed":{"enabled":true,"provider":"approved","model":"e","region":"us-east-1"},` +
		`"rerank":{"enabled":true,"provider":"approved","model":"r","region":"us-east-1"},` +
		`"dive":{"enabled":true,"provider":"approved","model":"d","region":"us-east-1"},` +
		`"consolidate":{"enabled":true,"provider":"approved","model":"c","region":"us-east-1"}`
	resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{
		util.UUIDToString(workspaceID): testMemoryProviderSettings("policy-outage", entries),
	}}, testProviderPolicyConfig(func(context.Context, ResolvedMemoryProvider) error {
		return errors.New("provider unavailable")
	}))

	want := map[MemoryProviderPurpose]MemoryProviderDegradation{
		ProviderAtomize:     DegradeFallbackAtom,
		ProviderEmbed:       DegradeBM25,
		ProviderRerank:      DegradeDeterministicFusion,
		ProviderDive:        DegradeJudgeUnavailable,
		ProviderConsolidate: DegradeDelayConsolidation,
	}
	for purpose, degradation := range want {
		t.Run(string(purpose), func(t *testing.T) {
			_, err := resolver.Resolve(context.Background(), workspaceID, purpose)
			var policyErr *MemoryProviderPolicyError
			if !errors.As(err, &policyErr) {
				t.Fatalf("Resolve error = %v, want MemoryProviderPolicyError", err)
			}
			if policyErr.Kind != MemoryProviderUnavailable || policyErr.Degradation != degradation {
				t.Fatalf("error = %+v, want unavailable degradation %q", policyErr, degradation)
			}
		})
	}
}

func TestMemoryProviderPolicyDisabledPurpose(t *testing.T) {
	workspaceID := util.MustParseUUID("10000000-0000-4000-8000-000000000003")
	resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{
		util.UUIDToString(workspaceID): testMemoryProviderSettings("policy-disabled", `"rerank":{"enabled":false}`),
	}}, testProviderPolicyConfig(nil))

	_, err := resolver.Resolve(context.Background(), workspaceID, ProviderRerank)
	var policyErr *MemoryProviderPolicyError
	if !errors.As(err, &policyErr) || policyErr.Kind != MemoryProviderDisabled || policyErr.Degradation != DegradeDeterministicFusion {
		t.Fatalf("Resolve error = %#v, want disabled deterministic-fusion degradation", err)
	}
}

func TestMemoryProviderPolicyMissingFailsClosed(t *testing.T) {
	workspaceID := util.MustParseUUID("10000000-0000-4000-8000-000000000004")
	resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{
		util.UUIDToString(workspaceID): []byte(`{"theme":"dark"}`),
	}}, testProviderPolicyConfig(nil))

	_, err := resolver.Resolve(context.Background(), workspaceID, ProviderEmbed)
	var policyErr *MemoryProviderPolicyError
	if !errors.As(err, &policyErr) || policyErr.Kind != MemoryProviderMissing || policyErr.Degradation != DegradeNone {
		t.Fatalf("Resolve error = %#v, want fail-closed missing policy", err)
	}
}

func TestMemoryProviderPolicyDisallowedProviderAndRegionFailClosed(t *testing.T) {
	workspaceID := util.MustParseUUID("10000000-0000-4000-8000-000000000005")
	for name, entry := range map[string]string{
		"provider": `"embed":{"enabled":true,"provider":"forbidden","model":"m","region":"us-east-1"}`,
		"region":   `"embed":{"enabled":true,"provider":"approved","model":"m","region":"forbidden-region"}`,
	} {
		t.Run(name, func(t *testing.T) {
			resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{
				util.UUIDToString(workspaceID): testMemoryProviderSettings("policy-disallowed", entry),
			}}, testProviderPolicyConfig(nil))
			_, err := resolver.Resolve(context.Background(), workspaceID, ProviderEmbed)
			var policyErr *MemoryProviderPolicyError
			if !errors.As(err, &policyErr) || policyErr.Kind != MemoryProviderDisallowed || policyErr.Degradation != DegradeNone {
				t.Fatalf("Resolve error = %#v, want fail-closed disallowed policy", err)
			}
		})
	}
}

func TestMemoryProviderPolicyNoProviderFallback(t *testing.T) {
	workspaceID := util.MustParseUUID("10000000-0000-4000-8000-000000000006")
	var checked []string
	resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{
		util.UUIDToString(workspaceID): testMemoryProviderSettings("policy-no-fallback", `"dive":{"enabled":true,"provider":"approved","model":"judge","region":"us-east-1"}`),
	}}, testProviderPolicyConfig(func(_ context.Context, p ResolvedMemoryProvider) error {
		checked = append(checked, p.Provider)
		if p.Provider == "approved" {
			return errors.New("outage")
		}
		return nil
	}))

	_, err := resolver.Resolve(context.Background(), workspaceID, ProviderDive)
	var policyErr *MemoryProviderPolicyError
	if !errors.As(err, &policyErr) || policyErr.Kind != MemoryProviderUnavailable {
		t.Fatalf("Resolve error = %#v, want unavailable", err)
	}
	if len(checked) != 1 || checked[0] != "approved" {
		t.Fatalf("availability checks = %v, want only the Workspace-selected provider", checked)
	}
}

func TestMemoryProviderPolicyWorkspacesResolveIndependently(t *testing.T) {
	workspaceA := util.MustParseUUID("10000000-0000-4000-8000-000000000007")
	workspaceB := util.MustParseUUID("10000000-0000-4000-8000-000000000008")
	resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{
		util.UUIDToString(workspaceA): testMemoryProviderSettings("policy-a", `"embed":{"enabled":true,"provider":"approved","model":"model-a","region":"us-east-1"}`),
		util.UUIDToString(workspaceB): testMemoryProviderSettings("policy-b", `"embed":{"enabled":true,"provider":"approved","model":"model-b","region":"eu-central-1"}`),
	}}, testProviderPolicyConfig(nil))

	gotA, err := resolver.Resolve(context.Background(), workspaceA, ProviderEmbed)
	if err != nil {
		t.Fatalf("Resolve workspace A: %v", err)
	}
	gotB, err := resolver.Resolve(context.Background(), workspaceB, ProviderEmbed)
	if err != nil {
		t.Fatalf("Resolve workspace B: %v", err)
	}
	if gotA == gotB || gotA.PolicyVersion != "policy-a" || gotB.PolicyVersion != "policy-b" {
		t.Fatalf("Workspace resolutions were not independent: A=%+v B=%+v", gotA, gotB)
	}
}
