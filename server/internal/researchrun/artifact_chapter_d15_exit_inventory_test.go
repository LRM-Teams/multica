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
	1:  "partial", // migration 318–330 roundtrip tests
	2:  "covered", // live catalog scope/FK/revision-uniqueness/complete stable guard inventory
	3:  "partial", // migration FK negative fixtures
	4:  "partial", // deferred guard tests in migration suite
	5:  "partial", // immutability/watermark CAS tests
	6:  "partial", // backfill diagnostic tests in migration 319
	7:  "covered", // exhaustive registered/unknown kind × lifecycle × provenance legacy admission matrix
	8:  "partial", // full-family shadow + evidence tie-order + omission mutation; prompt hash covered
	9:  "covered", // closed normal-clearance/purpose/evaluation matrix + output taint permutations
	10: "covered", // structural subject isolation + real subject/grader frozen-manifest dispatch
	11: "partial", // dispatch race: eligibility/state/hash/lifecycle after rolled-back intent
	12: "covered", // canonical candidate locks + concurrent shared candidates + stable entry ordinals
	13: "partial", // eligibility + representation CAS
	14: "covered", // replay/prompt/outbox binding tests
	15: "partial", // dispatch transaction recovery matrix
	16: "covered", // historical live vs frozen manifest tests
	17: "partial", // frozen gate snapshot on TaskContextForAttempt
	18: "partial", // accept race: eligibility/lifecycle/hash/manifest/repr/watermark/state + Task contract/dependencies after rollback
	19: "partial", // unrelated watermark advance after rolled-back accept
	20: "partial", // result lock-order concurrency + normalized manifest locks
	21: "covered", // payload + manifest ID/hash + exact input-version set + producer/watermark lineage replay
	22: "partial", // result accept transaction recovery matrix
	23: "partial", // human/assigned/unbound/unassigned/cross-workspace/header-spoof HTTP matrix; evaluator/projector surfaces open
	24: "partial", // withdrawal/acceptance ledger + revoked frozen-read denial/history preservation
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
