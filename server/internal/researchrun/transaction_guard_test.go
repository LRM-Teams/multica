package researchrun

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type transactionBoundaryCalls struct {
	direct []string
	runner map[string]int
}

func inspectTransactionBoundaryCalls(t *testing.T, source []byte, functionName string) transactionBoundaryCalls {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), functionName+".go", source, 0)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	calls := transactionBoundaryCalls{runner: map[string]int{}}
	found := false
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName || function.Body == nil {
			continue
		}
		found = true
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "BeginTx", "Commit":
				calls.direct = append(calls.direct, selector.Sel.Name)
			case "beginResearchTx", "commitResearchTx":
				calls.runner[selector.Sel.Name]++
			}
			return true
		})
	}
	if !found {
		t.Fatalf("function %s not found", functionName)
	}
	sort.Strings(calls.direct)
	return calls
}

func TestTransactionGuardDetectsDirectBoundary(t *testing.T) {
	source := []byte(`package fixture
import (
    "context"
    "github.com/jackc/pgx/v5"
)
type store struct { pool interface { BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) } }
func (s *store) CreateDispatchIntent(ctx context.Context) error {
    tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil { return err }
    return tx.Commit(ctx)
}`)

	calls := inspectTransactionBoundaryCalls(t, source, "CreateDispatchIntent")
	want := []string{"BeginTx", "Commit"}
	if !reflect.DeepEqual(calls.direct, want) {
		t.Fatalf("direct calls=%v want=%v", calls.direct, want)
	}
}

func TestMigratedTransactionsUseResearchTransactionRunner(t *testing.T) {
	tests := []struct {
		file        string
		function    string
		wantBegins  int
		wantCommits int
	}{
		{file: "postgres_tasks.go", function: "CreateDispatchIntent", wantBegins: 1, wantCommits: 2},
		{file: "postgres_tasks.go", function: "ActivateReadyTasks", wantBegins: 1, wantCommits: 1},
		{file: "postgres_dispatch.go", function: "ClaimDispatchIntents", wantBegins: 1, wantCommits: 1},
		{file: "postgres_dispatch.go", function: "RescheduleDispatchIntent", wantBegins: 1, wantCommits: 1},
		{file: "postgres_dispatch.go", function: "FailDispatchIntent", wantBegins: 1, wantCommits: 3},
		{file: "postgres_dispatch.go", function: "AcknowledgeDispatchIntent", wantBegins: 1, wantCommits: 2},
		{file: "postgres_tasks.go", function: "AttachInboxTask", wantBegins: 1, wantCommits: 2},
		{file: "postgres_tasks.go", function: "FailAttempt", wantBegins: 1, wantCommits: 1},
		{file: "postgres.go", function: "MarkCancellationsRequested", wantBegins: 1, wantCommits: 1},
		{file: "postgres.go", function: "CompleteCancellations", wantBegins: 1, wantCommits: 1},
		{file: "postgres_result.go", function: "AcceptResult", wantBegins: 1, wantCommits: 2},
		{file: "postgres_tasks.go", function: "CreateControlTask", wantBegins: 1, wantCommits: 2},
		{file: "postgres_node_command.go", function: "NodeCommand", wantBegins: 1, wantCommits: 1},
		{file: "postgres_node_command.go", function: "nodeCommandContinueFork", wantBegins: 0, wantCommits: 1},
		{file: "postgres_node_command.go", function: "nodeCommandRetry", wantBegins: 0, wantCommits: 1},
		{file: "postgres_node_command.go", function: "nodeCommandReassign", wantBegins: 0, wantCommits: 1},
		{file: "postgres.go", function: "CreateRun", wantBegins: 1, wantCommits: 1},
		{file: "postgres.go", function: "InitializeRun", wantBegins: 1, wantCommits: 2},
		{file: "postgres.go", function: "ClaimRun", wantBegins: 1, wantCommits: 1},
		{file: "postgres.go", function: "RenewRunLease", wantBegins: 1, wantCommits: 1},
		{file: "postgres.go", function: "ReleaseRun", wantBegins: 1, wantCommits: 1},
		{file: "postgres_tasks.go", function: "SetAwaitingConfirmation", wantBegins: 1, wantCommits: 2},
		{file: "postgres_tasks.go", function: "Complete", wantBegins: 1, wantCommits: 2},
		{file: "postgres_tasks.go", function: "Resume", wantBegins: 1, wantCommits: 2},
		{file: "postgres_tasks.go", function: "transitionRun", wantBegins: 1, wantCommits: 2},
		{file: "postgres_tasks.go", function: "Steer", wantBegins: 1, wantCommits: 1},
		{file: "postgres_gate.go", function: "RecordBudgetExhausted", wantBegins: 1, wantCommits: 2},
		{file: "postgres_gate.go", function: "MarkEventProjected", wantBegins: 1, wantCommits: 1},
		{file: "postgres_gate.go", function: "MarkEventProjectionFailed", wantBegins: 1, wantCommits: 1},
		{file: "postgres_artifact_lifecycle.go", function: "ApplyArtifactLifecycleChange", wantBegins: 1, wantCommits: 2},
		{file: "postgres_gate.go", function: "reconcileAttemptRuntime", wantBegins: 1, wantCommits: 3},
		{file: "postgres_circuit_routing.go", function: "DeferTaskForExecutionTarget", wantBegins: 1, wantCommits: 2},
		{file: "postgres_circuit.go", function: "RecordCircuitFailure", wantBegins: 1, wantCommits: 2},
		{file: "postgres_circuit.go", function: "RecordCircuitSuccess", wantBegins: 1, wantCommits: 5},
		{file: "postgres_circuit.go", function: "ClaimCircuitProbe", wantBegins: 1, wantCommits: 4},
		{file: "postgres_circuit.go", function: "ResolveCircuitProbe", wantBegins: 1, wantCommits: 1},
		{file: "postgres_artifact_supersession.go", function: "SupersedeArtifact", wantBegins: 1, wantCommits: 2},
		{file: "postgres_artifact_lifecycle.go", function: "WithdrawArtifact", wantBegins: 1, wantCommits: 2},
		{file: "postgres_task_inquiry_target.go", function: "BindTaskInquiryTargets", wantBegins: 1, wantCommits: 2},
		{file: "postgres_inquiry_status.go", function: "UpdateInquiryStatus", wantBegins: 1, wantCommits: 2},
	}

	for _, test := range tests {
		t.Run(test.function, func(t *testing.T) {
			source, err := os.ReadFile(test.file)
			if err != nil {
				t.Fatal(err)
			}
			calls := inspectTransactionBoundaryCalls(t, source, test.function)
			if len(calls.direct) != 0 {
				t.Errorf("%s has direct transaction boundaries: %v", test.function, calls.direct)
			}
			if calls.runner["beginResearchTx"] != test.wantBegins || calls.runner["commitResearchTx"] != test.wantCommits {
				t.Errorf(
					"%s runner calls=%v, want %d beginResearchTx and %d commitResearchTx calls",
					test.function, calls.runner, test.wantBegins, test.wantCommits,
				)
			}
		})
	}
}

