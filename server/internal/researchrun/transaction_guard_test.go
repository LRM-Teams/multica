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

func TestCreateDispatchIntentUsesResearchTransactionRunner(t *testing.T) {
	source, err := os.ReadFile("postgres_tasks.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := inspectTransactionBoundaryCalls(t, source, "CreateDispatchIntent")
	if len(calls.direct) != 0 {
		t.Errorf("CreateDispatchIntent has direct transaction boundaries: %v", calls.direct)
	}
	if calls.runner["beginResearchTx"] != 1 || calls.runner["commitResearchTx"] != 2 {
		t.Errorf("CreateDispatchIntent runner calls=%v, want one beginResearchTx and two commitResearchTx exits", calls.runner)
	}
}
