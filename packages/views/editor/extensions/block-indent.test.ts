import { afterEach, describe, expect, it } from "vitest";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { BlockIndentExtension } from "./block-indent";
import { PatchedListItem } from "./list-item";

interface JsonNode {
  type: string;
  text?: string;
  content?: JsonNode[];
}

function makeEditor(content: JsonNode) {
  const element = document.createElement("div");
  document.body.appendChild(element);
  return new Editor({
    element,
    extensions: [
      StarterKit.configure({ listItem: false }),
      PatchedListItem,
      BlockIndentExtension,
    ],
    content,
  });
}

function paragraphTextPos(editor: Editor, index: number) {
  let count = 0;
  let pos = -1;
  editor.state.doc.descendants((node, p) => {
    if (node.type.name === "paragraph") {
      if (count === index) {
        pos = p + 1;
        return false;
      }
      count += 1;
    }
    return true;
  });
  if (pos < 0) throw new Error(`no paragraph at index ${index}`);
  return pos;
}

function pressTab(editor: Editor) {
  const extension = editor.extensionManager.extensions.find(
    (item) => item.name === "blockIndent",
  );
  if (!extension) throw new Error("blockIndent extension not registered");
  const shortcuts = (
    extension.config.addKeyboardShortcuts as
      | (() => Record<string, () => boolean>)
      | undefined
  )?.bind({ editor, options: extension.options, storage: extension.storage } as never)();
  const tab = shortcuts?.Tab;
  if (!tab) throw new Error("Tab shortcut not bound");
  return tab();
}

describe("BlockIndentExtension", () => {
  let editor: Editor | undefined;

  afterEach(() => {
    editor?.destroy();
    editor = undefined;
    document.body.innerHTML = "";
  });

  it("indents a top-level paragraph at line start under the previous paragraph", () => {
    editor = makeEditor({
      type: "doc",
      content: [
        { type: "paragraph", content: [{ type: "text", text: "Parent" }] },
        { type: "paragraph", content: [{ type: "text", text: "Child" }] },
      ],
    });
    editor.commands.setTextSelection(paragraphTextPos(editor, 1));

    expect(pressTab(editor)).toBe(true);

    expect(editor.getJSON()).toEqual({
      type: "doc",
      content: [
        {
          type: "bulletList",
          content: [
            {
              type: "listItem",
              content: [
                { type: "paragraph", content: [{ type: "text", text: "Parent" }] },
                {
                  type: "bulletList",
                  content: [
                    {
                      type: "listItem",
                      content: [
                        { type: "paragraph", content: [{ type: "text", text: "Child" }] },
                      ],
                    },
                  ],
                },
              ],
            },
          ],
        },
      ],
    });
  });

  it("does not indent the first paragraph in a note", () => {
    editor = makeEditor({
      type: "doc",
      content: [
        { type: "paragraph", content: [{ type: "text", text: "First" }] },
      ],
    });
    editor.commands.setTextSelection(paragraphTextPos(editor, 0));

    expect(pressTab(editor)).toBe(false);
    expect(editor.getJSON().content?.[0]?.type).toBe("paragraph");
  });

  it("only indents when the cursor is at the start of the line", () => {
    editor = makeEditor({
      type: "doc",
      content: [
        { type: "paragraph", content: [{ type: "text", text: "Parent" }] },
        { type: "paragraph", content: [{ type: "text", text: "Child" }] },
      ],
    });
    editor.commands.setTextSelection(paragraphTextPos(editor, 1) + 1);

    expect(pressTab(editor)).toBe(false);
    expect(editor.getJSON().content?.[1]?.type).toBe("paragraph");
  });
});
