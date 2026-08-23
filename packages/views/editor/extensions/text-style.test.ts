import { afterEach, describe, expect, it } from "vitest";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import { TextStyleExtension } from "./text-style";

let editor: Editor | null = null;

function makeEditor(markdown = ""): Editor {
  const element = document.createElement("div");
  document.body.appendChild(element);
  editor = new Editor({
    element,
    extensions: [StarterKit, Markdown, TextStyleExtension],
  });
  if (markdown) editor.commands.setContent(markdown, { contentType: "markdown" });
  return editor;
}

function roundTrip(markdown: string): string {
  return makeEditor(markdown).getMarkdown().trim();
}

afterEach(() => {
  editor?.destroy();
  editor = null;
  document.body.innerHTML = "";
});

describe("TextStyleExtension — markdown serialization", () => {
  it("round-trips a colored span", () => {
    expect(roundTrip('<span style="color: #dc2626">alert</span>')).toBe(
      '<span style="color: #dc2626">alert</span>',
    );
  });

  it("round-trips color and font size together", () => {
    expect(
      roundTrip('<span style="color: #2563eb; font-size: 18px">title</span>'),
    ).toBe('<span style="color: #2563eb; font-size: 18px">title</span>');
  });

  it("serializes setTextColor into a span", () => {
    const e = makeEditor("hello");
    e.commands.selectAll();
    e.commands.setTextColor("#dc2626");
    expect(e.getMarkdown().trim()).toBe('<span style="color: #dc2626">hello</span>');
  });

  it("serializes setFontSize into a span", () => {
    const e = makeEditor("hello");
    e.commands.selectAll();
    e.commands.setFontSize("20px");
    expect(e.getMarkdown().trim()).toBe('<span style="font-size: 20px">hello</span>');
  });

  it("drops javascript and unknown sizes", () => {
    expect(roundTrip('<span style="color: expression(alert(1)); font-size: 99px">x</span>'))
      .toBe("x");
  });

  it("keeps inner bold inside a styled span", () => {
    expect(roundTrip('<span style="color: #16a34a">**ok**</span>')).toBe(
      '<span style="color: #16a34a">**ok**</span>',
    );
  });
});
