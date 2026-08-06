import { describe, expect, it } from "vitest";
import {
  parseCodeFenceInfo,
  serializeCodeFenceInfo,
  normalizeMermaidView,
} from "./code-block-fence";

describe("parseCodeFenceInfo", () => {
  it("treats bare mermaid as both", () => {
    expect(parseCodeFenceInfo("mermaid")).toEqual({
      language: "mermaid",
      mermaidView: "both",
    });
  });

  it("reads mermaid view=diagram / source / both", () => {
    expect(parseCodeFenceInfo("mermaid view=diagram")).toEqual({
      language: "mermaid",
      mermaidView: "diagram",
    });
    expect(parseCodeFenceInfo("mermaid view=source")).toEqual({
      language: "mermaid",
      mermaidView: "source",
    });
    expect(parseCodeFenceInfo("mermaid view=both")).toEqual({
      language: "mermaid",
      mermaidView: "both",
    });
  });

  it("leaves other languages alone", () => {
    expect(parseCodeFenceInfo("python")).toEqual({
      language: "python",
      mermaidView: "both",
    });
    expect(parseCodeFenceInfo("")).toEqual({
      language: "",
      mermaidView: "both",
    });
  });
});

describe("serializeCodeFenceInfo", () => {
  it("omits view= for the default both mode", () => {
    expect(serializeCodeFenceInfo("mermaid", "both")).toBe("mermaid");
  });

  it("encodes non-default mermaid views in the fence", () => {
    expect(serializeCodeFenceInfo("mermaid", "diagram")).toBe(
      "mermaid view=diagram",
    );
    expect(serializeCodeFenceInfo("mermaid", "source")).toBe(
      "mermaid view=source",
    );
  });

  it("round-trips through parse", () => {
    for (const view of ["both", "diagram", "source"] as const) {
      const fence = serializeCodeFenceInfo("mermaid", view);
      expect(parseCodeFenceInfo(fence)).toEqual({
        language: "mermaid",
        mermaidView: normalizeMermaidView(view),
      });
    }
  });
});
