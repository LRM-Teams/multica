import { describe, expect, it } from "vitest";
import {
  NOTES_ASSISTANT_AGENT_NAME,
  isNotesAssistantAgent,
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
});
