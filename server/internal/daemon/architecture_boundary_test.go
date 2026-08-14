package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDaemonProductionDoesNotOwnComputerHost(t *testing.T) {
	hostType := regexp.MustCompile(`\*computer\.Host(?:[^A-Za-z0-9_]|$)`)
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if hostType.Match(body) || strings.Contains(string(body), "computer.NewHost(") || strings.Contains(string(body), "daemonProcessComputerHost") {
			t.Errorf("%s mixes the Computer Host into daemon execution", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
