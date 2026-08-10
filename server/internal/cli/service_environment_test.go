package cli

import "testing"

func TestNewServiceTargetLocksProductionOrigin(t *testing.T) {
	target, err := NewServiceTarget("production", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if target.Environment != ServiceEnvironmentProduction || target.Origin != OfficialCloudAPIURL || target.AppOrigin != OfficialCloudAppURL {
		t.Fatalf("target = %+v", target)
	}
	if _, err := NewServiceTarget("production", "https://evil.example", "https://evil.example"); err == nil {
		t.Fatal("production accepted a redirect")
	}
	if _, err := ResolveServiceTarget(CLIConfig{Environment: "production", ServerURL: "https://evil.example"}); err == nil {
		t.Fatal("persisted production config accepted a redirect")
	}
}

func TestNewServiceTargetAcceptsExplicitTencentTestOrigins(t *testing.T) {
	for _, pair := range [][2]string{
		{"http://203.0.113.8:8080/", "http://203.0.113.8:3000/"},
		{"https://api.test.leagent.me", "https://test.leagent.me"},
	} {
		target, err := NewServiceTarget("test", pair[0], pair[1])
		if err != nil {
			t.Fatalf("NewServiceTarget(%q, %q): %v", pair[0], pair[1], err)
		}
		if target.Environment != ServiceEnvironmentTest || target.Origin == target.AppOrigin {
			t.Fatalf("target = %+v", target)
		}
	}
}

func TestNewServiceTargetRejectsAmbiguousOrUnsafeTestOrigins(t *testing.T) {
	for _, raw := range []string{"", "203.0.113.8", "ftp://test.leagent.me", "https://user:pass@test.leagent.me", "https://test.leagent.me/api", "https://leagent.me", OfficialCloudAPIURL, OfficialCloudAppURL} {
		if _, err := NewServiceTarget("test", raw, "https://app.test.example"); err == nil {
			t.Errorf("accepted server origin %q", raw)
		}
		if _, err := NewServiceTarget("test", "https://api.test.example", raw); err == nil {
			t.Errorf("accepted app origin %q", raw)
		}
	}
}

func TestResolveServiceTargetPreservesSplitTestOrigins(t *testing.T) {
	target, err := ResolveServiceTarget(CLIConfig{
		Environment: "test",
		ServerURL:   "https://api.test.example",
		AppURL:      "https://test.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.Origin != "https://api.test.example" || target.AppOrigin != "https://test.example" {
		t.Fatalf("target = %+v", target)
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
