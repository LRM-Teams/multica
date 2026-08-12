package handler

// Note Editor vs Worker intents (S2-C3).
// Full contract: docs/notes-editor-worker-contract.md
const (
	NoteIntentEditor = "editor"
	NoteIntentWorker = "worker"
)

const (
	errNoteEditorRejectsWorker = "note AI jobs are Editor-only; use POST /api/notes/pages/{id}/worker-jobs for platform work"
	errNoteWorkerRejectsEditor = "note Worker jobs cannot edit note pages; use POST /api/notes/pages/{id}/ai-jobs (Editor)"
	errNoteWorkerInstruction   = "instruction is required"
)

func normalizeNoteIntent(raw string) string {
	switch raw {
	case "", NoteIntentEditor:
		return NoteIntentEditor
	case NoteIntentWorker:
		return NoteIntentWorker
	default:
		return raw
	}
}

// editorCreateMisuseReason returns a client-facing error when an Editor
// (note_ai_job) create body carries Worker intent or Worker-only fields.
func editorCreateMisuseReason(intent, instruction, action string) string {
	if normalizeNoteIntent(intent) == NoteIntentWorker {
		return errNoteEditorRejectsWorker
	}
	if intent != "" && normalizeNoteIntent(intent) != NoteIntentEditor {
		return errNoteEditorRejectsWorker
	}
	if instruction != "" || action != "" {
		return errNoteEditorRejectsWorker
	}
	return ""
}

// workerCreateMisuseReason returns a client-facing error when a Worker
// (note_worker_job) create body carries Editor edit fields or Editor intent.
func workerCreateMisuseReason(intent, prompt, action string) string {
	if normalizeNoteIntent(intent) == NoteIntentEditor && intent != "" {
		return errNoteWorkerRejectsEditor
	}
	if intent != "" && normalizeNoteIntent(intent) != NoteIntentWorker {
		return errNoteWorkerRejectsEditor
	}
	if prompt != "" || action != "" {
		return errNoteWorkerRejectsEditor
	}
	return ""
}
