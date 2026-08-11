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
	2:  "partial", // trigger/constraint name inventory tests
	3:  "partial", // migration FK negative fixtures
	4:  "partial", // deferred guard tests in migration suite
	5:  "partial", // immutability/watermark CAS tests
	6:  "partial", // backfill diagnostic tests in migration 319
	7:  "partial", // artifact_policy legacy admission matrix
	8:  "partial", // shadow fixture + prompt hash; not full family matrix
	9:  "partial", // access matrix unit tests
	10: "partial", // evaluation compartment integration + researcheval SubjectInput
	11: "partial", // stale state version dispatch; not full race matrix
	12: "partial", // canonical entry order; not concurrent inverse lock
	13: "partial", // eligibility + representation CAS
	14: "covered", // replay/prompt/outbox binding tests
	15: "partial", // dispatch transaction recovery matrix
	16: "covered", // historical live vs frozen manifest tests
	17: "missing", // D gate uses frozen manifest without rubric drift
	18: "missing", // accept race fault matrix after preflight pause
	19: "partial", // unrelated vs affected watermark/eligibility
	20: "missing", // result lock-order concurrency
	21: "partial", // accept replay hash/lineage conflict
	22: "partial", // result accept transaction recovery matrix
	23: "partial", // handler cross-workspace 404; not full principal matrix
	24: "partial", // withdrawal omission + in-flight accept block
	25: "partial", // attempt_context stable metadata + FE schema fallback
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
