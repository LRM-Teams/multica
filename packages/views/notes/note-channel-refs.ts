import type { NotePage, NotePageIssueRef } from "@multica/core/types";

export function notePageChannelRefs(page: NotePage | null | undefined): NotePageIssueRef[] {
  const refs = page?.refs ?? [];
  return refs.filter(
    (ref) => ref.type === "channel" && ref.accessible === true && !!ref.id,
  );
}
