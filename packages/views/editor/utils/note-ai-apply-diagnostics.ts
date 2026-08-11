import { captureEvent } from "@multica/core/analytics";
import type { NoteAIEditResult } from "@multica/core/types";

export type NoteAIApplySurface = "selected_text" | "page";
export type NoteAIApplyOutcome = "applied" | "undo_clicked" | "invalid_markdown" | "patch_target_missing";

export function captureNoteAIApplyDiagnostic({
  surface,
  outcome,
  result,
}: {
  surface: NoteAIApplySurface;
  outcome: NoteAIApplyOutcome;
  result?: NoteAIEditResult | null;
}) {
  captureEvent("note_ai_edit_apply_result", {
    surface,
    outcome,
    action: result?.action ?? "unknown",
    title_suggested: Boolean(result?.title),
    has_patch_target: Boolean(result?.target?.trim()),
    markdown_length: result?.markdown.length ?? 0,
  });
}