var readOnlyTransactionExceptions = map[string]struct{}{
	"CanonicalState": {},
	"ListRunEvents":  {},
}

func TestResearchRunPackageUsesTransactionRunnerForMutations(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	violations := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if entry.Name() == "transaction.go" {
			continue
		}
		source, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), entry.Name(), source, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Body == nil {
				continue
			}
			recv := function.Recv.List[0].Type
			star, ok := recv.(*ast.StarExpr)
			if !ok {
				continue
			}
			ident, ok := star.X.(*ast.Ident)
			if !ok || ident.Name != "PostgresStore" {
				continue
			}
			if _, exempt := readOnlyTransactionExceptions[function.Name.Name]; exempt {
				continue
			}
			calls := inspectTransactionBoundaryCalls(t, source, function.Name.Name)
			if len(calls.direct) > 0 {
				violations = append(violations, fmt.Sprintf("%s:%s direct=%v", entry.Name(), function.Name.Name, calls.direct))
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("direct transaction boundaries remain:\n%s", strings.Join(violations, "\n"))
	}
}

func TestResearchTransactionOperationRegistryMatchesUsage(t *testing.T) {
	registry := map[string]researchTxOperation{}
	transactionSource, err := os.ReadFile("transaction.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "transaction.go", transactionSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		genDecl, ok := declaration.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Names) != 1 || len(valueSpec.Values) != 1 {
				continue
			}
			typeIdent, ok := valueSpec.Type.(*ast.Ident)
			if !ok || typeIdent.Name != "researchTxOperation" {
				continue
			}
			lit, ok := valueSpec.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil {
				t.Fatal(unquoteErr)
			}
			registry[valueSpec.Names[0].Name] = researchTxOperation(value)
		}
	}
	if len(registry) == 0 {
		t.Fatal("expected research transaction operation registry")
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]int{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), entry.Name(), source, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "beginResearchTx" && selector.Sel.Name != "commitResearchTx") {
				return true
			}
			if len(call.Args) < 2 {
				return true
			}
			ident, ok := call.Args[1].(*ast.Ident)
			if !ok {
				return true
			}
			used[ident.Name]++
			return true
		})
	}
	for name := range used {
		if _, ok := registry[name]; !ok {
			t.Fatalf("operation constant %q used in production but missing from registry", name)
		}
	}
	for name := range registry {
		if used[name] == 0 {
			t.Fatalf("registry operation %q is never used", name)
		}
	}
}
