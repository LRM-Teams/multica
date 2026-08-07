"use client";

/**
 * ContentEditor — the rich-text editor used wherever the user TYPES content.
 *
 * Architecture decisions (April 2026 refactor):
 *
 * 1. EDITING ONLY. Read-only display is handled by `ReadonlyContent` (a
 *    react-markdown renderer), not this component. There used to be an
 *    `editable` prop here that toggled between modes, but every readonly
 *    callsite migrated to ReadonlyContent and the prop only invited
 *    misuse — Tiptap's `useEditor` reads `editable` at mount, so toggling
 *    the prop later silently failed (mounted-as-readonly editors stayed
 *    unfocusable forever). To express "currently disabled", wrap this
 *    component in a layout that sets `pointer-events-none` / `aria-disabled`
 *    — don't reach into the editor.
 *
 * 2. ONE MARKDOWN PIPELINE via @tiptap/markdown. Content is loaded with
 *    `contentType: 'markdown'` and saved with `editor.getMarkdown()`.
 *    Previously we had a custom `markdownToHtml()` pipeline (Marked library)
 *    for loading and regex post-processing for saving — two asymmetric paths
 *    that caused roundtrip inconsistencies. The @tiptap/markdown extension
 *    (v3.21.0+) handles table cell <p> wrapping and custom mention tokenizers
 *    natively, eliminating the need for the HTML detour.
 *
 * 3. PREPROCESSING is minimal: only legacy mention shortcode migration and
 *    URL linkification (preprocessMarkdown). No HTML conversion.
 *
 * Tech: Tiptap v3.22.1 (ProseMirror wrapper), @tiptap/markdown for
 * bidirectional Markdown ↔ ProseMirror JSON conversion.
 */

import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
} from "react";
import { useEditor, EditorContent } from "@tiptap/react";
import { cn } from "@multica/ui/lib/utils";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceSlug } from "@multica/core/paths";
import { useQueryClient } from "@tanstack/react-query";
import type { Attachment } from "@multica/core/types";
import { Slice } from "@tiptap/pm/model";
import {
  parseMarkdownChunked,
  MARKDOWN_CHUNK_THRESHOLD,
  type MarkdownManagerLike,
} from "./utils/parse-markdown-chunked";
import type { MentionAgentCandidate, MentionItem } from "./extensions/mention-suggestion";
import { createEditorExtensions } from "./extensions";
import { uploadAndInsertFile } from "./extensions/file-upload";
import { preprocessMarkdown } from "./utils/preprocess";
import { openLink, isMentionHref } from "./utils/link-handler";
import { EditorBubbleMenu } from "./bubble-menu";
import { useLinkHover, LinkHoverCard } from "./link-hover-card";
import { AttachmentDownloadProvider } from "./attachment-download-context";
import "katex/dist/katex.min.css";
import "./styles/index.css";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Blob URLs (blob:http://…) are process-local and expire on reload. Strip them
 *  from serialised markdown so they never reach the database. */
const BLOB_IMAGE_RE = /!\[[^\]]*\]\(blob:[^)]*\)\n?/g;

