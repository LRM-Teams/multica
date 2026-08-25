import { describe, expect, it } from "vitest";
import { buildNotePageEditPrompt } from "./note-ai-edit-prompt";

const request = {
  instruction: "生成麦克斯韦方程组",
  content: "Notes body",
  contextBefore: "",
  contextAfter: "",
};

describe("buildNotePageEditPrompt", () => {
  it("tells the Editor job to emit KaTeX $ / $$ formulas, not latex fences or \\[ \\]", () => {
    const prompt = buildNotePageEditPrompt(request, "Untitled");

    expect(prompt).toContain("You are the in-note AI assistant for a user's Notion-style note page.");
    expect(prompt).toContain("生成麦克斯韦方程组");
    expect(prompt).toContain("$E=mc^2$");
    expect(prompt).toContain("$$");
    expect(prompt).toContain("Forbidden");
    expect(prompt).toContain("```latex");
    expect(prompt).toContain("\\[...\\]");
    expect(prompt).toContain("\\\\nabla");
  });
});
