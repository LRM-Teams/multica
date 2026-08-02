package migrations

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// UnsafeNarrowing is one ADD CONSTRAINT ... CHECK (<col> IN (...)) in a
// down.sql that narrows relative to its paired up.sql without a preceding
// remap UPDATE.
type UnsafeNarrowing struct {
	Migration  string
	Constraint string
	Table      string
	Column     string
	Removed    []string // sorted
}

func (n UnsafeNarrowing) String() string {
	return fmt.Sprintf(
		"%s: down.sql narrows constraint %q on %s.%s (removes %s) without an UPDATE ... SET %s ... statement before the narrowing ADD CONSTRAINT — this passes on an empty database and fails the moment a real row holds one of the removed values",
		n.Migration, n.Constraint, n.Table, n.Column, strings.Join(n.Removed, ", "), n.Column,
	)
}

// FindUnsafeNarrowings is a text-level lint for exactly one concrete
// down-migration failure shape (task #97): a down.sql that narrows an
// `ALTER TABLE ... ADD CONSTRAINT ... CHECK (<col> IN (...))` value list
// relative to what the paired up.sql just established, without an UPDATE
// statement on the same table (mentioning the same column) appearing before
// the narrowing ADD CONSTRAINT to remap now-forbidden existing rows first.
//
// Without that UPDATE, the down migration works on an empty database but
// fails the moment a real row uses one of the removed values — which is
// exactly the shape that shipped in 268_agent_workspace_file_audit.down.sql
// before review caught it (task #95 / PR #1834).
//
// Scope: this is a narrow, deliberately imperfect regex-based heuristic —
// not a SQL parser. It catches ONLY the "narrowed CHECK IN(...) list, no
// preceding remap" shape. It does NOT verify that a down migration can
// actually run, and does not catch other ways a down migration can fail —
// including ways that are worse than a loud failure. Two concrete examples
// found during task #97's own history audit:
//   - dropping a column that holds data, dropping an FK-referenced table,
//     or a type change existing rows can't convert to: these fail loudly
//     (ALTER TABLE errors out) — bad, but at least visible and the data
//     survives.
//   - migration 107's down.sql (task #99) narrows comment.author_type by
//     DELETE-ing the now-forbidden rows instead of remapping them. This
//     checker does not flag it — it only looks for narrowed CHECK IN(...)
//     lists, and a DELETE-based rollback has no CHECK-narrowing shape to
//     detect. The down migration SUCCEEDS, silently, and 218 rows of real
//     data are gone with no error to notice. A loud failure is strictly
//     safer than a silent successful data loss; this checker only catches
//     the loud-failure family.
//
// A migration passing this check is not a general guarantee its rollback
// works, or that it doesn't quietly destroy data — see task #97's own
// scope note.
func FindUnsafeNarrowings(migrationName, upSQL, downSQL string) []UnsafeNarrowing {
	upConstraints := extractCheckInConstraints(upSQL)
	downConstraints := extractCheckInConstraints(downSQL)

	var found []UnsafeNarrowing
	for name, down := range downConstraints {
		up, ok := upConstraints[name]
		if !ok {
			// This constraint wasn't touched by the paired up.sql (e.g. an
			// unrelated pre-existing constraint referenced for other
			// reasons) — out of scope for "did THIS migration narrow it".
			continue
		}
		removed := setDifference(up.values, down.values)
		if len(removed) == 0 {
			continue // down.sql's list is equal to or a superset of up's — safe
		}
		if hasPrecedingRemapUpdate(downSQL, down) {
			continue
		}
		found = append(found, UnsafeNarrowing{
			Migration:  migrationName,
			Constraint: name,
			Table:      down.table,
			Column:     down.column,
			Removed:    sortedKeys(removed),
		})
	}
	return found
}

type checkInConstraint struct {
	table  string
	column string
	values map[string]bool
	pos    int // byte offset of the ADD CONSTRAINT statement within the source text
}

var (
	// Matches one ALTER TABLE <table> ... ADD CONSTRAINT <name> ...
	// CHECK ( <column> IN ( <values> ) ) statement. (?s) lets the pattern
	// span the newlines this codebase's migrations format across; matching
	// is applied per already-`;`-split statement (splitStatements) so it
	// cannot accidentally span into an unrelated later statement.
	addConstraintCheckInRe = regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(\w+)\b.*?ADD\s+CONSTRAINT\s+(\w+)\b.*?CHECK\s*\(\s*(\w+)\s+IN\s*\(([^)]*)\)\s*\)`)
	quotedValueRe          = regexp.MustCompile(`'([^']*)'`)
	updateRe               = regexp.MustCompile(`(?is)UPDATE\s+(\w+)\b\s+SET\b(.*?);`)
)

// extractCheckInConstraints finds every ADD CONSTRAINT ... CHECK (col IN
// (...)) statement in sql, keyed by constraint name. pos is the byte offset
// of the statement's ADD CONSTRAINT match start, used to order it against
// any preceding UPDATE.
func extractCheckInConstraints(sql string) map[string]checkInConstraint {
	out := map[string]checkInConstraint{}
	for _, stmt := range splitStatements(sql) {
		m := addConstraintCheckInRe.FindStringSubmatchIndex(stmt.text)
		if m == nil {
			continue
		}
		table := stmt.text[m[2]:m[3]]
		name := stmt.text[m[4]:m[5]]
		column := stmt.text[m[6]:m[7]]
		valuesRaw := stmt.text[m[8]:m[9]]

		values := map[string]bool{}
		for _, vm := range quotedValueRe.FindAllStringSubmatch(valuesRaw, -1) {
			values[vm[1]] = true
		}
		out[name] = checkInConstraint{
			table:  table,
			column: column,
			values: values,
			pos:    stmt.start + m[0],
		}
	}
	return out
}

// hasPrecedingRemapUpdate reports whether downSQL contains an
// `UPDATE <same table> SET ...<column>...` statement whose start offset is
// before the narrowing constraint's own offset.
func hasPrecedingRemapUpdate(downSQL string, c checkInConstraint) bool {
	for _, m := range updateRe.FindAllStringSubmatchIndex(downSQL, -1) {
		start := m[0]
		if start >= c.pos {
			continue // must come before the narrowing ADD CONSTRAINT, not after
		}
		table := downSQL[m[2]:m[3]]
		setClause := downSQL[m[4]:m[5]]
		if table != c.table {
			continue
		}
		// Only the SET assignment proper counts — a WHERE clause happening
		// to mention the column name (e.g. "... WHERE status = 'archived'")
		// is a filter, not a remap, and must not satisfy this check.
		if idx := strings.Index(strings.ToUpper(setClause), "WHERE"); idx != -1 {
			setClause = setClause[:idx]
		}
		if strings.Contains(setClause, c.column) {
			return true
		}
	}
	return false
}

type sqlStatement struct {
	text  string
	start int // byte offset of text within the original source
}

// splitStatements splits sql on top-level `;` terminators. This codebase's
// migrations don't use `;` inside string literals (enum values are short
// bare words), so a naive split is sufficient — a real SQL tokenizer would
// be overkill for a lint whose whole point is staying cheap enough to run
// in CI on every migration.
func splitStatements(sql string) []sqlStatement {
	var out []sqlStatement
	start := 0
	for i, r := range sql {
		if r == ';' {
			out = append(out, sqlStatement{text: sql[start : i+1], start: start})
			start = i + 1
		}
	}
	if strings.TrimSpace(sql[start:]) != "" {
		out = append(out, sqlStatement{text: sql[start:], start: start})
	}
	return out
}

func setDifference(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if !b[k] {
			out[k] = true
		}
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
