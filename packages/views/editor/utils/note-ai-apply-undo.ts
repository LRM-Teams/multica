import type { Editor } from "@tiptap/core";
import { toast } from "sonner";

export type NoteAIUndoSnapshot = {
  markdown: string;
  title?: string;
};

export function setEditorMarkdown(editor: Editor, markdown: string) {
  if (editor.markdown) {
    editor.commands.setContent(markdown, { contentType: "markdown" });
    return;
  }
  editor.commands.setContent(markdown);
}

export function captureNoteAIUndoSnapshot(editor: Editor, currentTitle?: string): NoteAIUndoSnapshot {
  return {
    markdown: editor.getMarkdown(),
    title: currentTitle,
  };
}

export function restoreNoteAIUndoSnapshot(
  editor: Editor,
  snapshot: NoteAIUndoSnapshot,
  onApplyTitle?: (title: string) => void,
) {
  setEditorMarkdown(editor, snapshot.markdown);
  if (snapshot.title !== undefined) onApplyTitle?.(snapshot.title);
}

export function showNoteAIApplyUndoToast({
  editor,
  snapshot,
  onApplyTitle,
  message,
  undoLabel,
  onUndo,
}: {
  editor: Editor;
  snapshot: NoteAIUndoSnapshot;
  onApplyTitle?: (title: string) => void;
  message: string;
  undoLabel: string;
  onUndo?: () => void;
}) {
  toast.success(message, {
    action: {
      label: undoLabel,
      onClick: () => {
        restoreNoteAIUndoSnapshot(editor, snapshot, onApplyTitle);
        onUndo?.();
      },
    },
  });
}
