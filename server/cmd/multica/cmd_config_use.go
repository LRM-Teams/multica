package main

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

const environmentSwitchTimeout = 30 * time.Minute

type packageActivation struct {
	PreviousVersion string
	ActiveVersion   string
	BinaryPath      string
	Changed         bool
}

// environmentSwitcher is the one orchestration boundary for changing a live
// Computer's service environment. Cobra only selects the target; this module
// owns package staging, explicit interruption confirmation, config commit,
// restart, acceptance, and rollback.
type environmentSwitcher struct {
	loadConfig      func() (cli.CLIConfig, error)
	saveConfig      func(cli.CLIConfig) error
	activeBindings  func(string) ([]computer.WorkspaceBinding, error)
	health          func(context.Context) map[string]any
	withPackageLock func(context.Context, func() error) error
	stagePackage    func(context.Context, cli.ReleaseChannel) (string, error)
	activatePackage func(context.Context, string) (packageActivation, error)
	rollbackPackage func(context.Context, string) error
	stop            func() computer.StopResult
	start           func() (computer.StartResult, error)
	waitReady       func(context.Context, cli.ServiceTarget, []computer.WorkspaceBinding) error
}

type environmentSwitchResult struct {
	Environment   cli.ServiceEnvironment
	PackageSource string
	Version       string
	BinaryPath    string
	Restarted     bool
	AlreadyActive bool
	Aborted       bool
}

func newEnvironmentSwitcher() (*environmentSwitcher, error) {
	store, err := cli.OpenVersionStore("")
	if err != nil {
		return nil, fmt.Errorf("open version store: %w", err)
	}
	lifecycle := &computer.Lifecycle{}
	return &environmentSwitcher{
		loadConfig: cli.LoadCLIConfig,
		saveConfig: cli.SaveCLIConfig,
		activeBindings: func(environment string) ([]computer.WorkspaceBinding, error) {
			return computer.NewBindingsStore(computer.RootDir("")).AllActiveForEnvironment(environment)
		},
		health: func(ctx context.Context) map[string]any {
			return lifecycle.Health(ctx)
		},
		withPackageLock: store.WithMachineMutationLock,
		stagePackage: func(ctx context.Context, channel cli.ReleaseChannel) (string, error) {
			state, err := store.ReadActivationState()
			if err != nil {
				return "", err
			}
			if state.ActiveVersion == "" {
				if !cli.IsReleaseVersion(version) {
					return "", fmt.Errorf("the current development binary %q is not a release; install Multica before switching environments", version)
				}
				if _, err := store.BootstrapActiveFromExecutable(ctx, version); err != nil {
					return "", fmt.Errorf("bootstrap current package: %w", err)
				}
			}
			manifest, err := cli.FetchReleaseForChannelWithOverride(channel, "")
			if err != nil {
				return "", fmt.Errorf("fetch %s package manifest: %w", packageSourceName(channel), err)
			}
			staged, err := cli.DownloadAndStageRelease(ctx, store, manifest.TagName, cli.DefaultUpdateDownloadTimeout, "")
			if err != nil {
				return "", fmt.Errorf("stage %s package %s: %w", packageSourceName(channel), manifest.TagName, err)
			}
			return staged.Staged.Version, nil
		},
		activatePackage: func(ctx context.Context, targetVersion string) (packageActivation, error) {
			before, err := store.ReadActivationState()
			if err != nil {
				return packageActivation{}, err
			}
			after, path, err := store.OfflineActivateStaged(ctx, targetVersion, "environment-switch")
			if err != nil {
				return packageActivation{}, err
			}
			return packageActivation{
				PreviousVersion: before.ActiveVersion,
				ActiveVersion:   after.ActiveVersion,
				BinaryPath:      path,
				Changed:         before.ActiveVersion != after.ActiveVersion,
			}, nil
		},
		rollbackPackage: func(ctx context.Context, previousVersion string) error {
			if strings.TrimSpace(previousVersion) == "" {
				return fmt.Errorf("previous Active package is unavailable")
			}
			_, _, err := store.OfflineActivateStaged(ctx, previousVersion, "environment-switch-rollback")
			return err
		},
		stop: lifecycle.Stop,
		start: func() (computer.StartResult, error) {
			return lifecycle.StartBackground(computer.StartOptions{})
		},
		waitReady: func(ctx context.Context, target cli.ServiceTarget, bindings []computer.WorkspaceBinding) error {
			return waitForSwitchedEnvironment(ctx, lifecycle, target, bindings)
		},
	}, nil
}

