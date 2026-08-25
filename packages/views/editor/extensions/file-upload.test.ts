import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RefObject } from "react";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";
import { ImageExtension } from "./index";
import {
  createFileUploadExtension,
  uploadAndInsertFile,
  type MediaMode,
} from "./file-upload";
import { createMarkdownPasteExtension } from "./markdown-paste";

function refOf<T>(value: T): RefObject<T> {
  return { current: value };
}

const BLOB_URL = "blob:test-image";
const FINAL_URL = "https://cdn.example.com/photo.png";

let editors: Editor[] = [];
let originalCreateObjectURL: typeof URL.createObjectURL | undefined;
let originalRevokeObjectURL: typeof URL.revokeObjectURL | undefined;

function makeEditor() {
  const element = document.createElement("div");
  document.body.appendChild(element);
  const editor = new Editor({
    element,
    extensions: [
      StarterKit,
      ImageExtension,
      Markdown.configure({ indentation: { style: "space", size: 3 } }),
    ],
  });
  editors.push(editor);
  return editor;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function makeUpload(
  overrides: Partial<UploadResult> & {
    id: string;
    link: string;
    filename: string;
  },
): UploadResult {
  return {
    workspace_id: "ws-1",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "member",
    uploader_id: "user-1",
    url: overrides.link,
    download_url: overrides.link,
    markdown_url: overrides.link,
    content_type: "image/png",
    size_bytes: 1,
    created_at: new Date(0).toISOString(),
    // markdownLink defaults to the same value as `link` so legacy tests
    // continue to assert the previous URL shape unless they pass an
    // explicit override. Real callers always set it to the stable
    // `/api/attachments/<id>/download` path via useFileUpload.
    markdownLink: overrides.link,
    ...overrides,
  };
}

beforeEach(() => {
  originalCreateObjectURL = URL.createObjectURL;
  originalRevokeObjectURL = URL.revokeObjectURL;
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    value: vi.fn(() => BLOB_URL),
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    configurable: true,
    value: vi.fn(),
  });
});

afterEach(() => {
  for (const editor of editors) editor.destroy();
  editors = [];
  document.body.innerHTML = "";

  if (originalCreateObjectURL) {
    Object.defineProperty(URL, "createObjectURL", {
      configurable: true,
      value: originalCreateObjectURL,
    });
  } else {
    delete (URL as Partial<typeof URL>).createObjectURL;
  }

  if (originalRevokeObjectURL) {
    Object.defineProperty(URL, "revokeObjectURL", {
      configurable: true,
      value: originalRevokeObjectURL,
    });
  } else {
    delete (URL as Partial<typeof URL>).revokeObjectURL;
  }
});

function firstImageAttrs(editor: Editor): Record<string, unknown> | null {
  let attrs: Record<string, unknown> | null = null;
  editor.state.doc.descendants((node) => {
    if (attrs) return false;
    if (node.type.name === "image") {
      attrs = node.attrs;
      return false;
    }
    return undefined;
  });
  return attrs;
}

describe("uploadAndInsertFile", () => {
  it("lets typing continue in the trailing paragraph after pasted image upload preview", async () => {
    const editor = makeEditor();
    const upload = deferred<UploadResult | null>();
    const handler = vi.fn(() => upload.promise);
    const file = new File(["image"], "photo.png", { type: "image/png" });

    const uploadTask = uploadAndInsertFile(editor, file, handler);

    expect(handler).toHaveBeenCalledWith(file);
    expect(editor.state.selection.$from.parent.type.name).toBe("paragraph");

    editor.commands.insertContent("after");
    expect(editor.getMarkdown().trimEnd()).toBe(
      [`![photo.png](${BLOB_URL})`, "", "after"].join("\n"),
    );

    upload.resolve(
      makeUpload({ id: "attachment-1", link: FINAL_URL, filename: "photo.png" }),
    );
    await uploadTask;

    const saved = editor.getMarkdown().trimEnd();
    expect(saved).toBe([`![photo.png](${FINAL_URL})`, "", "after"].join("\n"));

    const reparsed = makeEditor();
    reparsed.commands.setContent(saved, { contentType: "markdown" });
    expect(reparsed.getMarkdown().trimEnd()).toBe(saved);
  });


  it("persists markdownLink (the stable per-attachment URL) into the markdown body, not the short-lived storage URL", async () => {
    // Regression pin for MUL-3130 review feedback. useFileUpload returns
    // both `link` (= att.url, short-lived signed `/uploads/<key>?exp&sig`
    // on LocalStorage) and `markdownLink` (= /api/attachments/<id>/download).
    // The editor must persist `markdownLink` so the comment doesn't
    // capture a 30-min signature, while non-markdown callers (avatar
    // pickers, logo upload) keep using `link` for backward compatibility.
    const editor = makeEditor();
    const SIGNED_URL = "/uploads/workspaces/ws-1/photo.png?exp=42&sig=fake";
    const STABLE_URL = "/api/attachments/attachment-7/download";
    const handler = vi.fn(async () =>
      makeUpload({
        id: "attachment-7",
        link: SIGNED_URL,
        markdownLink: STABLE_URL,
        filename: "photo.png",
      }),
    );
    const file = new File(["image"], "photo.png", { type: "image/png" });

    await uploadAndInsertFile(editor, file, handler);

    // The img node ends up with the stable URL as its src — the
    // expiring signed URL never makes it into the persisted markdown.
    const attrs = firstImageAttrs(editor);
    expect(attrs?.src).toBe(STABLE_URL);
    expect(editor.getMarkdown().trimEnd()).toBe(`![photo.png](${STABLE_URL})`);
    expect(editor.getMarkdown()).not.toContain("?exp=");
    expect(editor.getMarkdown()).not.toContain("?sig=");
  });
});

