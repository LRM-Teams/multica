package computer

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestGenerationStoreIsMonotonicAndConcurrent(t *testing.T) {
	root := t.TempDir()
	store := NewGenerationStore(root)
	const count = 16
	values := make(chan int64, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := NewGenerationStore(root).Next()
			if err != nil {
				t.Errorf("Next: %v", err)
				return
			}
			values <- value
		}()
	}
	wg.Wait()
	close(values)
	seen := map[int64]bool{}
	for value := range values {
		seen[value] = true
	}
	if len(seen) != count || store.Current() != count {
		t.Fatalf("generations=%v current=%d, want unique 1..%d", seen, store.Current(), count)
	}
}

func TestGenerationStoreIgnoresStaleUnlockedLockFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, generationLockFile), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := NewGenerationStore(root).Next()
	if err != nil || value != 1 {
		t.Fatalf("Next with stale lock file = %d, %v", value, err)
	}
}