func (s *environmentSwitcher) Switch(ctx context.Context, environment cli.ServiceEnvironment, confirmInterrupt func(int64) (bool, error)) (environmentSwitchResult, error) {
	current, err := s.loadConfig()
	if err != nil {
		return environmentSwitchResult{}, err
	}
	previous := cloneCLIConfig(current)
	next := cloneCLIConfig(current)
	if err := next.ActivateServiceEnvironment(environment); err != nil {
		return environmentSwitchResult{}, err
	}
	target, err := cli.ResolveServiceTarget(next)
	if err != nil {
		return environmentSwitchResult{}, err
	}
	if strings.TrimSpace(next.Token) == "" {
		return environmentSwitchResult{}, fmt.Errorf("%s environment has no saved login; run `multica setup --environment %s /<workspace>` first", environment, environment)
	}
	bindings, err := s.activeBindings(string(environment))
	if err != nil {
		return environmentSwitchResult{}, fmt.Errorf("load %s Workspace connections: %w", environment, err)
	}
	if len(bindings) == 0 {
		return environmentSwitchResult{}, fmt.Errorf("%s environment has no active Workspace connection; run `multica setup --environment %s /<workspace>` first", environment, environment)
	}
	if current.Environment == string(environment) {
		return environmentSwitchResult{
			Environment: environment, PackageSource: packageSourceName(cli.ReleaseChannelForEnvironment(environment)), AlreadyActive: true,
		}, nil
	}

	channel := cli.ReleaseChannelForEnvironment(environment)
	var targetVersion string
	err = s.withPackageLock(ctx, func() error {
		var stageErr error
		targetVersion, stageErr = s.stagePackage(ctx, channel)
		return stageErr
	})
	if err != nil {
		return environmentSwitchResult{}, err
	}

	healthCtx, healthCancel := context.WithTimeout(ctx, 2*time.Second)
	health := s.health(healthCtx)
	healthCancel()
	wasRunning := computer.Alive(health)
	if wasRunning {
		if confirmInterrupt == nil {
			return environmentSwitchResult{}, fmt.Errorf("environment switch confirmation is required while Computer is running")
		}
		activeTaskCount, _ := health["active_task_count"].(float64)
		confirmed, confirmErr := confirmInterrupt(int64(activeTaskCount))
		if confirmErr != nil {
			return environmentSwitchResult{}, confirmErr
		}
		if !confirmed {
			return environmentSwitchResult{Environment: environment, PackageSource: packageSourceName(channel), Aborted: true}, nil
		}
	}

	var activation packageActivation
	rollbackCommitted := func(why error) error {
		var rollbackErrors []string
		if activation.Changed {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), cli.DefaultUpdateDownloadTimeout+30*time.Second)
			if err := s.rollbackPackage(rollbackCtx, activation.PreviousVersion); err != nil {
				rollbackErrors = append(rollbackErrors, "package: "+err.Error())
			}
			cancel()
		}
		if err := s.saveConfig(previous); err != nil {
			rollbackErrors = append(rollbackErrors, "config: "+err.Error())
		}
		if len(rollbackErrors) > 0 {
			return fmt.Errorf("%w; rollback incomplete: %s", why, strings.Join(rollbackErrors, "; "))
		}
		return why
	}

	err = s.withPackageLock(ctx, func() error {
		var activateErr error
		activation, activateErr = s.activatePackage(ctx, targetVersion)
		if activateErr != nil {
			return rollbackCommitted(fmt.Errorf("activate %s package: %w", packageSourceName(channel), activateErr))
		}
		if err := s.saveConfig(next); err != nil {
			return rollbackCommitted(fmt.Errorf("save %s environment: %w", environment, err))
		}
		if wasRunning {
			stopped := s.stop()
			if !stopped.Stopped {
				stopErr := stopped.Err
				if stopErr == nil {
					stopErr = fmt.Errorf("Computer did not stop")
				}
				return rollbackCommitted(stopErr)
			}
		}
		return nil
	})
	if err != nil {
		return environmentSwitchResult{}, err
	}

	result := environmentSwitchResult{
		Environment: environment, PackageSource: packageSourceName(channel), Version: activation.ActiveVersion, BinaryPath: activation.BinaryPath,
	}
	if !wasRunning {
		return result, nil
	}
	started, err := s.start()
	if err == nil && !started.Started {
		err = fmt.Errorf("successor did not become ready (last status %q)", started.LastStatus)
	}
	if err == nil {
		err = s.waitReady(ctx, target, bindings)
	}
	if err == nil {
		result.Restarted = true
		return result, nil
	}

	// The new process did not prove the target connection. Stop it, restore
	// package+config under the mutation lock, then restart the old environment.
	failedSuccessorStop := s.stop()
	if !failedSuccessorStop.Stopped {
		return environmentSwitchResult{}, fmt.Errorf("%s environment successor failed acceptance: %w; rollback was not attempted because the successor could not be stopped safely", environment, err)
	}
	rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), cli.DefaultUpdateDownloadTimeout+30*time.Second)
	defer rollbackCancel()
	rollbackErr := s.withPackageLock(rollbackCtx, func() error {
		return rollbackCommitted(fmt.Errorf("%s environment successor failed acceptance: %w", environment, err))
	})
	restored, restoreStartErr := s.start()
	if restoreStartErr != nil || !restored.Started {
		return environmentSwitchResult{}, fmt.Errorf("%v; previous environment also failed to restart: %v", rollbackErr, restoreStartErr)
	}
	return environmentSwitchResult{}, rollbackErr
}