function makeFileList(files: File[]): FileList {
  const list = {
    length: files.length,
    item: (i: number) => files[i] ?? null,
    *[Symbol.iterator]() {
      yield* files;
    },
  } as FileList;
  files.forEach((f, i) => {
    Object.defineProperty(list, i, { value: f, enumerable: true });
  });
  return list;
}

function pasteClipboard(
  editor: Editor,
  options: { files?: File[]; text?: string; html?: string },
): boolean {
  const files = options.files ?? [];
  const event = {
    clipboardData: {
      files: makeFileList(files),
      getData: (type: string) =>
        type === "text/plain"
          ? (options.text ?? "")
          : type === "text/html"
            ? (options.html ?? "")
            : "",
    },
    preventDefault: () => {},
  } as unknown as ClipboardEvent;
  return (
    editor.view.someProp("handlePaste", (handler) =>
      handler(editor.view, event, editor.view.state.selection.content()),
    ) === true
  );
}

function pasteFiles(editor: Editor, files: File[]): boolean {
  return pasteClipboard(editor, { files });
}

function dropFiles(editor: Editor, files: File[]): boolean {
  const event = {
    dataTransfer: { files: makeFileList(files) },
    clientX: 0,
    clientY: 0,
    preventDefault: () => {},
  } as unknown as DragEvent;
  return (
    editor.view.someProp("handleDrop", (handler) =>
      handler(editor.view, event, editor.view.state.selection.content(), false),
    ) === true
  );
}

function makeUploadEditor(options: {
  onUploadFileRef: RefObject<((file: File) => Promise<UploadResult | null>) | undefined>;
  mediaModeRef?: RefObject<MediaMode>;
  onExternalFilesRef?: RefObject<((files: File[]) => void) | undefined>;
}) {
  const element = document.createElement("div");
  document.body.appendChild(element);
  const editor = new Editor({
    element,
    extensions: [
      StarterKit,
      ImageExtension,
      Markdown.configure({ indentation: { style: "space", size: 3 } }),
      createFileUploadExtension(options.onUploadFileRef, {
        mediaModeRef: options.mediaModeRef,
        onExternalFilesRef: options.onExternalFilesRef,
      }),
    ],
  });
  editors.push(editor);
  return editor;
}

describe("createFileUploadExtension — mediaMode external", () => {
  it("paste image calls onExternalFiles and does not insert markdown image", () => {
    const onExternalFiles = vi.fn();
    const onUploadFile = vi.fn(async () =>
      makeUpload({ id: "a1", link: FINAL_URL, filename: "photo.png" }),
    );
    const onUploadFileRef = refOf(onUploadFile as
      | ((file: File) => Promise<UploadResult | null>)
      | undefined);
    const mediaModeRef = refOf<MediaMode>("external");
    const onExternalFilesRef = refOf(onExternalFiles as
      | ((files: File[]) => void)
      | undefined);

    const editor = makeUploadEditor({
      onUploadFileRef,
      mediaModeRef,
      onExternalFilesRef,
    });
    const file = new File(["image"], "photo.png", { type: "image/png" });

    const handled = pasteFiles(editor, [file]);

    expect(handled).toBe(true);
    expect(onExternalFiles).toHaveBeenCalledTimes(1);
    expect(onExternalFiles).toHaveBeenCalledWith([file]);
    expect(onUploadFile).not.toHaveBeenCalled();
    expect(editor.getMarkdown()).not.toContain("![](");
    expect(editor.getMarkdown()).not.toContain("![");
    expect(firstImageAttrs(editor)).toBeNull();
  });

  it("drop image in external mode calls onExternalFiles without inserting", () => {
    const onExternalFiles = vi.fn();
    const onUploadFileRef = refOf(
      vi.fn() as ((file: File) => Promise<UploadResult | null>) | undefined,
    );
    const mediaModeRef = refOf<MediaMode>("external");
    const onExternalFilesRef = refOf(onExternalFiles as
      | ((files: File[]) => void)
      | undefined);

    const editor = makeUploadEditor({
      onUploadFileRef,
      mediaModeRef,
      onExternalFilesRef,
    });
    const file = new File(["image"], "drop.png", { type: "image/png" });

    const handled = dropFiles(editor, [file]);

    expect(handled).toBe(true);
    expect(onExternalFiles).toHaveBeenCalledWith([file]);
    expect(firstImageAttrs(editor)).toBeNull();
  });


  it("dedupes duplicate files from the same paste before calling onExternalFiles", () => {
    const onExternalFiles = vi.fn();
    const onUploadFileRef = refOf(
      undefined as ((file: File) => Promise<UploadResult | null>) | undefined,
    );
    const mediaModeRef = refOf<MediaMode>("external");
    const onExternalFilesRef = refOf(onExternalFiles as
      | ((files: File[]) => void)
      | undefined);

    const editor = makeUploadEditor({
      onUploadFileRef,
      mediaModeRef,
      onExternalFilesRef,
    });
    const file = new File(["image"], "photo.png", { type: "image/png" });

    pasteFiles(editor, [file, file]);

    expect(onExternalFiles).toHaveBeenCalledTimes(1);
    expect(onExternalFiles.mock.calls[0]?.[0]).toHaveLength(1);
  });
});

