import { describe, expect, it } from "vitest";
import { NotePageIssueRefSchema, NotePageSchema } from "../api/schemas";

describe("NotePageIssueRefSchema (S1-R3)", () => {
  it("keeps accessible refs with label", () => {
    const parsed = NotePageIssueRefSchema.parse({
      type: "issue",
      id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      label: "MUL-1",
      accessible: true,
      title: "Ship it",
    });
    expect(parsed.accessible).toBe(true);
    expect(parsed.label).toBe("MUL-1");
    expect(parsed.title).toBe("Ship it");
  });

  it("accepts agent and run refs (S2-R1)", () => {
    expect(
      NotePageIssueRefSchema.parse({
        type: "agent",
        id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
        accessible: true,
        label: "Deepseek",
      }).type,
    ).toBe("agent");
    expect(
      NotePageIssueRefSchema.parse({
        type: "run",
        id: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
        accessible: true,
        label: "run",
        agent_id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      }).agent_id,
    ).toBe("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
  });
});

describe("NotePageSchema refs", () => {
  it("parses detail payloads that embed structured refs", () => {
    const parsed = NotePageSchema.parse({
      id: "p1",
      workspace_id: "w1",
      owner_user_id: "u1",
      title: "Note",
      content: "hi",
      refs: [
        { type: "issue", id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", accessible: true, label: "MUL-1" },
        { type: "issue", id: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", accessible: false },
      ],
    });
    expect(parsed.refs).toHaveLength(2);
    expect(parsed.refs?.[0]?.accessible).toBe(true);
    expect(parsed.refs?.[1]?.accessible).toBe(false);
  });
});
