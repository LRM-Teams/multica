package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

func configuredEnvironmentSwitchTestConfig(t *testing.T) cli.CLIConfig {
	t.Helper()
	production, err := cli.NewServiceTarget("production", "", "")
	if err != nil {
		t.Fatal(err)
	}
	testTarget, err := cli.NewServiceTarget("test", "https://api.test.leagent.me", "https://test.leagent.me")
	if err != nil {
		t.Fatal(err)
	}
	cfg := cli.CLIConfig{}
	cfg.PutServiceEnvironment(production)
	cfg.Token = "prod-session"
	cfg.WorkspaceID = "ws-prod"
	cfg.PutServiceEnvironment(testTarget)
	cfg.Token = "test-session"
	cfg.WorkspaceID = "ws-test"
	if err := cfg.ActivateServiceEnvironment(cli.ServiceEnvironmentProduction); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func testEnvironmentSwitcher(t *testing.T, waitReadyErr error) (*environmentSwitcher, *[]string, *[]cli.CLIConfig) {
	t.Helper()
	cfg := configuredEnvironmentSwitchTestConfig(t)
	var events []string
	var saves []cli.CLIConfig
	startCount := 0
	switcher := &environmentSwitcher{
		loadConfig: func() (cli.CLIConfig, error) { return cloneCLIConfig(cfg), nil },
		saveConfig: func(saved cli.CLIConfig) error {
			events = append(events, "save:"+saved.Environment)
			saves = append(saves, cloneCLIConfig(saved))
			return nil
		},
		activeBindings: func(environment string) ([]computer.WorkspaceBinding, error) {
			return []computer.WorkspaceBinding{{Environment: environment, WorkspaceID: "ws-test", Active: true}}, nil
		},
		health:       func(context.Context) map[string]any { return map[string]any{"status": "running"} },
		controlToken: func() (string, error) { return "owner", nil },
		prepare: func(context.Context, string) error {
			events = append(events, "prepare")
			return nil
		},
		release: func(context.Context, string) error {
			events = append(events, "release")
			return nil
		},
		withPackageLock: func(_ context.Context, fn func() error) error {
			events = append(events, "lock")
			return fn()
		},
		stagePackage: func(context.Context, cli.ReleaseChannel) (string, error) {
			events = append(events, "stage:preview")
			return "v2.0.0-alpha.1", nil
		},
		activatePackage: func(context.Context, string) (packageActivation, error) {
			events = append(events, "activate")
			return packageActivation{PreviousVersion: "v1.0.0", ActiveVersion: "v2.0.0-alpha.1", BinaryPath: "/versions/alpha", Changed: true}, nil
		},
		rollbackPackage: func(_ context.Context, previous string) error {
			events = append(events, "rollback:"+previous)
			return nil
		},
		stop: func() computer.StopResult {
			events = append(events, "stop")
			return computer.StopResult{Running: true, Stopped: true}
		},
		start: func() (computer.StartResult, error) {
			startCount++
			events = append(events, "start")
			return computer.StartResult{Started: true}, nil
		},
		waitReady: func(context.Context, cli.ServiceTarget, []computer.WorkspaceBinding) error {
			events = append(events, "accept")
			return waitReadyErr
		},
	}
	_ = startCount
	return switcher, &events, &saves
}

func TestEnvironmentSwitcherLiveSuccessCommitsInSafeOrder(t *testing.T) {
	switcher, events, saves := testEnvironmentSwitcher(t, nil)
	result, err := switcher.Switch(context.Background(), cli.ServiceEnvironmentTest)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Restarted || result.PackageSource != "preview" || result.Version != "v2.0.0-alpha.1" {
		t.Fatalf("result = %+v", result)
	}
	want := []string{"lock", "stage:preview", "prepare", "activate", "save:test", "stop", "start", "accept"}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("events = %v, want %v", *events, want)
	}
	if len(*saves) != 1 || (*saves)[0].Token != "test-session" {
		t.Fatalf("saved target session = %+v", *saves)
	}
}

func TestEnvironmentSwitcherAcceptanceFailureRollsBackBeforeRestartingPrevious(t *testing.T) {
	switcher, events, saves := testEnvironmentSwitcher(t, errors.New("test service unreachable"))
	_, err := switcher.Switch(context.Background(), cli.ServiceEnvironmentTest)
	if err == nil {
		t.Fatal("expected failed target acceptance")
	}
	want := []string{
		"lock", "stage:preview", "prepare", "activate", "save:test", "stop", "start", "accept",
		"stop", "lock", "rollback:v1.0.0", "save:production", "start",
	}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("events = %v, want %v", *events, want)
	}
	if len(*saves) != 2 || (*saves)[1].Environment != "production" || (*saves)[1].Token != "prod-session" {
		t.Fatalf("rollback config = %+v", *saves)
	}
}

func TestEnvironmentSwitcherRequiresSetupForTarget(t *testing.T) {
	cfg := cli.CLIConfig{}
	production, err := cli.NewServiceTarget("production", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PutServiceEnvironment(production)
	switcher := &environmentSwitcher{loadConfig: func() (cli.CLIConfig, error) { return cfg, nil }}
	if _, err := switcher.Switch(context.Background(), cli.ServiceEnvironmentTest); err == nil {
		t.Fatal("unconfigured test environment was accepted")
	}
}