describe("createFileUploadExtension — Office dual-format clipboard", () => {
  const officeHtml =
    '<html xmlns:o="urn:schemas-microsoft-com:office:office">' +
    "<body><p>季度目标</p></body></html>";
  const bitmap = new File(["png"], "image.png", { type: "image/png" });

  it("does not consume a PowerPoint text copy that also carries a bitmap", () => {
    const onUploadFile = vi.fn(async () =>
      makeUpload({ id: "a1", link: FINAL_URL, filename: "image.png" }),
    );
    const editor = makeUploadEditor({
      onUploadFileRef: refOf(onUploadFile as
        | ((file: File) => Promise<UploadResult | null>)
        | undefined),
    });

    const handled = pasteClipboard(editor, {
      files: [bitmap],
      text: "季度目标",
      html: officeHtml,
    });

    expect(handled).toBe(false);
    expect(onUploadFile).not.toHaveBeenCalled();
    expect(firstImageAttrs(editor)).toBeNull();
  });

  it("still uploads a screenshot when the clipboard has only an image", () => {
    const onUploadFile = vi.fn(async () =>
      makeUpload({ id: "a1", link: FINAL_URL, filename: "image.png" }),
    );
    const editor = makeUploadEditor({
      onUploadFileRef: refOf(onUploadFile as
        | ((file: File) => Promise<UploadResult | null>)
        | undefined),
    });

    const handled = pasteClipboard(editor, { files: [bitmap] });

    expect(handled).toBe(true);
    expect(onUploadFile).toHaveBeenCalledWith(bitmap);
  });

  it("pastes PowerPoint text into the editor instead of uploading the bitmap", () => {
    const onUploadFile = vi.fn(async () =>
      makeUpload({ id: "a1", link: FINAL_URL, filename: "image.png" }),
    );
    const element = document.createElement("div");
    document.body.appendChild(element);
    const editor = new Editor({
      element,
      extensions: [
        StarterKit,
        ImageExtension,
        Markdown.configure({ indentation: { style: "space", size: 3 } }),
        createMarkdownPasteExtension(),
        createFileUploadExtension(
          refOf(onUploadFile as
            | ((file: File) => Promise<UploadResult | null>)
            | undefined),
        ),
      ],
    });
    editors.push(editor);

    const handled = pasteClipboard(editor, {
      files: [bitmap],
      text: "季度目标",
      html: officeHtml,
    });

    expect(handled).toBe(true);
    expect(onUploadFile).not.toHaveBeenCalled();
    expect(editor.getText()).toContain("季度目标");
    expect(firstImageAttrs(editor)).toBeNull();
  });

  it("still uploads a Finder file copy whose text/plain is only the path", () => {
    const file = new File(["png"], "slide.png", { type: "image/png" });
    const onUploadFile = vi.fn(async () =>
      makeUpload({ id: "a1", link: FINAL_URL, filename: "slide.png" }),
    );
    const editor = makeUploadEditor({
      onUploadFileRef: refOf(onUploadFile as
        | ((file: File) => Promise<UploadResult | null>)
        | undefined),
    });

    const handled = pasteClipboard(editor, {
      files: [file],
      text: "/Users/me/Desktop/slide.png",
    });

    expect(handled).toBe(true);
    expect(onUploadFile).toHaveBeenCalledWith(file);
  });
});
