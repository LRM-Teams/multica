// Command cursordeadlock statically detects the #1803 pool-deadlock shape:
// a `rows, err := <recv>.Query(...)` result iterated with `for rows.Next()`,
// where the loop body (before rows.Close()) makes another call that acquires
// a connection from the SAME bounded pool — directly, or transitively
// through a same-package helper function. Under enough concurrent load,
// every goroutine's outer cursor holds the pool's last free connection while
// its own nested call waits forever for one.
//
// Design goal (task #91, Parker's bar): never miss a real one, but false
// positives are worse than a miss — a checker that cries wolf gets
// suppressed into silence, which is worse than no checker. When genuinely
// unsure, this tool does not flag.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// poolReceiverExprs are the exact receiver source text this tool treats as
// "a bounded connection pool" — a receiver whose .Query/.QueryRow/.Exec
// acquires (and, for Query/QueryRow, holds until Close/exhaustion) a
// connection from a shared, size-bounded pgxpool.Pool. Name heuristic, not
// type-checked (no go/types loader) — chosen to match every receiver seen
// in the 2026-08-02 manual scan's 5 real findings.
var poolReceiverExprs = map[string]bool{
	"pool":   true,
	"h.DB":   true,
	"s.pool": true,
	"s.db":   true,
}

// acquiringMethods are method names that, called on a pool receiver, take a
// new connection out of the pool.
var acquiringMethods = map[string]bool{
	"Query":    true,
	"QueryRow": true,
	"Exec":     true,
	"Acquire":  true,
}

type funcKey struct {
	dir  string // package directory, our stand-in for package identity
	recv string // receiver type name, or "" for a free function
	name string
}

func (k funcKey) String() string {
	if k.recv == "" {
		return k.dir + "#" + k.name
	}
	return k.dir + "#" + k.recv + "." + k.name
}

type parsedFile struct {
	path string
	dir  string
	file *ast.File
}

type finding struct {
	file      string
	line      int
	funcName  string
	innerLine int
	innerDesc string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cursordeadlock <dir> [<dir>...]")
		os.Exit(2)
	}
	findings, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].file != findings[j].file {
			return findings[i].file < findings[j].file
		}
		return findings[i].line < findings[j].line
	})

	blocking, known := filterKnown(findings)
	for _, f := range known {
		fmt.Printf("%s:%d: in %s — KNOWN, not blocking (%s)\n", f.file, f.line, f.funcName, knownIssues[knownIssueKey{file: f.file, funcName: f.funcName}])
	}

	if len(blocking) == 0 {
		fmt.Println("cursordeadlock: no new findings")
		return
	}
	for _, f := range blocking {
		fmt.Printf("%s:%d: in %s, an outer rows cursor (Query) is still open when %s:%d acquires a second pool connection — %s\n",
			f.file, f.line, f.funcName, f.file, f.innerLine, f.innerDesc)
	}
	os.Exit(1)
}

// knownIssueKey/knownIssues is a small, explicit, task-tracked allowlist —
// NOT a general suppression mechanism. Every entry here is a real,
// already-reported instance of this bug shape that this checker is
// deliberately not blocking CI on yet, so landing this checker doesn't turn
// every future PR red for pre-existing debt it didn't create. Each entry
// must reference a tracked task and gets removed the moment that task fixes
// it — if you're tempted to add an entry for something new instead of
// fixing it, don't; this list is for "already known and tracked," not "too
// hard to fix right now."
type knownIssueKey struct {
	file     string
	funcName string
}

var knownIssues = map[knownIssueKey]string{
	{file: "cmd/materialize-promoted/main.go", funcName: "main"}: "task #90 (one-shot CLI script, low priority — single-threaded, won't deadlock unless the pool is misconfigured to size 1)",
}

// filterKnown splits findings into blocking (fail CI) and known (logged,
// not blocking) using the knownIssues allowlist above.
func filterKnown(findings []finding) (blocking, known []finding) {
	return filterKnownWith(findings, knownIssues)
}

