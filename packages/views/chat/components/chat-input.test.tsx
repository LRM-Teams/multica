import { forwardRef, useRef, useImperativeHandle } from "react";
import { beforeEach, describe, it, expect, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";

function makeUpload(overrides: Partial<UploadResult> & { id: string; link: string; filename: string }): UploadResult {
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
    // markdownLink defaults to the same value as `link` so legacy
    // tests assert the previous URL shape unless they pass an
    // explicit override. Real callers always set it to the stable
    // /api/attachments/<id>/download path via useFileUpload.
    markdownLink: overrides.link,
    ...overrides,
  };
}

const TEST_RESOURCES = { en: { common: enCommon, chat: enChat } };

// Track drop-zone callbacks so the test can simulate a real drop.
const dropHandlers = vi.hoisted(() => ({
  onDrop: null as null | ((files: File[]) => void),
}));
const editorProps = vi.hoisted(() => ({
  last: null as null | Record<string, unknown>,
}));

vi.mock("../../editor", () => ({
  useFileDropZone: ({ onDrop }: { onDrop: (files: File[]) => void }) => {
    dropHandlers.onDrop = onDrop;
    return { isDragOver: false, dropZoneProps: { "data-testid": "drop-zone" } };
  },
  FileDropOverlay: () => null,
  ContentEditor: forwardRef(function MockContentEditor(
    props: {
      defaultValue?: string;
      onUpdate?: (md: string) => void;
      placeholder?: string;
      onUploadFile?: (file: File) => Promise<UploadResult | null>;
      mentionMode?: string;
      mentionContextItems?: unknown[];
    },
    ref: React.Ref<unknown>,
  ) {
    const {
      defaultValue,
      onUpdate,
      placeholder,
      onUploadFile,
    } = props;
    editorProps.last = props as unknown as Record<string, unknown>;
    const valueRef = useRef<string>(defaultValue ?? "");
    const uploadingRef = useRef(0);
    useImperativeHandle(ref, () => ({
      getMarkdown: () => valueRef.current,
      clearContent: () => {
        valueRef.current = "";
      },
      blur: () => {},
      focus: () => {},
      uploadFile: async (file: File) => {
        uploadingRef.current += 1;
        try {
          const result = await onUploadFile?.(file);
          if (result) {
            // Mirror the real editor (uploadAndInsertFile in
            // packages/views/editor/extensions/file-upload.ts): the
            // markdown body captures `markdownLink` (the stable
            // /api/attachments/<id>/download URL) when the upload
            // returned one, falling back to `link` for the
            // no-workspace avatar branch. The chat input's
            // uploadMapRef must use the same value as its key —
            // pinning that contract is the regression below.
            const persistedURL = result.markdownLink || result.link;
            valueRef.current = `${valueRef.current}![](${persistedURL})`.trim();
            onUpdate?.(valueRef.current);
          }
        } finally {
          uploadingRef.current = Math.max(0, uploadingRef.current - 1);
        }
      },
      hasActiveUploads: () => uploadingRef.current > 0,
    }));
    return (
      <textarea
        data-testid="editor"
        placeholder={placeholder}
        onChange={(e) => {
          valueRef.current = e.target.value;
          onUpdate?.(e.target.value);
        }}
      />
    );
  }),
}));

// Mock chat store with an in-memory implementation that supports both
// (selector) calls and getState().
vi.mock("@multica/core/chat", () => {
  const state = {
    activeSessionId: null as string | null,
    selectedAgentId: "agent-1",
    inputDrafts: {} as Record<string, string>,
    setInputDraft: vi.fn(),
    clearInputDraft: vi.fn(),
  };
  return {
    DRAFT_NEW_SESSION: "__draft_new__",
    useChatStore: Object.assign(
      (selector?: (s: typeof state) => unknown) =>
        selector ? selector(state) : state,
      { getState: () => state },
    ),
  };
});

import { ChatInput } from "./chat-input";
import { useChatStore } from "@multica/core/chat";

beforeEach(() => {
  dropHandlers.onDrop = null;
  editorProps.last = null;
  const state = useChatStore.getState() as unknown as {
    activeSessionId: string | null;
    selectedAgentId: string;
    inputDrafts: Record<string, string>;
    setInputDraft: ReturnType<typeof vi.fn>;
    clearInputDraft: ReturnType<typeof vi.fn>;
  };
  state.activeSessionId = null;
  state.selectedAgentId = "agent-1";
  state.inputDrafts = {};
  state.setInputDraft.mockClear();
  state.clearInputDraft.mockClear();
});

function renderInput(props: Partial<React.ComponentProps<typeof ChatInput>> = {}) {
  const onSend = props.onSend ?? vi.fn();
  const onUploadFile =
    "onUploadFile" in props
      ? props.onUploadFile
      : vi.fn(async (_file: File) =>
          makeUpload({ id: "att-1", link: "https://cdn.example/att-1.png", filename: "img.png" }),
        );
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <ChatInput
        {...props}
        onSend={onSend}
        onUploadFile={onUploadFile}
        agentName={props.agentName ?? "Multica"}
        agentId={props.agentId ?? "agent-1"}
        wsId={props.wsId ?? "ws-1"}
      />
    </I18nProvider>,
  );
  return { onSend, onUploadFile };
}

describe("ChatInput @ context wiring", () => {
  it("configures chat @ with current/recent issue/project context", () => {
    const contextItems = [
      { id: "issue-1", label: "MUL-1", type: "issue" as const, group: "current" as const },
    ];

    renderInput({ contextItems });

    expect(editorProps.last?.mentionMode).toBe("context");
    expect(editorProps.last?.mentionContextItems).toBe(contextItems);
  });
});

describe("ChatInput Enter-to-send", () => {
  it("enables bare Enter submit for FAB / chat bubbles", () => {
    renderInput();
    expect(editorProps.last?.submitOnEnter).toBe(true);
  });
});

describe("ChatInput attachment wiring", () => {
  it("routes dropped files through the editor's upload handler", async () => {
    const { onUploadFile } = renderInput();
    expect(dropHandlers.onDrop).not.toBeNull();
    const file = new File(["x"], "drop.png", { type: "image/png" });
    await act(async () => {
      dropHandlers.onDrop?.([file]);
      // Microtask: the mock editor awaits onUploadFile before mutating its value.
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(onUploadFile).toHaveBeenCalledWith(file);
  });




  it("does not render the file upload button when onUploadFile is omitted", () => {
    renderInput({ onUploadFile: undefined });
    // FileUploadButton renders an icon button labelled by its tooltip — when
    // upload wiring is absent the chat input falls back to "submit + extras"
    // only. Probe by counting buttons: with no upload, only the submit
    // button is in the action row.
    const buttons = screen.getAllByRole("button");
    // The agent picker may render zero buttons
    // in this test (no leftAdornment passed). So a single button = submit.
    expect(buttons.length).toBe(1);
  });
});
