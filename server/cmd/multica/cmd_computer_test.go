package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/computer"
)

func TestCommandTestsDoNotConstructRealComputerLifecycle(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), ".", func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range packages {
		for filename, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (selector.Sel.Name != "Stop" && selector.Sel.Name != "Restart" && selector.Sel.Name != "StartBackground") {
					return true
				}
				address, ok := selector.X.(*ast.ParenExpr)
				if !ok {
					return true
				}
				unary, ok := address.X.(*ast.UnaryExpr)
				if !ok || unary.Op != token.AND {
					return true
				}
				literal, ok := unary.X.(*ast.CompositeLit)
				if !ok {
					return true
				}
				lifecycle, ok := literal.Type.(*ast.SelectorExpr)
				pkg, packageOK := lifecycle.X.(*ast.Ident)
				if ok && packageOK && pkg.Name == "computer" && lifecycle.Sel.Name == "Lifecycle" {
					t.Errorf("%s constructs a real Computer lifecycle in a command test; inject the lifecycle seam", filename)
				}
				return true
			})
		}
	}
}

func TestComputerResidentConstructsComputerHostWithoutDaemonContainer(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), ".", func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var foundComputerHost bool
	for _, pkg := range packages {
		for filename, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				declaration, ok := node.(*ast.FuncDecl)
				if !ok || declaration.Name.Name != "runComputerResident" {
					return true
				}
				ast.Inspect(declaration.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					owner, ok := selector.X.(*ast.Ident)
					if !ok {
						return true
					}
					if owner.Name == "daemon" {
						t.Errorf("%s: Computer resident must not depend on internal/daemon (%s)", filename, selector.Sel.Name)
					}
					if owner.Name == "computer" && selector.Sel.Name == "NewHost" {
						foundComputerHost = true
					}
					return true
				})
				return false
			})
		}
	}
	if !foundComputerHost {
		t.Fatal("Computer resident does not construct computer.Host")
	}
}

func TestComputerMachineLifecycleDoesNotDependOnDaemon(t *testing.T) {
	for _, filename := range []string{"cmd_computer_resident.go", "machine_upgrade_detached.go", "cmd_daemon.go"} {
		body, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if strings.Contains(text, "internal/daemon") || strings.Contains(text, "daemon.") {
			t.Errorf("%s mixes the Computer machine lifecycle with internal/daemon", filename)
		}
	}
}

func TestComputerServiceCommandIsHiddenResidentEntry(t *testing.T) {
	if got, want := computerServiceCmd.Use, computer.ResidentServiceArg; got != want {
		t.Fatalf("computer service use = %q, want %q", got, want)
	}
	if !computerServiceCmd.Hidden {
		t.Fatal("computer __service must stay hidden")
	}
	if err := computerServiceCmd.Args(computerServiceCmd, nil); err != nil {
		t.Fatalf("computer __service rejects no arguments: %v", err)
	}
	if err := computerServiceCmd.Args(computerServiceCmd, []string{"extra"}); err == nil {
		t.Fatal("computer __service accepts extra arguments")
	}
	if flag := computerServiceCmd.Flags().Lookup("computer-generation"); flag == nil {
		t.Fatal("computer __service is missing --computer-generation")
	}
	if !hasSubcommand(computerCmd, computer.ResidentServiceArg) {
		t.Fatal("computer command is missing the hidden resident entry")
	}
}

func TestComputerServiceAcceptsPreviousPackageUpgradeBootstrap(t *testing.T) {
	// v0.4.24-alpha.55 launches its detached upgrade successor with this exact
	// argv shape. These are bootstrap compatibility inputs, not a request to
	// restore the retired takeover state machine.
	args := []string{
		"computer", computer.ResidentServiceArg,
		"--computer-generation", "251",
		"--machine-upgrade-detached-candidate",
		"--machine-upgrade-takeover-protocol", "machine-upgrade-takeover-v2:251",
		"--machine-attestation-source-pid", "57261",
	}
	cmd, remaining, err := rootCmd.Find(args)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != computerServiceCmd {
		t.Fatalf("previous-package successor resolved to %q, want computer %s", cmd.CommandPath(), computer.ResidentServiceArg)
	}
	if err := cmd.ParseFlags(remaining); err != nil {
		t.Fatalf("previous-package successor bootstrap rejected: %v", err)
	}
}

func TestComputerRunnerCommandIsHiddenBindingChild(t *testing.T) {
	if got, want := computerRunnerCmd.Use, computer.ResidentRunnerArg; got != want {
		t.Fatalf("computer runner use = %q, want %q", got, want)
	}
	if !computerRunnerCmd.Hidden {
		t.Fatal("computer __runner must stay hidden")
	}
	if flag := computerRunnerCmd.Flags().Lookup("workspace-id"); flag == nil {
		t.Fatal("computer __runner is missing --workspace-id")
	}
	if !hasSubcommand(computerCmd, computer.ResidentRunnerArg) {
		t.Fatal("computer command is missing the hidden Binding child entry")
	}
}