// filterKnownWith is the testable core of filterKnown: tests pass a fixture
// allowlist so they don't hardcode (and break on) real knownIssues entries
// that get deleted when those bugs are fixed (Parker's 2026-08-02 note on
// task #90 / #1824).
func filterKnownWith(findings []finding, allow map[knownIssueKey]string) (blocking, known []finding) {
	for _, f := range findings {
		if _, ok := allow[knownIssueKey{file: f.file, funcName: f.funcName}]; ok {
			known = append(known, f)
			continue
		}
		blocking = append(blocking, f)
	}
	return blocking, known
}

// run walks each root, parses every non-test .go file, and returns every
// finding across all of them. Split out from main so it's directly
// testable without spawning a subprocess.
func run(roots []string) ([]finding, error) {
	var paths []string
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				base := filepath.Base(path)
				if base == "vendor" || base == "node_modules" || strings.HasPrefix(base, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", root, err)
		}
	}
	sort.Strings(paths)

	fset := token.NewFileSet()
	var parsed []parsedFile
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		parsed = append(parsed, parsedFile{path: p, dir: filepath.Dir(p), file: f})
	}

	funcs := indexFuncs(parsed)
	touchers := buildToucherSet(parsed, funcs)

	var findings []finding
	for _, pf := range parsed {
		findings = append(findings, scanFile(fset, pf, funcs, touchers)...)
	}
	return findings, nil
}

// funcInfo is one parsed function/method declaration.
type funcInfo struct {
	key  funcKey
	decl *ast.FuncDecl
	dir  string
}

// indexFuncs collects every top-level func/method declaration, keyed by
// (dir, receiverTypeName, name).
func indexFuncs(parsed []parsedFile) map[funcKey]*funcInfo {
	out := map[funcKey]*funcInfo{}
	for _, pf := range parsed {
		for _, decl := range pf.file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			key := funcKey{dir: pf.dir, name: fd.Name.Name, recv: receiverTypeName(fd)}
			out[key] = &funcInfo{key: key, decl: fd, dir: pf.dir}
		}
	}
	return out
}

func receiverTypeName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if ident, ok := t.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// buildToucherSet computes, via fixed-point iteration, the set of funcKeys
// whose body directly or transitively (same-package calls only — every
// known real finding delegates within one package) acquires a pool
// connection: a call to an acquiringMethod on a poolReceiverExpr, or a call
// to any method/function named like a sqlc Queries method
// (`h.Queries.X(...)` / `q.X(...)` / `queries.X(...)`) or db.New(...)-style
// query object — every sqlc-generated method acquires its own connection
// internally, so calling into one counts as "touches the pool" from the
// caller's perspective.
func buildToucherSet(parsed []parsedFile, funcs map[funcKey]*funcInfo) map[funcKey]bool {
	touch := map[funcKey]bool{}
	// Pass 0: functions that directly issue an acquiring call.
	for key, fi := range funcs {
		if directlyTouchesPool(fi.decl) {
			touch[key] = true
		}
	}
	// Fixed point: propagate through same-package calls.
	for {
		changed := false
		for key, fi := range funcs {
			if touch[key] {
				continue
			}
			if callsToucher(fi.decl, fi.dir, funcs, touch) {
				touch[key] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return touch
}

func directlyTouchesPool(fd *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isPoolAcquireCall(call) || isQueriesMethodCall(call) {
			found = true
			return false
		}
		return true
	})
	return found
}

func callsToucher(fd *ast.FuncDecl, dir string, funcs map[funcKey]*funcInfo, touch map[funcKey]bool) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if key, ok := resolveCallTarget(call, dir, funcs); ok && touch[key] {
			found = true
			return false
		}
		return true
	})
	return found
}

