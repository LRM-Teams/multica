//go:build windows

package main

import "testing"

func TestWindowsTaskNameDefaultProfile(t *testing.T) {
	if got, want := windowsTaskName(""), "MulticaDaemon"; got != want {
		t.Fatalf("windowsTaskName(\"\") = %q, want %q", got, want)
	}
}

func TestWindowsTaskNameNamedProfile(t *testing.T) {
	if got, want := windowsTaskName("staging"), "MulticaDaemon-staging"; got != want {
		t.Fatalf("windowsTaskName(\"staging\") = %q, want %q", got, want)
	}
}

func TestQuoteWindowsArgNoSpecialChars(t *testing.T) {
	if got, want := quoteWindowsArg("--profile"), "--profile"; got != want {
		t.Fatalf("quoteWindowsArg(--profile) = %q, want %q", got, want)
	}
}

func TestQuoteWindowsArgWithSpace(t *testing.T) {
	if got, want := quoteWindowsArg(`C:\Program Files\multica.exe`), `"C:\Program Files\multica.exe"`; got != want {
		t.Fatalf("quoteWindowsArg(path with space) = %q, want %q", got, want)
	}
}

func TestQuoteWindowsArgWithEmbeddedQuote(t *testing.T) {
	got := quoteWindowsArg(`say "hi"`)
	want := `"say \"hi\""`
	if got != want {
		t.Fatalf("quoteWindowsArg(embedded quote) = %q, want %q", got, want)
	}
}

func TestQuoteWindowsArgEmpty(t *testing.T) {
	if got, want := quoteWindowsArg(""), `""`; got != want {
		t.Fatalf("quoteWindowsArg(\"\") = %q, want %q", got, want)
	}
}

func TestSchtasksListFieldExtractsStatus(t *testing.T) {
	sample := "Folder: \\\r\nHostName:                            DESKTOP\r\nTaskName:                             \\MulticaDaemon\r\nStatus:                               Running\r\n"
	if got, want := schtasksListField(sample, "Status:"), "Running"; got != want {
		t.Fatalf("schtasksListField(Status) = %q, want %q", got, want)
	}
}

func TestSchtasksListFieldMissingKeyReturnsEmpty(t *testing.T) {
	sample := "Folder: \\\r\nTaskName: \\MulticaDaemon\r\n"
	if got := schtasksListField(sample, "Status:"); got != "" {
		t.Fatalf("schtasksListField(missing Status) = %q, want empty", got)
	}
}

func TestSchtasksTaskNotFound(t *testing.T) {
	notFound := `ERROR: The system cannot find the file specified.`
	if !schtasksTaskNotFound(notFound) {
		t.Fatal("expected schtasksTaskNotFound to recognize the standard schtasks not-found message")
	}
	other := `ERROR: Access is denied.`
	if schtasksTaskNotFound(other) {
		t.Fatal("schtasksTaskNotFound must not misclassify an unrelated error as not-found")
	}
}
