import { describe, expect, it } from "vitest";
import {
  DEFAULT_NOTE_FORMAT,
  cssToNoteFontSize,
  hexToNoteColor,
  noteColorToHex,
  noteFormatCssVars,
  noteFormatExportCss,
  parseNoteFormatDefaults,
  sanitizeTextStyle,
} from "./format";

describe("parseNoteFormatDefaults", () => {
  it("returns the built-in defaults for empty or invalid input", () => {
    expect(parseNoteFormatDefaults(null)).toEqual(DEFAULT_NOTE_FORMAT);
    expect(parseNoteFormatDefaults({ fontFamily: "comic", fontSize: "99", color: "neon" }))
      .toEqual(DEFAULT_NOTE_FORMAT);
  });

  it("keeps known values and fills the rest", () => {
    expect(parseNoteFormatDefaults({ fontFamily: "serif", fontSize: "18" })).toEqual({
      fontFamily: "serif",
      fontSize: "18",
      color: "default",
    });
  });
});

describe("noteFormatCssVars", () => {
  it("omits variables that still match the built-in look", () => {
    expect(noteFormatCssVars(DEFAULT_NOTE_FORMAT)).toEqual({
      "--note-default-font-family": undefined,
      "--note-default-font-size": undefined,
      "--note-default-color": undefined,
    });
  });

  it("emits CSS variables for explicit defaults", () => {
    const vars = noteFormatCssVars({
      fontFamily: "serif",
      fontSize: "18",
      color: "blue",
    });
    expect(vars["--note-default-font-family"]).toContain("Georgia");
    expect(vars["--note-default-font-size"]).toBe("18px");
    expect(vars["--note-default-color"]).toBe("#2563eb");
  });
});

describe("noteFormatExportCss", () => {
  it("always emits a concrete body rule for standalone HTML", () => {
    const css = noteFormatExportCss({ fontFamily: "sans", fontSize: "16", color: "red" });
    expect(css).toContain("font-size: 16px");
    expect(css).toContain("#dc2626");
    expect(css).toContain("ui-sans-serif");
  });
});

describe("sanitizeTextStyle", () => {
  it("keeps palette hex colors and allowed pixel sizes", () => {
    expect(sanitizeTextStyle("color: #DC2626; font-size: 18px")).toEqual({
      color: "#dc2626",
      fontSize: "18px",
    });
  });

  it("drops javascript, urls, and unknown sizes", () => {
    expect(sanitizeTextStyle("color: expression(alert(1)); font-size: 99px")).toEqual({});
    expect(sanitizeTextStyle("color: url(https://evil.test); background: red")).toEqual({});
  });
});

describe("color and size adapters", () => {
  it("round-trips palette colors", () => {
    expect(hexToNoteColor(noteColorToHex("green"))).toBe("green");
    expect(hexToNoteColor("#00ff00")).toBe("default");
  });

  it("round-trips allowed font sizes", () => {
    expect(cssToNoteFontSize("20px")).toBe("20");
    expect(cssToNoteFontSize("13px")).toBeNull();
  });
});