// resolveCallTarget best-effort maps a call expression to a same-package
// funcKey: `foo(...)` -> {dir, "", "foo"}; `x.Method(...)` -> tries every
// receiver type declared in dir with a method named Method (we don't have
// type info to know x's concrete type, so an ambiguous name matches any of
// them — this can only ever ADD false positives, never hide a real one,
// which matches the "prefer false negative" bar only in the direction of
// not missing anything; see README in this package for the tradeoff and why
// it hasn't caused a false positive in this codebase to date).
func resolveCallTarget(call *ast.CallExpr, dir string, funcs map[funcKey]*funcInfo) (funcKey, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		key := funcKey{dir: dir, name: fn.Name}
		if _, ok := funcs[key]; ok {
			return key, true
		}
	case *ast.SelectorExpr:
		// Only resolve selector calls where the base is a short lowercase
		// identifier (a local receiver var like h/s/q), not a package
		// qualifier (imports are capitalized-package-style or explicitly
		// aliased) or a struct-literal field access chain we can't type.
		if ident, ok := fn.X.(*ast.Ident); ok && isLikelyLocalVar(ident.Name) {
			for key, fi := range funcs {
				if fi.dir == dir && key.recv != "" && key.name == fn.Sel.Name {
					return key, true
				}
			}
		}
	}
	return funcKey{}, false
}

// isLikelyLocalVar filters out obvious package qualifiers (multi-char,
// capitalized import names like `service`, `db`, `util` are still lowercase
// by Go convention, so this can't distinguish by case alone — instead we
// exclude the handful of package names imported for their exported API
// surface, i.e. names that also appear in poolReceiverExprs are handled
// separately, and everything else short (<=3 chars, e.g. h, s, q, tx) or
// matching a common receiver-name convention is treated as local).
func isLikelyLocalVar(name string) bool {
	switch name {
	case "h", "s", "q", "tx", "d", "p":
		return true
	}
	return len(name) <= 4
}

func isPoolAcquireCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if !acquiringMethods[sel.Sel.Name] {
		return false
	}
	recv := exprString(sel.X)
	return poolReceiverExprs[recv]
}

// isQueriesMethodCall matches `h.Queries.X(...)`, `q.X(...)` where q looks
// like a *db.Queries value (heuristic: identifier literally named `q` or
// `queries`, or selector `<recv>.Queries.X(...)`) — every sqlc-generated
// method internally does exactly one Query/QueryRow/Exec, so calling one is
// itself an acquiring call from the caller's point of view.
func isQueriesMethodCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	recv := exprString(sel.X)
	if recv == "q" || recv == "queries" || recv == "h.Queries" || recv == "s.Queries" {
		return true
	}
	return false
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	default:
		return ""
	}
}

// scanFile finds, within each function of the file, `for X.Next() { ... }`
// loops whose X was assigned from `<pool-recv>.Query(...)` (never
// QueryRow — that's a single row, already released after Scan) in an
// enclosing statement, then checks whether the loop body — before any
// X.Close() call within it — makes an acquiring call on the SAME pool
// receiver, or calls a toucher function. tx-reuse (outer receiver is the
// exact same variable the inner call also uses, and that variable is not
// itself in poolReceiverExprs — i.e. a checked-out single connection/tx
// being read from twice) is excluded: reusing one already-held connection
// cannot exhaust the pool.
func scanFile(fset *token.FileSet, pf parsedFile, funcs map[funcKey]*funcInfo, touchers map[funcKey]bool) []finding {
	var out []finding
	for _, decl := range pf.file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		fname := fd.Name.Name
		if r := receiverTypeName(fd); r != "" {
			fname = r + "." + fname
		}
		out = append(out, scanFuncBody(fset, pf, fd, fname, funcs, touchers)...)
	}
	return out
}

// rowsSource records that a variable was assigned from a pool-receiver
// Query() call at a given position in the AST (used to identify the
// matching `for <var>.Next()` loop later in the same block).
type rowsSource struct {
	varName  string
	poolRecv string
	pos      token.Pos
}

