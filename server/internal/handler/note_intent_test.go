package handler

import "testing"

func TestEditorCreateMisuseReason(t *testing.T) {
	t.Parallel()
	if got := editorCreateMisuseReason("", "", ""); got != "" {
		t.Fatalf("empty editor body: got %q", got)
	}
	if got := editorCreateMisuseReason(NoteIntentEditor, "", ""); got != "" {
		t.Fatalf("explicit editor: got %q", got)
	}
	if got := editorCreateMisuseReason(NoteIntentWorker, "", ""); got != errNoteEditorRejectsWorker {
		t.Fatalf("worker intent: got %q", got)
	}
	if got := editorCreateMisuseReason("", "do the issue", ""); got != errNoteEditorRejectsWorker {
		t.Fatalf("instruction field: got %q", got)
	}
	if got := editorCreateMisuseReason("", "", "replace_page"); got != errNoteEditorRejectsWorker {
		t.Fatalf("action field: got %q", got)
	}
	if got := editorCreateMisuseReason("weird", "", ""); got != errNoteEditorRejectsWorker {
		t.Fatalf("unknown intent: got %q", got)
	}
}

func TestWorkerCreateMisuseReason(t *testing.T) {
	t.Parallel()
	if got := workerCreateMisuseReason("", "", ""); got != "" {
		t.Fatalf("empty worker body: got %q", got)
	}
	if got := workerCreateMisuseReason(NoteIntentWorker, "", ""); got != "" {
		t.Fatalf("explicit worker: got %q", got)
	}
	if got := workerCreateMisuseReason(NoteIntentEditor, "", ""); got != errNoteWorkerRejectsEditor {
		t.Fatalf("editor intent: got %q", got)
	}
	if got := workerCreateMisuseReason("", "rewrite page", ""); got != errNoteWorkerRejectsEditor {
		t.Fatalf("prompt field: got %q", got)
	}
	if got := workerCreateMisuseReason("", "", "replace_page"); got != errNoteWorkerRejectsEditor {
		t.Fatalf("action field: got %q", got)
	}
}
