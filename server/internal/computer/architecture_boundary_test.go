package computer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestComputerProductionOwnsNoBindingExecutionTypes(t *testing.T) {
	forbiddenIdentifiers := map[string]struct{}{
		"WorkspaceRunner": {}, "AgentProcessManager": {}, "agentProcessManager": {},
		"MessageCoordinator": {}, "messageCoordinator": {},
		"LocalReminderInbox": {}, "canonicalAgentRuntimePool": {},
		"mixedRunActivityOutbox": {}, "agentActivityProducer": {}, "inboxRegistry": {},
		"localAgentAttachmentRegistry": {},
	}
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if value == "github.com/multica-ai/multica/server/internal/daemon" {
				t.Errorf("%s imports Binding execution package internal/daemon", path)
			}
		}
		file, err = parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			if _, forbidden := forbiddenIdentifiers[identifier.Name]; forbidden {
				t.Errorf("%s owns Binding execution identifier %s", path, identifier.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
