package researchrun

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
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