func TestComputerUpgradeCommandUsesBoundComputer(t *testing.T) {
	if got, want := computerUpgradeCmd.Use, "upgrade"; got != want {
		t.Fatalf("computer upgrade use = %q, want %q", got, want)
	}
	if err := computerUpgradeCmd.Args(computerUpgradeCmd, nil); err != nil {
		t.Fatalf("computer upgrade rejects no arguments: %v", err)
	}
	if err := computerUpgradeCmd.Args(computerUpgradeCmd, []string{"daemon-id"}); err == nil {
		t.Fatal("computer upgrade accepts an explicit daemon ID")
	}
	if flag := computerUpgradeCmd.Flags().Lookup("target-version"); flag == nil {
		t.Fatal("computer upgrade is missing --target-version")
	}
	for _, retired := range []string{"wait", "output", "request-id", "download-timeout"} {
		if flag := computerUpgradeCmd.Flags().Lookup(retired); flag != nil {
			t.Fatalf("computer upgrade still exposes split/legacy flag --%s", retired)
		}
	}
}

func TestLegacyTopLevelUpdateCommandIsRemoved(t *testing.T) {
	if hasSubcommand(rootCmd, "update") {
		t.Fatal("top-level multica update must not remain alongside computer upgrade")
	}
}

// #2487/#2490: selectors scope readiness or log/doctor evidence only. Stop and
// status remain strictly machine-wide, and no command exposes a profile.
func TestComputerLifecycleCommandsAreMachineWideWithOnlyDefinedSelectors(t *testing.T) {
	for _, lc := range []*cobra.Command{computerStopCmd, computerStatusCmd} {
		if lc.Args == nil {
			t.Fatalf("%s: no Args validator (must reject positional args)", lc.Name())
		}
		if err := lc.Args(lc, []string{"/ws"}); err == nil {
			t.Fatalf("%s must reject Workspace selectors", lc.Name())
		}
	}
	for _, lc := range []*cobra.Command{computerStartCmd, computerRestartCmd, computerLogsCmd, computerDoctorCmd} {
		if err := lc.Args(lc, []string{"/ws"}); err != nil {
			t.Fatalf("%s rejects its defined Workspace selector: %v", lc.Name(), err)
		}
		if err := lc.Args(lc, []string{"profile"}); err == nil {
			t.Fatalf("%s accepts a non-/ selector that could be confused with a profile", lc.Name())
		}
	}
	for _, lc := range []*cobra.Command{computerStartCmd, computerStopCmd, computerRestartCmd, computerStatusCmd, computerLogsCmd, computerDoctorCmd} {
		if flag := lc.Flags().Lookup("profile"); flag != nil {
			t.Fatalf("%s exposes --profile; Computer is machine-wide", lc.Name())
		}
	}
}

func TestDaemonGroupIsRemoved(t *testing.T) {
	if hasSubcommand(rootCmd, "daemon") {
		t.Fatal("standalone multica daemon must not remain; Computer is the only resident")
	}
}

// Computer-mode resolves profile to the default machine-wide Computer.
func TestComputerModeForcesDefaultProfile(t *testing.T) {
	old := computerMode
	computerMode = true
	t.Cleanup(func() { computerMode = old })

	fake := &cobra.Command{}
	fake.Flags().String("profile", "staging", "")
	if got := resolveProfile(fake); got != "" {
		t.Fatalf("computer-mode resolveProfile = %q, want machine-wide \"\"", got)
	}
}

func TestComputerModeRespectsProfileWhenNotInComputerMode(t *testing.T) {
	old := computerMode
	computerMode = false
	t.Cleanup(func() { computerMode = old })

	fake := &cobra.Command{}
	fake.Flags().String("profile", "staging", "")
	if got := resolveProfile(fake); got != "staging" {
		t.Fatalf("non-computer-mode resolveProfile = %q, want staging", got)
	}
}

func TestComputerIsPrimaryVisibleSurface(t *testing.T) {
	if computerCmd.Hidden {
		t.Fatal("computer group must be the visible primary surface, not hidden")
	}
	for _, name := range []string{"start", "stop", "restart", "status", "logs", "upgrade", "doctor"} {
		if !hasSubcommand(computerCmd, name) {
			t.Fatalf("computer is missing primary subcommand %q", name)
		}
	}
}

func hasSubcommand(cmd interface{ Commands() []*cobra.Command }, name string) bool {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

func TestRetiredProfileSelfHostAndOSServiceSurfacesAreNotPublic(t *testing.T) {
	for _, name := range []string{"supervise", "install-service", "uninstall-service", "service-status"} {
		if hasSubcommand(computerCmd, name) {
			t.Fatalf("retired Computer subcommand %q is still reachable", name)
		}
	}
	if hasSubcommand(setupCmd, "self-host") {
		t.Fatal("retired self-host setup is still reachable")
	}
	for _, name := range []string{"profile", "server-url"} {
		flag := rootCmd.PersistentFlags().Lookup(name)
		if flag == nil || !flag.Hidden {
			t.Fatalf("legacy root flag --%s must be hidden during compatibility cycle", name)
		}
		fake := &cobra.Command{}
		fake.Flags().String(name, "", "")
		if err := fake.Flags().Set(name, "legacy"); err != nil {
			t.Fatal(err)
		}
		if err := rejectRetiredComputerFlags(fake); err == nil {
			t.Fatalf("Computer accepted retired --%s", name)
		}
	}
}