func cloneCLIConfig(cfg cli.CLIConfig) cli.CLIConfig {
	clone := cfg
	if cfg.Environments != nil {
		clone.Environments = make(map[string]cli.ServiceEnvironmentConfig, len(cfg.Environments))
		for key, value := range cfg.Environments {
			clone.Environments[key] = value
		}
	}
	return clone
}

func waitForSwitchedEnvironment(ctx context.Context, lifecycle *computer.Lifecycle, target cli.ServiceTarget, bindings []computer.WorkspaceBinding) error {
	wantWorkspaces := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		wantWorkspaces[binding.WorkspaceID] = struct{}{}
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		healthCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		health := lifecycle.Health(healthCtx)
		cancel()
		if health["status"] == "running" && health["connected"] == true &&
			fmt.Sprint(health["environment"]) == string(target.Environment) &&
			normalizeAPIBaseURL(fmt.Sprint(health["server_url"])) == normalizeAPIBaseURL(target.Origin) {
			for workspaceID := range wantWorkspaces {
				if healthContainsWorkspace(health, workspaceID) {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func packageSourceName(channel cli.ReleaseChannel) string {
	if channel == cli.ReleaseChannelAlpha {
		return "preview"
	}
	return "stable"
}

func runConfigUse(cmd *cobra.Command, args []string) error {
	if profile := resolveProfile(cmd); profile != "" {
		return fmt.Errorf("config use switches the machine-wide Cloud Computer and does not support --profile")
	}
	environment := cli.ServiceEnvironment(strings.ToLower(strings.TrimSpace(args[0])))
	if environment != cli.ServiceEnvironmentProduction && environment != cli.ServiceEnvironmentTest {
		return fmt.Errorf("unsupported environment %q: use production or test", args[0])
	}
	switcher, err := newEnvironmentSwitcher()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), environmentSwitchTimeout)
	defer cancel()
	skipConfirmation, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return err
	}
	confirm := func(activeTaskCount int64) (bool, error) {
		if skipConfirmation {
			return true, nil
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Computer is running. Switching environments restarts it immediately and may interrupt current work.")
		fmt.Fprintf(cmd.ErrOrStderr(), "Active tasks reported: %d. Continue? [y/N] ", activeTaskCount)
		answer, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if readErr != nil && strings.TrimSpace(answer) == "" {
			return false, fmt.Errorf("read environment switch confirmation: %w", readErr)
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes", nil
	}
	result, err := switcher.Switch(ctx, environment, confirm)
	if err != nil {
		return err
	}
	if result.Aborted {
		fmt.Fprintln(cmd.OutOrStdout(), "Environment switch aborted.")
		return nil
	}
	if result.AlreadyActive {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is already active (%s packages).\n", result.Environment, result.PackageSource)
		return nil
	}
	if result.Restarted {
		fmt.Fprintf(cmd.OutOrStdout(), "Switched to %s (%s packages, %s). Computer restarted and connected.\n", result.Environment, result.PackageSource, result.Version)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Switched to %s (%s packages, %s). Computer remains stopped.\n", result.Environment, result.PackageSource, result.Version)
	return nil
}

var configUseCmd = &cobra.Command{
	Use:   "use <production|test>",
	Short: "Switch the Computer environment and its matching package source",
	Args:  exactArgs(1),
	RunE:  runConfigUse,
}

func init() {
	configUseCmd.Flags().BoolP("yes", "y", false, "Switch without the confirmation prompt")
}