function stripBlobUrls(md: string): string {
  return md.replace(BLOB_IMAGE_RE, "");
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ContentEditorProps {
  defaultValue?: string;
  onUpdate?: (markdown: string) => void;
  placeholder?: string;
  className?: string;
  debounceMs?: number;
  onSubmit?: () => void;
  onBlur?: () => void;
  onUploadFile?: (file: File) => Promise<UploadResult | null>;
  /** Show the floating formatting toolbar on text selection. Defaults true. */
  showBubbleMenu?: boolean;
  /** When true, bare Enter submits (chat-style). Mod-Enter always submits. */
  submitOnEnter?: boolean;
  /**
   * ID of the issue this editor belongs to. When set, the bubble menu exposes
   * a "Create sub-issue from selection" action that parents the new issue
   * under this ID and replaces the selection with a mention link.
   */
  currentIssueId?: string;
  /**
   * When true, the `@` suggestion picker is disabled but the mention node
   * type remains in the schema, so existing mentions pasted in from other
   * Multica editors still render as the normal pill. Use for editors where
   * *creating* a new mention has no business meaning (e.g. agent system
   * prompts) but *preserving* an existing one still matters.
   */
  disableMentions?: boolean;
  /** Chat can surface current/recent issue/project suggestions. Other editors use default mention behavior. */
  mentionMode?: "default" | "context";
  mentionContextItems?: MentionItem[];
  /** Enable a channel reference `#` inline picker. */
  enableChannelReferences?: boolean;
  /** Restrict the @ picker's member/agent candidates to these actor ids
   *  (e.g. a channel's members). Omit for the full workspace. */
  mentionAllowedActorIds?: ReadonlySet<string> | null;
  /** Channel-member agents to surface in the @ picker even when they aren't in
   *  the member's personal agent list (e.g. a teammate's private Wendy). Only
   *  used when `mentionAllowedActorIds` is active (channel scope). */
  scopedMentionAgents?: readonly MentionAgentCandidate[] | null;
  /**
   * #35: membership ids for IN THIS CHANNEL / NOT IN THIS CHANNEL section
   * headers. Does not filter the pool (that is `mentionAllowedActorIds`).
   */
  mentionChannelMemberIds?: ReadonlySet<string> | null;
  /** Enable the `/` command picker. Defaults false. */
  enableSlashCommands?: boolean;
  /**
   * Which `/` menu to show when enableSlashCommands is true: "skill" (default)
   * lists the active agent's skills (chat); "command" shows the fixed built-in
   * command menu (issue comments), e.g. /note; "block" shows Notion-style
   * content blocks such as code and Mermaid.
   */
  slashCommandMode?: "skill" | "command" | "block";
  /**
   * Attachments referenced by this content. The download buttons on file
   * cards and images inside the editor look up an attachment by `url` and
   * fetch a fresh CloudFront signature at click time, so a stale URL
   * persisted in markdown never opens. Pass `issue.attachments` /
   * `comment.attachments` etc.; omit when no attachment context is
   * available (NodeView buttons fall back to opening the raw URL).
   */
  attachments?: Attachment[];
  /**
   * How paste/drop/paperclip handle media files.
   * - `"inline"` (default): upload and insert image/fileCard into the doc
   *   (issue description, comments).
   * - `"external"`: do not insert into the editor; call `onExternalFiles`
   *   so the host (chat composer tray) owns the files.
   */
  mediaMode?: "inline" | "external";
  /** Called with deduped files when `mediaMode === "external"`. */
  onExternalFiles?: (files: File[]) => void;
  /**
   * When true, bare URLs stay PLAIN TEXT in this editable surface — they are
   * not auto-linkified on load or through the setContent round-trip. Used by
   * the chat composer (#531/#542) so a typed URL isn't turned into a link in
   * the input. The read/display side still renders bare URLs clickable via its
   * own preprocessLinks, so sent messages are unaffected. Defaults false, so
   * every other editor (issue/comment/description) keeps its current behavior.
   */
  plainUrls?: boolean;
}

interface ContentEditorRef {
  getMarkdown: () => string;
  clearContent: () => void;
  focus: () => void;
  /** Drop focus from the editor — used by chat after send so the caret
   *  stops competing with the StatusPill / streaming reply for the user's
   *  attention. */
  blur: () => void;
  uploadFile: (file: File) => void;
  /** True when file uploads are still in progress. */
  hasActiveUploads: () => boolean;
  /** Insert plain text at the current selection and focus the editor. */
  insertText: (text: string) => void;
  /** Insert an empty paragraph before the first body block and focus it. */
  insertBlankLineAtStart: () => void;
  /** Focus the editor and open the issue reference `#` picker. */
  openIssueReferences: () => void;
  /**
   * LRM-695 — append Markdown at the end of the document, parsing it through
   * the same `@tiptap/markdown` pipeline as paste so block syntax (e.g. a `>`
   * blockquote) becomes a real node instead of literal text. The caret lands at
   * the end; nothing is sent. Falls back to plain-text insertion when the
   * Markdown parser is unavailable (readonly/legacy mounts).
   */
  insertMarkdown: (md: string) => void;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

const ContentEditor = forwardRef<ContentEditorRef, ContentEditorProps>(
  function ContentEditor(
    {
      defaultValue = "",
      onUpdate,
      placeholder: placeholderText = "",
      className,
      debounceMs = 300,
      onSubmit,
      onBlur,
      onUploadFile,
      showBubbleMenu = true,
      submitOnEnter = false,
      currentIssueId,
      disableMentions = false,
      mentionMode = "default",
      mentionContextItems,
      enableChannelReferences = false,
      mentionAllowedActorIds,
      scopedMentionAgents,
      mentionChannelMemberIds,
      enableSlashCommands = false,
      slashCommandMode = "skill",
      attachments,
      mediaMode = "inline",
      onExternalFiles,
      plainUrls = false,
    },
    ref,
  ) {
    const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);
    const onUpdateRef = useRef(onUpdate);
    const onSubmitRef = useRef(onSubmit);
    const onBlurRef = useRef(onBlur);
    const onUploadFileRef = useRef<
      ((file: File) => Promise<UploadResult | null>) | undefined
    >(undefined);
    const mediaModeRef = useRef<"inline" | "external">(mediaMode);
    const onExternalFilesRef = useRef<((files: File[]) => void) | undefined>(
      onExternalFiles,
    );
    const mentionContextItemsRef = useRef<MentionItem[]>(mentionContextItems ?? []);
    const mentionAllowedActorIdsRef = useRef<ReadonlySet<string> | null>(mentionAllowedActorIds ?? null);
    const scopedMentionAgentsRef = useRef<readonly MentionAgentCandidate[] | null>(scopedMentionAgents ?? null);
    const mentionChannelMemberIdsRef = useRef<ReadonlySet<string> | null>(
      mentionChannelMemberIds ?? null,
    );
    const lastEmittedRef = useRef<string | null>(null);

    // In-session record of attachments freshly uploaded through this editor.
    // Surfaces (like the quick-create modal) that don't have a server-supplied
    // `attachments` prop still need the AttachmentDownloadProvider to know
    // about images the user just pasted/dropped — without a record in scope,
    // Attachment.normalize() can't swap the persisted /api/attachments/<id>/
    // download URL to a freshly-loadable one, and the <img> renders broken in
    // any environment where the renderer's origin doesn't proxy /api to the
    // API host (MUL-3192, Desktop/Electron).
    const [sessionUploads, setSessionUploads] = useState<Attachment[]>([]);
    // Wrap the caller-supplied uploader so we can stash each successful result
    // in `sessionUploads`. The wrapper is rebuilt only when the underlying
    // `onUploadFile` identity changes, so the inner ref handed to Tiptap stays
    // stable across renders the way the original passthrough did.
    const wrappedOnUploadFile = useMemo(() => {
      if (!onUploadFile) return undefined;
      return async (file: File): Promise<UploadResult | null> => {
        const result = await onUploadFile(file);
        // Only track attachments that carry a persisted id — the no-workspace
        // avatar branch returns an id-less record that the resolver can't key
        // off of, and tracking it would just bloat memory without helping
        // anyone. See useFileUpload's `markdownLink` docstring for why.
        if (result?.id) {
          setSessionUploads((prev) =>
            // Deduplicate on id so a re-upload (or a paste-then-drop of the
            // same blob) doesn't create a parallel record.
            prev.some((a) => a.id === result.id) ? prev : [...prev, result],
          );
        }
        return result;
      };
    }, [onUploadFile]);

    // Merged list fed to AttachmentDownloadProvider. Caller-supplied attachments
    // (issue / comment editors that pre-load the full attachments[] from the
    // server) take precedence — we only append session uploads the caller
    // doesn't already have, so a parent re-render that includes the same record
    // doesn't end up with two copies.
    const providerAttachments = useMemo(() => {
      if (sessionUploads.length === 0) return attachments;
      const seen = new Set<string>();
      const merged: Attachment[] = [];
      for (const a of attachments ?? []) {
        if (a.id) seen.add(a.id);
        merged.push(a);
      }
      for (const a of sessionUploads) {
        if (!seen.has(a.id)) {
          seen.add(a.id);
          merged.push(a);
        }
      }
      return merged;
    }, [attachments, sessionUploads]);

    // Current workspace slug kept in a ref so the click handler always sees the
    // latest value without recreating the editor. Used by openLink to prefix
    // legacy /issues/... style paths that lack a workspace slug.
    const workspaceSlug = useWorkspaceSlug();
    const workspaceSlugRef = useRef(workspaceSlug);
    workspaceSlugRef.current = workspaceSlug;

    // Keep refs in sync without recreating editor
    onUpdateRef.current = onUpdate;
    onSubmitRef.current = onSubmit;
    onBlurRef.current = onBlur;
    onUploadFileRef.current = wrappedOnUploadFile;
    mediaModeRef.current = mediaMode;
    onExternalFilesRef.current = onExternalFiles;
    mentionContextItemsRef.current = mentionContextItems ?? [];
    mentionAllowedActorIdsRef.current = mentionAllowedActorIds ?? null;
    scopedMentionAgentsRef.current = scopedMentionAgents ?? null;
    mentionChannelMemberIdsRef.current = mentionChannelMemberIds ?? null;

    const queryClient = useQueryClient();

    const initialContent = defaultValue
      ? preprocessMarkdown(defaultValue, { linkify: !plainUrls })
      : "";
    // Large markdown is parsed in chunks to dodge marked's O(n²) tokenizer (see
    // parseMarkdownChunked). Small docs stay on the single-parse fast path.
    const mountChunked = initialContent.length > MARKDOWN_CHUNK_THRESHOLD;

    const editor = useEditor({
      immediatelyRender: false,
      // Note: in v3.22.1 the default is already false/undefined (same behavior).
      // Explicit for clarity — the real perf win is useEditorState in BubbleMenu.
      shouldRerenderOnTransaction: false,
      onCreate: ({ editor: ed }) => {
        // For large docs we mount empty (below) and parse in chunks here, so the
        // O(n²) marked tokenizer never sees the whole document at once.
        if (mountChunked) {
          const manager = (
            ed.storage as { markdown?: { manager?: MarkdownManagerLike } }
          ).markdown?.manager;
          if (manager) {
            ed.commands.setContent(
              parseMarkdownChunked(manager, initialContent),
              { emitUpdate: false },
            );
          } else {
            ed.commands.setContent(initialContent, {
              emitUpdate: false,
              contentType: "markdown",
            });
          }
        }
        lastEmittedRef.current = stripBlobUrls(ed.getMarkdown()).trimEnd();
      },
      content: mountChunked ? "" : initialContent,
      contentType: mountChunked
        ? undefined
        : defaultValue
          ? "markdown"
          : undefined,
      extensions: createEditorExtensions({
        placeholder: placeholderText,
        queryClient,
        onSubmitRef,
        onUploadFileRef,
        mediaModeRef,
        onExternalFilesRef,
        submitOnEnter,
        disableMentions,
          mentionMode,
          getMentionContextItems: () => mentionContextItemsRef.current,
          getMentionAllowedActorIds: () => mentionAllowedActorIdsRef.current,
          getMentionScopedAgents: () => scopedMentionAgentsRef.current,
          getMentionChannelMemberIds: () => mentionChannelMemberIdsRef.current,
          enableChannelReferences,
          enableSlashCommands,
        slashCommandMode,
      }),
      onUpdate: ({ editor: ed }) => {
        if (!onUpdateRef.current) return;
        const emitUpdate = () => {
          const md = stripBlobUrls(ed.getMarkdown()).trimEnd();
          if (md === lastEmittedRef.current) return;
          lastEmittedRef.current = md;
          onUpdateRef.current?.(md);
        };
        if (debounceMs <= 0) {
          emitUpdate();
          return;
        }
        if (debounceRef.current) clearTimeout(debounceRef.current);
        debounceRef.current = setTimeout(() => {
          emitUpdate();
        }, debounceMs);
      },
      onBlur: () => {
        onBlurRef.current?.();
      },
      editorProps: {
        handleDOMEvents: {
          click(_view, event) {
            const target = event.target as HTMLElement;
            // Skip links inside NodeView wrappers — they handle their own clicks
            if (target.closest("[data-node-view-wrapper]")) return false;

            const link = target.closest("a");
            const href = link?.getAttribute("href");
            if (!href || isMentionHref(href)) return false;

            event.preventDefault();
            openLink(href, workspaceSlugRef.current);
            return true;
          },
        },
        attributes: {
          class: cn("flex-1 rich-text-editor text-sm outline-none", className),
        },
      },
    });

    // Cleanup debounce on unmount
    useEffect(() => {
      return () => {
        if (debounceRef.current) clearTimeout(debounceRef.current);
      };
    }, []);

    // Sync external `defaultValue` changes into the editor.
    // Tiptap v3 `useEditor` reads `content` only at mount (ueberdosis/tiptap#5831);
    // without this effect, a WS-driven description update keeps the editor
    // showing stale content until the issue is closed and reopened.
    useEffect(() => {
      if (!editor || editor.isDestroyed) return;

      const current = stripBlobUrls(editor.getMarkdown()).trimEnd();
      // "Dirty" = user has local edits not yet flushed through the debounced
      // `onUpdate`. `lastEmittedRef` is advanced only after a debounce fire,
      // so a divergence means the editor holds unsaved bytes.
      const isDirty =
        lastEmittedRef.current !== null && current !== lastEmittedRef.current;

      // Guard 1: focused AND dirty — protect bytes the user is actively
      // typing. Focused-but-clean falls through: applying setContent is safe
      // (no user input to lose) and necessary, because onBlur has no replay
      // mechanism and a focused clean editor would otherwise drop this sync
      // permanently.
      if (editor.isFocused && isDirty) return;

      // Guard 2: unfocused-but-dirty — blur happened but the debounce window
      // (debounceMs, 1500ms for description) hasn't flushed yet. The pending
      // onUpdate will reach the server and the cache will reconcile; skipping
      // here avoids overwriting unsaved local edits.
      if (isDirty) return;

      const incoming = defaultValue
        ? preprocessMarkdown(defaultValue, { linkify: !plainUrls })
        : "";
      const incomingNormalized = stripBlobUrls(incoming).trimEnd();
      // Guard 3: normalized-equal short-circuit. Avoids a no-op transaction
      // when the cache reflects a write this same editor just emitted.
      if (incomingNormalized === current) return;

      // Guard 4: `emitUpdate: false`. Tiptap v3's setContent defaults to
      // `emitUpdate: true`; without this we would re-trigger onUpdate →
      // server save → self-write loop.
      const { from, to } = editor.state.selection;
      // Same chunked path on WS-driven re-parse of a large description.
      const manager =
        incoming.length > MARKDOWN_CHUNK_THRESHOLD
          ? (editor.storage as { markdown?: { manager?: MarkdownManagerLike } })
              .markdown?.manager
          : undefined;
      if (manager) {
        editor.commands.setContent(parseMarkdownChunked(manager, incoming), {
          emitUpdate: false,
        });
      } else {
        editor.commands.setContent(incoming, {
          emitUpdate: false,
          contentType: "markdown",
        });
      }

      // Clamp prior selection to the new doc size so the caret doesn't snap
      // to position 0 after ProseMirror replaces the document.
      const docSize = editor.state.doc.content.size;
      editor.commands.setTextSelection({
        from: Math.min(from, docSize),
        to: Math.min(to, docSize),
      });

      lastEmittedRef.current = stripBlobUrls(editor.getMarkdown()).trimEnd();
    }, [defaultValue, editor, plainUrls]);

    useImperativeHandle(ref, () => ({
      getMarkdown: () => stripBlobUrls(editor?.getMarkdown() ?? ""),
      clearContent: () => {
        editor?.commands.clearContent();
      },
      focus: () => {
        editor?.commands.focus();
      },
      blur: () => {
        editor?.commands.blur();
      },
      uploadFile: (file: File) => {
        if (!editor) return;
        // Chat tray: paperclip / external callers hand files off without
        // inserting image/fileCard into the Tiptap document.
        if (mediaModeRef.current === "external") {
          onExternalFilesRef.current?.([file]);
          return;
        }
        if (!onUploadFileRef.current) return;
        const endPos = editor.state.doc.content.size;
        uploadAndInsertFile(editor, file, onUploadFileRef.current, endPos);
      },
      hasActiveUploads: () => {
        if (!editor) return false;
        let uploading = false;
        editor.state.doc.descendants((node) => {
          if (node.attrs.uploading) uploading = true;
          return !uploading;
        });
        return uploading;
      },
      insertText: (text: string) => {
        editor?.chain().focus().insertContent(text).run();
      },
      insertBlankLineAtStart: () => {
        if (!editor) return;
        editor
          .chain()
          .focus("start")
          .insertContentAt(0, { type: "paragraph" })
          .setTextSelection(1)
          .run();
      },
      openIssueReferences: () => {
        if (!editor) return;
        editor.chain().focus().insertContent("#").run();
      },
      insertMarkdown: (md: string) => {
        if (!editor) return;
        // No Markdown parser (readonly/legacy) → insert as plain text at end.
        if (!editor.markdown) {
          editor.chain().focus("end").insertContent(md).run();
          return;
        }
        const json = editor.markdown.parse(md);
        const node = editor.schema.nodeFromJSON(json);
        // maxOpen lets ProseMirror stitch the block content in at the caret;
        // mirrors the proven markdown-paste path (extensions/markdown-paste.ts).
        const slice = Slice.maxOpen(node.content);
        editor
          .chain()
          .focus("end")
          .command(({ tr }) => {
            tr.replaceSelection(slice);
            return true;
          })
          .focus("end")
          .run();
      },
    }));

    // Link hover card — disabled when BubbleMenu is active (has selection)
    const wrapperRef = useRef<HTMLDivElement>(null);
    const hoverDisabled = !editor?.state.selection.empty;
    const hover = useLinkHover(wrapperRef, hoverDisabled);

    const handleContainerMouseDown = (event: ReactMouseEvent<HTMLDivElement>) => {
      if (!editor) return;

      const target = event.target as HTMLElement;
      if (target.closest(".ProseMirror")) return;
      if (target.closest("a, button, input, textarea, [role='button'], [data-node-view-wrapper]")) return;

      event.preventDefault();
      editor.commands.focus("end");
    };

    if (!editor) return null;

    return (
      <AttachmentDownloadProvider attachments={providerAttachments}>
        <div
          ref={wrapperRef}
          className="relative flex flex-1 min-h-full flex-col"
          onMouseDown={handleContainerMouseDown}
        >
          <EditorContent className="flex flex-1 flex-col" editor={editor} />
          {showBubbleMenu && (
            <EditorBubbleMenu editor={editor} currentIssueId={currentIssueId} />
          )}
          <LinkHoverCard {...hover} />
        </div>
      </AttachmentDownloadProvider>
    );
  },
);

export { ContentEditor, type ContentEditorProps, type ContentEditorRef };
