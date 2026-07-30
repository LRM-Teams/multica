// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  formatFileSize,
  getFileExtension,
  getFileTypeCategory,
  type FileTypeCategory,
} from "./file-meta";

describe("getFileExtension", () => {
  it("lowercases and strips the dot", () => {
    expect(getFileExtension("Report.PDF")).toBe("pdf");
    expect(getFileExtension("archive.tar.gz")).toBe("gz");
    expect(getFileExtension("path/to/logs-2026.zip")).toBe("zip");
  });

  it("returns empty for dotfiles and extensionless names", () => {
    expect(getFileExtension("Dockerfile")).toBe("");
    expect(getFileExtension(".gitignore")).toBe("");
    expect(getFileExtension("")).toBe("");
  });
});

describe("formatFileSize", () => {
  const cases: Array<[number, string]> = [
    [0, "0 B"],
    [12, "12 B"],
    [1023, "1023 B"],
    [1024, "1 KB"],
    [245760, "240 KB"],
    [1468006, "1.4 MB"],
    [1073741824, "1 GB"],
  ];
  for (const [bytes, expected] of cases) {
    it(`formats ${bytes} → ${expected}`, () => {
      expect(formatFileSize(bytes)).toBe(expected);
    });
  }

  it("returns empty string for invalid input", () => {
    expect(formatFileSize(-1)).toBe("");
    expect(formatFileSize(Number.NaN)).toBe("");
    expect(formatFileSize(Number.POSITIVE_INFINITY)).toBe("");
  });
});

describe("getFileTypeCategory", () => {
  const cases: Array<[string, string, FileTypeCategory]> = [
    ["image/png", "avatar.png", "image"],
    ["", "photo.jpeg", "image"],
    ["video/mp4", "clip.mp4", "video"],
    ["audio/mpeg", "note.mp3", "audio"],
    ["application/pdf", "manual.pdf", "pdf"],
    ["", "2026-Q3-report.pdf", "pdf"],
    ["", "logs-2026-07-08.zip", "archive"],
    ["", "backup.tar.gz", "archive"],
    ["", "spec.docx", "word"],
    ["", "budget.xlsx", "excel"],
    ["", "data.csv", "excel"],
    ["", "deck.pptx", "ppt"],
    ["", "server.go", "code"],
    ["", "app.tsx", "code"],
    ["", "config.json", "code"],
    ["", "hello.txt", "text"],
    ["", "README.md", "text"],
    ["application/octet-stream", "firmware.bin", "file"],
    ["", "no-extension", "file"],
  ];
  for (const [contentType, filename, expected] of cases) {
    it(`${filename || "(empty)"} → ${expected}`, () => {
      expect(getFileTypeCategory(contentType, filename)).toBe(expected);
    });
  }
});
