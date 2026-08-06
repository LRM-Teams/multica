package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSkillFilesRejectsEscapingSupportingFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	err := writeSkillFiles(skillsDir, []SkillContextForEnv{{
		Name:    "unsafe",
		Content: "# Unsafe\n",
		Files:   []SkillFileContextForEnv{{Path: "../escaped.md", Content: "escaped"}},
	}}, nil)
	if err == nil {
		t.Fatal("expected escaping supporting file path to fail")
	}
	if _, statErr := os.Stat(filepath.Join(skillsDir, "escaped.md")); !os.IsNotExist(statErr) {
		t.Fatalf("escaping file was written, err=%v", statErr)
	}
}

func TestSafeSkillFilePathAcceptsNestedRelativePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got, err := safeSkillFilePath(root, "references/guide.md")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "references", "guide.md")
	if got != want {
		t.Fatalf("path=%q want=%q", got, want)
	}
}
