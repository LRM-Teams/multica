import { describe, expect, it } from "vitest";
import {
  NOTE_WORKER_PLAYBOOKS,
  noteWorkerPlaybookById,
} from "./note-worker-playbooks";

describe("NOTE_WORKER_PLAYBOOKS period_brief", () => {
  it("prefers Agent DM, not a forced channel", () => {
    const playbook = noteWorkerPlaybookById("period_brief");
    expect(playbook).toEqual({ id: "period_brief", prefersChannel: false });
    expect(NOTE_WORKER_PLAYBOOKS.some((item) => item.id === "period_brief")).toBe(true);
  });
});