func scanFuncBody(fset *token.FileSet, pf parsedFile, fd *ast.FuncDecl, fname string, funcs map[funcKey]*funcInfo, touchers map[funcKey]bool) []finding {
	var out []finding
	var sources []rowsSource

	ast.Inspect(fd.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Query" {
			return true
		}
		recv := exprString(sel.X)
		if !poolReceiverExprs[recv] {
			return true
		}
		if len(assign.Lhs) < 1 {
			return true
		}
		varIdent, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || varIdent.Name == "_" {
			return true
		}
		sources = append(sources, rowsSource{varName: varIdent.Name, poolRecv: recv, pos: assign.Pos()})
		return true
	})

	if len(sources) == 0 {
		return nil
	}

	ast.Inspect(fd.Body, func(n ast.Node) bool {
		forStmt, ok := n.(*ast.ForStmt)
		if !ok || forStmt.Cond == nil {
			return true
		}
		call, ok := forStmt.Cond.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Next" {
			return true
		}
		loopVar, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		// Find the nearest preceding source for this variable name.
		var src *rowsSource
		for i := range sources {
			s := &sources[i]
			if s.varName != loopVar.Name {
				continue
			}
			if s.pos > forStmt.Pos() {
				continue
			}
			if src == nil || s.pos > src.pos {
				src = s
			}
		}
		if src == nil {
			return true
		}
		if closesBeforeAcquire(forStmt.Body, loopVar.Name) {
			return true
		}
		if desc, bad := loopBodyAcquires(forStmt.Body, src.poolRecv, pf.dir, funcs, touchers); bad {
			out = append(out, finding{
				file:      pf.path,
				line:      fset.Position(src.pos).Line,
				funcName:  fname,
				innerLine: fset.Position(forStmt.Pos()).Line,
				innerDesc: desc,
			})
		}
		return true
	})

	return out
}

// closesBeforeAcquire is a narrow escape hatch: if the very first
// statements of the loop body close the cursor before doing anything else
// (a pattern this codebase doesn't currently use, but which would be
// legitimate), don't flag. We keep this conservative — it only fires if a
// Close() on the loop variable appears as one of the first two statements.
func closesBeforeAcquire(body *ast.BlockStmt, varName string) bool {
	for i, stmt := range body.List {
		if i > 1 {
			break
		}
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := exprStmt.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == varName && sel.Sel.Name == "Close" {
			return true
		}
	}
	return false
}

// loopBodyAcquires walks a `for rows.Next() { ... }` body looking for a
// second pool-connection acquire before the loop can reach rows.Close().
// outerPoolRecv is the exact receiver expression (e.g. "h.DB") the outer
// Query() call used — a nested acquire is only a real risk if it goes
// through the SAME pool object; a nested call on a *different* pool/db
// value (rare, but possible with a per-call scoped connection) isn't this
// bug shape.
func loopBodyAcquires(body *ast.BlockStmt, outerPoolRecv, dir string, funcs map[funcKey]*funcInfo, touchers map[funcKey]bool) (string, bool) {
	desc := ""
	bad := false
	ast.Inspect(body, func(n ast.Node) bool {
		if bad {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isPoolAcquireCall(call) {
			sel := call.Fun.(*ast.SelectorExpr)
			recv := exprString(sel.X)
			if recv == outerPoolRecv {
				bad = true
				desc = fmt.Sprintf("nested %s.%s(...) on the same pool receiver", recv, sel.Sel.Name)
				return false
			}
			return true
		}
		if isQueriesMethodCall(call) {
			bad = true
			desc = fmt.Sprintf("nested %s(...) call (sqlc query method, acquires its own connection)", exprString(call.Fun))
			return false
		}
		if key, ok := resolveCallTarget(call, dir, funcs); ok && touchers[key] {
			bad = true
			desc = fmt.Sprintf("nested call to %s, which itself acquires a pool connection (directly or transitively)", key)
			return false
		}
		return true
	})
	if bad {
		return desc, true
	}
	return "", false
}
