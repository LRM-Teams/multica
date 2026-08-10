package cli

import "testing"

func TestNewServiceTargetLocksProductionOrigin(t *testing.T) {
	target, err := NewServiceTarget("production", "")
	if err != nil {
		t.Fatal(err)
	}
	if target.Environment != ServiceEnvironmentProduction || target.Origin != OfficialCloudAPIURL || target.AppOrigin != OfficialCloudAppURL {
		t.Fatalf("target = %+v", target)
	}
	if _, err := NewServiceTarget("production", "https://evil.example"); err == nil {
		t.Fatal("production accepted a redirect")
	}
	if _, err := ResolveServiceTarget(CLIConfig{Environment: "production", ServerURL: "https://evil.example"}); err == nil {
		t.Fatal("persisted production config accepted a redirect")
	}
}

func TestNewServiceTargetAcceptsExplicitTencentTestOrigins(t *testing.T) {
	for _, raw := range []string{"http://203.0.113.8:8080/", "https://test.leagent.me"} {
		target, err := NewServiceTarget("test", raw)
		if err != nil {
			t.Fatalf("NewServiceTarget(%q): %v", raw, err)
		}
		if target.Environment != ServiceEnvironmentTest || target.Origin != target.AppOrigin {
			t.Fatalf("environment = %q", target.Environment)
		}
	}
}

func TestNewServiceTargetRejectsAmbiguousOrUnsafeTestOrigins(t *testing.T) {
	for _, raw := range []string{"", "203.0.113.8", "ftp://test.leagent.me", "https://user:pass@test.leagent.me", "https://test.leagent.me/api", "https://leagent.me", OfficialCloudAPIURL, OfficialCloudAppURL} {
		if _, err := NewServiceTarget("test", raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestResolveServiceTargetMigratesOnlyCanonicalLegacyConfig(t *testing.T) {
	target, err := ResolveServiceTarget(CLIConfig{ServerURL: "https://leagent.me"})
	if err != nil || target.Environment != ServiceEnvironmentProduction || target.Origin != OfficialCloudAPIURL || target.AppOrigin != OfficialCloudAppURL {
		t.Fatalf("canonical legacy config: target=%+v err=%v", target, err)
	}
	if _, err := ResolveServiceTarget(CLIConfig{ServerURL: "https://old-self-host.example"}); err == nil {
		t.Fatal("custom legacy config was silently trusted as test")
	}
}
