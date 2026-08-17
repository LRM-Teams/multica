/**
 * Note Worker collaboration playbooks (todo N1-T1 / N1-T2) plus Period Work
 * Brief (`period_brief`, Slice J3).
 *
 * Templates fill the instruction textarea only — still one Worker job, no new
 * orchestration backend. `prefersChannel` drives destination UX hints.
 */

export type NoteWorkerPlaybookId = "coordinate" | "hire" | "writeback" | "period_brief";

export type NoteWorkerPlaybook = {
  id: NoteWorkerPlaybookId;
  /** Collaboration scripts should run in a group channel when possible. */
  prefersChannel: boolean;
};

/** Stable order for the dialog chip row. */
export const NOTE_WORKER_PLAYBOOKS: readonly NoteWorkerPlaybook[] = [
  { id: "coordinate", prefersChannel: true },
  { id: "hire", prefersChannel: true },
  { id: "writeback", prefersChannel: true },
  { id: "period_brief", prefersChannel: false },
] as const;

export function noteWorkerPlaybookById(
  id: string | null | undefined,
): NoteWorkerPlaybook | null {
  if (!id) return null;
  return NOTE_WORKER_PLAYBOOKS.find((playbook) => playbook.id === id) ?? null;
}
