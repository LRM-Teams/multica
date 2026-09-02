package computer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePeriodBriefCollectRootsKeepsUserRootsAndRejectsHome(t *testing.T) {
	home := "/home/owner"
	got, err := NormalizePeriodBriefCollectRoots([]string{
		" ~/code ",
		"/home/owner/multica",
		"/home/owner/code",
		"",
		"~/code",
	}, home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"~/code", "/home/owner/multica"}
	if len(got) != len(want) {
		t.Fatalf("roots = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roots = %#v, want %#v", got, want)
		}
	}
}

func TestNormalizePeriodBriefCollectRootsRejectsFilesystemAndSecretRoots(t *testing.T) {
	home := "/home/owner"
	cases := []string{"/", "~", "$HOME", "${HOME}", "/home/owner", "/home/owner/.ssh", "/home/owner/.multica", "C:\\"}
	for _, root := range cases {
		got, err := NormalizePeriodBriefCollectRoots([]string{root}, home)
		if err == nil {
			t.Fatalf("%q: expected error, got %#v", root, got)
		}
	}
}

func TestNormalizePeriodBriefCollectRootsEmptyMeansUnset(t *testing.T) {
	got, err := NormalizePeriodBriefCollectRoots(nil, "/home/owner")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty = %#v err=%v", got, err)
	}
	got, err = NormalizePeriodBriefCollectRoots([]string{"  ", ""}, "/home/owner")
	if err != nil || len(got) != 0 {
		t.Fatalf("whitespace = %#v err=%v", got, err)
	}
}

func TestPeriodBriefCollectRootsFileRoundTrip(t *testing.T) {
	root := t.TempDir()
	if got, err := ReadPeriodBriefCollectRoots(root); err != nil || len(got) != 0 {
		t.Fatalf("missing file should be unset: %#v err=%v", got, err)
	}
	if err := WritePeriodBriefCollectRoots(root, []string{"~/work", "/opt/app"}, "/home/owner"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPeriodBriefCollectRoots(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "~/work" || got[1] != "/opt/app" {
		t.Fatalf("read back %#v", got)
	}
	if err := WritePeriodBriefCollectRoots(root, nil, "/home/owner"); err != nil {
		t.Fatal(err)
	}
	got, err = ReadPeriodBriefCollectRoots(root)
	if err != nil || len(got) != 0 {
		t.Fatalf("cleared file should be unset: %#v err=%v", got, err)
	}
}

func TestComputerCorePeriodBriefCollectRootsPersist(t *testing.T) {
	root := t.TempDir()
	host := &ComputerCore{workJournalRoot: root, workJournalHome: "/home/owner"}
	if len(host.PeriodBriefCollectRoots()) != 0 {
		t.Fatal("default should be unset")
	}
	if err := host.SetPeriodBriefCollectRoots([]string{"~/code"}); err != nil {
		t.Fatal(err)
	}
	if got := host.PeriodBriefCollectRoots(); len(got) != 1 || got[0] != "~/code" {
		t.Fatalf("in-memory %#v", got)
	}
	reloaded := &ComputerCore{workJournalRoot: root, workJournalHome: "/home/owner"}
	reloaded.loadPeriodBriefCollectRoots()
	if got := reloaded.PeriodBriefCollectRoots(); len(got) != 1 || got[0] != "~/code" {
		t.Fatalf("reloaded %#v", got)
	}
	if _, err := os.Stat(filepath.Join(root, periodBriefCollectRootsFileName)); err != nil {
		t.Fatalf("expected resident file: %v", err)
	}
}
