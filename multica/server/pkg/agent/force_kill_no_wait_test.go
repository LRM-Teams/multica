package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// TestForceKillImplementationsNeverCallWait is a static-contract test, not a
// runtime one (task #62, Parker/Nash): the concurrency hazard ForceKill()
// exists to avoid — calling cmd.Wait() while Execute()'s own goroutine may
// still be reading a resident process's stdio — turned out NOT to reliably
// manifest as a race-detector-visible or crashing failure in a real test
// (see TestCursorACPBackendForceKillInterruptsHungTurn's comment for the
// full account of that dead end). A future "helpful" one-line addition of
// cmd.Wait() to any ForceKill() method would pass every runtime test in this
// package and go straight to production. This test converts that untestable
// runtime race into a testable static rule: no ForceKill() method body may
// directly call anything named Wait.
//
// What this does NOT catch (write this down so nobody assumes broader
// coverage than exists): it walks each ForceKill() method's own AST for a
// direct `x.Wait(...)` call. If someone moves the Wait() call into a helper
// function and has ForceKill() call that helper instead, this test stays
// green — it does not trace call chains. Closing that gap would need
// call-graph analysis, which is a real cost for a hazard this narrow; the
// judgment call here is that a direct-call check plus this comment is
// enough, not that the gap doesn't exist.
func TestForceKillImplementationsNeverCallWait(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	fset := token.NewFileSet()
	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || len(name) >= 8 && name[len(name)-8:] == "_test.go" {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "ForceKill" || fn.Recv == nil || fn.Body == nil {
				continue
			}
			found++
			receiverType := forceKillReceiverTypeName(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == "Wait" {
					pos := fset.Position(call.Pos())
					t.Errorf("%s:%d: %s.ForceKill() calls .Wait() directly — ForceKill must never call Wait; that stays the sole responsibility of the goroutine that was already using the process (see the doc comment on any ForceKill implementation for why)", pos.Filename, pos.Line, receiverType)
				}
				return true
			})
		}
	}
	if found == 0 {
		t.Fatal("no ForceKill() method found in this package — this test's file list or method name matching is broken, not proof there's nothing to check")
	}
}

func forceKillReceiverTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "?"
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return "?"
}
