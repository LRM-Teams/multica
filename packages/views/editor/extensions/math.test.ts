import { afterEach, describe, expect, it } from "vitest";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { BlockMathExtension, InlineMathExtension } from "./math";

interface JsonNode {
  type: string;
  text?: string;
  attrs?: Record<string, unknown>;
  content?: JsonNode[];
}

function makeEditor(content: JsonNode) {
  const element = document.createElement("div");
  document.body.appendChild(element);
  return new Editor({
    element,
    extensions: [StarterKit, InlineMathExtension, BlockMathExtension],
    content,
  });
}

function inlineMathShortcut(editor: Editor) {
  const extension = editor.extensionManager.extensions.find(
    (item) => item.name === "inlineMath",
  );
  if (!extension) throw new Error("inlineMath extension not registered");
  const shortcuts = (
    extension.config.addKeyboardShortcuts as
      | (() => Record<string, () => boolean>)
      | undefined
  )?.bind({
    editor,
    name: "inlineMath",
    options: extension.options,
    type: editor.schema.nodes.inlineMath,
    storage: extension.storage,
  } as never)();
  const shortcut = shortcuts?.["Mod-Shift-e"];
  if (!shortcut) throw new Error("inline math shortcut not bound");
  return shortcut();
}

describe("math extensions", () => {
  let editor: Editor | undefined;

  afterEach(() => {
    editor?.destroy();
    editor = undefined;
    document.body.innerHTML = "";
  });

  it("wraps selected text as inline math with Mod-Shift-E", () => {
    editor = makeEditor({
      type: "doc",
      content: [
        { type: "paragraph", content: [{ type: "text", text: "a+b" }] },
      ],
    });
    editor.commands.setTextSelection({ from: 1, to: 4 });

    expect(inlineMathShortcut(editor)).toBe(true);

    expect(editor.getJSON()).toEqual({
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            { type: "inlineMath", attrs: { expression: "a+b", editOnCreate: true } },
          ],
        },
      ],
    });
  });

  it("marks an empty-selection shortcut formula to open its editor immediately", () => {
    editor = makeEditor({ type: "doc", content: [{ type: "paragraph" }] });
    editor.commands.setTextSelection(1);

    expect(inlineMathShortcut(editor)).toBe(true);

    expect(editor.getJSON().content?.[0]?.content?.[0]).toEqual({
      type: "inlineMath",
      attrs: { expression: "x^2", editOnCreate: true },
    });
  });

  it("inserts a block math node through the command", () => {
    editor = makeEditor({ type: "doc", content: [{ type: "paragraph" }] });

    expect(editor.commands.setBlockMath("E = mc^2")).toBe(true);

    expect(editor.getJSON().content?.some(
      (node) => node.type === "blockMath" && node.attrs?.expression === "E = mc^2",
    )).toBe(true);
  });
});
