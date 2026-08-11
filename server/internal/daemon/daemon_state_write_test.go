package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWriteReminderAgentStateConcurrentNoENOENT(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reminder_agents.json")

	const writers = 32
	const rounds = 40
	var failures atomic.Int64
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			payload := []byte(`{"agents":[],"writer":` + strconv.Itoa(id) + `}`)
			for i := 0; i < rounds; i++ {
				if err := writeDaemonStateAtomically(path, payload); err != nil {
					failures.Add(1)
					t.Errorf("writer %d round %d: %v", id, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("concurrent writes produced %d failures", failures.Load())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("final state file empty")
	}
	// No leftover fixed-name tmp from the old path+".tmp" scheme.
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatalf("stale fixed tmp still present: %s.tmp", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat fixed tmp: %v", err)
	}
}
