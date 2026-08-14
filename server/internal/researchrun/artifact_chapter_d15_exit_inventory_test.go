package researchrun

import (
	"fmt"
	"strings"
	"testing"
)

// chapterD15EnforceComplete gates the Chapter D exit claim. Keep false until all
// §15 acceptance tests are implemented and green on real PostgreSQL CI.
const chapterD15EnforceComplete = false

// chapterD15Coverage tracks §15 acceptance inventory status:
//   - covered: executable test(s) exist and run in CI when DB is available
//   - partial: some but not all required fixtures/matrix rows
//   - missing: no meaningful executable coverage yet
var chapterD15Coverage = map[int]string{
	1:  "covered", // aggregate 318–330 fresh up, reverse down/up, lint, reconciliation, and no fabricated E–N rows
	2:  "covered", // live catalog scope/FK/revision-uniqueness/complete stable guard inventory
	3:  "covered", // legacy domain + normalized D same-scope/cross-session/cross-workspace direct FK matrices
	4:  "partial", // deferred guard tests in migration suite
	5:  "covered", // immutable versions + reciprocal/append-only ledgers + exact revision/watermark guards + concurrent writer convergence
	6:  "partial", // backfill diagnostic tests in migration 319
	7:  "covered", // exhaustive registered/unknown kind × lifecycle × provenance legacy admission matrix
	8:  "partial", // full-family shadow + evidence tie-order + omission mutation; prompt hash covered
	9:  "covered", // closed normal-clearance/purpose/evaluation matrix + output taint permutations
	10: "covered", // structural subject isolation + real subject/grader frozen-manifest dispatch
	11: "covered", // stale-request rollback/recompute across state/version/access/hash/provenance/lifecycle/verification/supersession/watermark
	12: "covered", // canonical candidate locks + concurrent shared candidates + stable entry ordinals
	13: "covered", // full-intent rollback on eligibility/version/access/lifecycle/provenance/content/representation CAS drift
	14: "covered", // replay/prompt/outbox binding tests
	15: "covered", // after-begin/before-commit/after-commit dispatch artifact recovery matrix
	16: "covered", // historical live vs frozen manifest tests
	17: "covered", // exact Gate bytes/hash + policy version + unchanged V1-V5 rubric on frozen Attempt context
	18: "covered", // full preflight race matrix + complete domain/passport/version/ledger rollback write-set assertions
	19: "covered", // unrelated advance reauthorization + affected eligibility denial + final acceptance watermark
	20: "covered", // kind/artifact/version total order + opposite-payload concurrent acceptance without deadlock
	21: "covered", // payload + manifest ID/hash + exact input-version set + producer/watermark lineage replay
	22: "covered", // all 19 semantic checkpoints + begin/commit/unknown recovery + D ledger atomicity guards
	23: "partial", // human/assigned/unbound/unassigned/cross-workspace/header-spoof HTTP matrix; evaluator/projector surfaces open
	24: "covered", // supersession/withdrawal ledger, new-context exclusion, in-flight denial, audit/history preservation
	25: "covered", // stable passport-ID/hash projection + frozen Attempt scope + enum/FE fallback
	26: "covered", // transaction structural guard tests
}

func TestChapterD15ExitCoverageInventory(t *testing.T) {
	var missing, partial []string
	for i := 1; i <= 26; i++ {
		status, ok := chapterD15Coverage[i]
		if !ok {
			status = "missing"
		}
		t.Logf("§15.%d: %s", i, status)
		switch status {
		case "missing":
			missing = append(missing, fmt.Sprintf("#%d", i))
		case "partial":
			partial = append(partial, fmt.Sprintf("#%d", i))
		}
	}
	t.Logf("Chapter D §15 summary: covered=%d partial=%d missing=%d",
		countChapterD15Status("covered"), len(partial), len(missing))
	if chapterD15EnforceComplete {
		if len(missing) > 0 || len(partial) > 0 {
			t.Fatalf("Chapter D exit blocked: missing=%s partial=%s",
				strings.Join(missing, ", "), strings.Join(partial, ", "))
		}
	}
}

func countChapterD15Status(want string) int {
	n := 0
	for i := 1; i <= 26; i++ {
		if chapterD15Coverage[i] == want {
			n++
		}
	}
	return n
}
