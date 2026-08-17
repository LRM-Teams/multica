"use client";

/**
 * LRM-1264 R2 — keep TipTap / ProseMirror / lowlight / editor math out of the
 * channel-shell eager graph.
 *
 * 1) Module is `React.lazy` — shell chunk does not evaluate TipTap until the
 *    composer is armed.
 * 2) Armed on first focus/pointer (or immediately when `defaultValue` is
 *    non-empty so drafts still open editable). Resting channel view therefore
 *    does not pay TipTap heap until the user actually composes.
 */

import {
  useCallback,
  forwardRef,
  lazy,
  Suspense,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type ComponentProps,
} from "react";
import { cn } from "@multica/ui/lib/utils";
import type { ContentEditorRef } from "./content-editor";

const LoadedContentEditor = lazy(() =>
  import("./content-editor").then((m) => ({ default: m.ContentEditor })),
);

export type { ContentEditorProps, ContentEditorRef } from "./content-editor";

function ComposerFallback({ className }: { className?: string }) {
  return (
    <div
      className={className}
      aria-busy="true"
      data-testid="content-editor-loading"
      style={{ minHeight: 40 }}
    />
  );
}

function ComposerShell({
  className,
  placeholder,
  onArm,
}: {
  className?: string;
  placeholder?: string;
  onArm: () => void;
}) {
  return (
    <div
      role="textbox"
      tabIndex={0}
      aria-label={placeholder}
      className={cn(
        "w-full cursor-text rounded-md px-1 py-1 text-sm text-muted-foreground outline-none",
        className,
      )}
      style={{ minHeight: 40 }}
      data-testid="content-editor-deferred"
      onFocus={onArm}
      onPointerDown={onArm}
    >
      {placeholder}
    </div>
  );
}

export const ContentEditor = forwardRef<
  ContentEditorRef,
  ComponentProps<typeof LoadedContentEditor>
>(function LazyContentEditor(props, ref) {
  const seeded = Boolean(props.defaultValue && String(props.defaultValue).trim());
  const [armed, setArmed] = useState(seeded);
  const loadedRef = useRef<ContentEditorRef | null>(null);

  const setLoadedRef = useCallback((editor: ContentEditorRef | null) => {
    loadedRef.current = editor;
  }, []);

  useImperativeHandle(ref, () => ({
    getMarkdown: () => loadedRef.current?.getMarkdown() ?? props.defaultValue ?? "",
    clearContent: () => loadedRef.current?.clearContent(),
    focus: () => {
      if (loadedRef.current) loadedRef.current.focus();
      else setArmed(true);
    },
    blur: () => loadedRef.current?.blur(),
    uploadFile: (file) => loadedRef.current?.uploadFile(file),
    hasActiveUploads: () => loadedRef.current?.hasActiveUploads() ?? false,
    insertText: (text) => loadedRef.current?.insertText(text),
    insertBlankLineAtStart: () => loadedRef.current?.insertBlankLineAtStart(),
    openIssueReferences: () => loadedRef.current?.openIssueReferences(),
    getSelectedText: () => loadedRef.current?.getSelectedText() ?? "",
    insertIssueReference: (attrs) => loadedRef.current?.insertIssueReference(attrs),
    insertRunReference: (attrs) => loadedRef.current?.insertRunReference(attrs),
    openPageAI: () => loadedRef.current?.openPageAI() ?? false,
  }), [props.defaultValue]);

  useEffect(() => {
    if (seeded) setArmed(true);
  }, [seeded]);

  if (!armed) {
    return (
      <ComposerShell
        className={props.className}
        placeholder={props.placeholder}
        onArm={() => setArmed(true)}
      />
    );
  }

  return (
    <Suspense fallback={<ComposerFallback className={props.className} />}>
      <LoadedContentEditor {...props} ref={setLoadedRef} />
    </Suspense>
  );
});
