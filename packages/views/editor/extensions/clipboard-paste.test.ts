import { describe, expect, it } from "vitest";
import { clipboardPrefersTextOverFiles } from "./clipboard-paste";

function makeFileList(files: File[]): FileList {
  const list = {
    length: files.length,
    item: (i: number) => files[i] ?? null,
    *[Symbol.iterator]() {
      yield* files;
    },
  } as FileList;
  files.forEach((file, index) => {
    Object.defineProperty(list, index, { value: file, enumerable: true });
  });
  return list;
}

function clipboard(text: string, files: File[] = []) {
  return {
    files: makeFileList(files),
    getData: (type: string) => (type === "text/plain" ? text : ""),
  };
}

const officeBitmap = new File(["png"], "image.png", { type: "image/png" });

describe("clipboardPrefersTextOverFiles", () => {
  it("prefers PowerPoint / Office text over the companion bitmap", () => {
    expect(
      clipboardPrefersTextOverFiles(clipboard("季度目标", [officeBitmap])),
    ).toBe(true);
  });

  it("keeps a screenshot paste as files when text/plain is empty", () => {
    expect(clipboardPrefersTextOverFiles(clipboard("", [officeBitmap]))).toBe(
      false,
    );
  });

  it("keeps a Finder / Explorer file copy as files", () => {
    const file = new File(["png"], "slide.png", { type: "image/png" });
    expect(
      clipboardPrefersTextOverFiles(
        clipboard("/Users/me/Desktop/slide.png", [file]),
      ),
    ).toBe(false);
    expect(
      clipboardPrefersTextOverFiles(clipboard("C:\\Users\\me\\slide.png", [file])),
    ).toBe(false);
    expect(
      clipboardPrefersTextOverFiles(
        clipboard("file:///Users/me/Desktop/slide.png", [file]),
      ),
    ).toBe(false);
    expect(clipboardPrefersTextOverFiles(clipboard("slide.png", [file]))).toBe(
      false,
    );
  });

  it("still prefers multiline copied text even if one line looks like a path", () => {
    expect(
      clipboardPrefersTextOverFiles(
        clipboard("季度目标\n/tmp/notes.md", [officeBitmap]),
      ),
    ).toBe(true);
  });
});
