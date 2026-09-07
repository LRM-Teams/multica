import { describe, expect, it } from "vitest";
import { reconcileNoteEditorDraft } from "./reconcile-editor-draft";

const baseDraft = {
  title: "Issuing note",
  content: "Existing body",
  serverTitle: "Issuing note",
  serverContent: "Existing body",
};

describe("reconcileNoteEditorDraft", () => {
  it("takes server content when the local draft is clean", () => {
    const next = reconcileNoteEditorDraft(baseDraft, {
      title: "Issuing note",
      content: "Existing body\n\n## 工作介绍 本周\n\nDone.",
    });
    expect(next?.content).toBe("Existing body\n\n## 工作介绍 本周\n\nDone.");
    expect(next?.serverContent).toBe("Existing body\n\n## 工作介绍 本周\n\nDone.");
  });

  it("applies a remote append onto a dirty draft so insert-below is visible", () => {
    // Tiptap getMarkdown() often differs from the last saved snapshot by a
    // trailing newline. That looks "dirty" to the editor, which used to keep
    // the local body and drop a period-brief insert that just landed on the
    // server — toast said inserted, the open note did not.
    const next = reconcileNoteEditorDraft(
      {
        ...baseDraft,
        content: "Existing body\n",
      },
      {
        title: "Issuing note",
        content: "Existing body\n\n## 工作介绍 本周\n\nDone.",
      },
    );
    expect(next?.content).toContain("## 工作介绍 本周");
    expect(next?.content).toContain("Done.");
    expect(next?.serverContent).toBe("Existing body\n\n## 工作介绍 本周\n\nDone.");
  });

  it("keeps in-progress typing ahead of the append suffix", () => {
    const next = reconcileNoteEditorDraft(
      {
        ...baseDraft,
        content: "Existing body plus typing",
      },
      {
        title: "Issuing note",
        content: "Existing body\n\n## 工作介绍 本周\n\nDone.",
      },
    );
    expect(next?.content).toBe("Existing body plus typing\n\n## 工作介绍 本周\n\nDone.");
  });

  it("does not clobber local edits when the remote change is not an append", () => {
    const next = reconcileNoteEditorDraft(
      {
        ...baseDraft,
        content: "Existing body plus typing",
      },
      {
        title: "Issuing note",
        content: "Totally replaced by another tab",
      },
    );
    expect(next).toBeNull();
  });

  it("does not re-append the user's own Enter when the cache echoes the dirty draft", () => {
    // Autosave onMutate writes the open draft into React Query. Reconcile
    // used to treat that echo as a remote insert-below and concatenate the
    // new list item / paragraph onto itself — one Enter became dozens of
    // blank lines, and each render PATCH'd a longer body.
    const next = reconcileNoteEditorDraft(
      {
        title: "TODO",
        content: "- [ ] a\n- [ ] ",
        serverTitle: "TODO",
        serverContent: "- [ ] a",
      },
      {
        title: "TODO",
        content: "- [ ] a\n- [ ] ",
      },
    );
    expect(next?.content ?? "- [ ] a\n- [ ] ").toBe("- [ ] a\n- [ ] ");
  });

  it("does not re-append typed mermaid fence text echoed by autosave", () => {
    const fence = "```mermaid\ngraph TD\n  A-->B\n```";
    const next = reconcileNoteEditorDraft(
      {
        title: "TODO",
        content: `- [ ] a\n\n${fence}`,
        serverTitle: "TODO",
        serverContent: "- [ ] a",
      },
      {
        title: "TODO",
        content: `- [ ] a\n\n${fence}`,
      },
    );
    expect(next?.content ?? `- [ ] a\n\n${fence}`).toBe(`- [ ] a\n\n${fence}`);
  });

  it("does not grow when the same echo is reconciled repeatedly", () => {
    let draft = {
      title: "TODO",
      content: "- [ ] a\n- [ ] ",
      serverTitle: "TODO",
      serverContent: "- [ ] a",
    };
    const selected = { title: "TODO", content: "- [ ] a\n- [ ] " };
    for (let i = 0; i < 8; i++) {
      const next = reconcileNoteEditorDraft(draft, selected);
      if (next) draft = next;
    }
    expect(draft.content).toBe("- [ ] a\n- [ ] ");
  });

  it("does not append pinyin after IME commits Chinese onto a stale autosave snapshot", () => {
    // Mid-composition autosave writes "nihao" into React Query. Space commits
    // 你好 in the editor. The cache still looks like an insert-below of the
    // last saved body, so concat used to produce 你好nihao.
    const next = reconcileNoteEditorDraft(
      {
        title: "TODO",
        content: "Existing body你好",
        serverTitle: "TODO",
        serverContent: "Existing body",
      },
      {
        title: "TODO",
        content: "Existing bodynihao",
      },
    );
    expect(next?.content ?? "Existing body你好").toBe("Existing body你好");
  });
});
