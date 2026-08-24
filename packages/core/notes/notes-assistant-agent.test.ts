import { describe, expect, it, vi } from "vitest";
import {
  NOTES_ASSISTANT_AGENT_NAME,
  isNotesAssistantAgent,
  requestInlineNotePageAI,
  resolveNotesAssistantAgent,
} from "./notes-assistant-agent";

describe("notes-assistant-agent", () => {
  it("matches permanent name only", () => {
    expect(isNotesAssistantAgent({ name: NOTES_ASSISTANT_AGENT_NAME })).toBe(true);
    expect(isNotesAssistantAgent({ name: "weekly-report" })).toBe(false);
    expect(isNotesAssistantAgent(null)).toBe(false);
  });

  it("resolves from agent list", () => {
    const agents = [
      { id: "a1", name: "wendy" },
      { id: "a2", name: NOTES_ASSISTANT_AGENT_NAME },
    ];
    expect(resolveNotesAssistantAgent(agents)?.id).toBe("a2");
    expect(resolveNotesAssistantAgent([{ id: "a1", name: "wendy" }])).toBeNull();
  });

  it("opens the notes bubble on send when 笔记助手 is missing", () => {
    const openNotesBubble = vi.fn();
    expect(requestInlineNotePageAI({
      agents: [{ id: "w", name: "wendy" }],
      openNotesBubble,
    })).toBe(false);
    expect(openNotesBubble).toHaveBeenCalledTimes(1);
  });

  it("allows inline empty-line AI when 笔记助手 exists", () => {
    const openNotesBubble = vi.fn();
    expect(requestInlineNotePageAI({
      agents: [{ id: "a2", name: NOTES_ASSISTANT_AGENT_NAME }],
      openNotesBubble,
    })).toBe(true);
    expect(openNotesBubble).not.toHaveBeenCalled();
  });
});
