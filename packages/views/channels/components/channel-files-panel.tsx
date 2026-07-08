"use client";

import { type ReactNode, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { channelProjectFilesOptions } from "@multica/core/channels";
import { api } from "@multica/core/api";
import { CodeBlock } from "@multica/ui/markdown";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { buildFileTree, FileTree, fileLanguage } from "./file-tree";

// Modal preview of one file's content. Fetches lazily when `path` is set.
function CenteredNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center p-6 text-center text-xs text-muted-foreground">
      {children}
    </div>
  );
}

function FilePreviewDialog({
  channelId,
  path,
  onClose,
}: {
  channelId: string;
  path: string | null;
  onClose: () => void;
}) {
  const { t } = useT("channels");
  const { data, isPending, isError } = useQuery({
    queryKey: ["channel-project-file", channelId, path],
    queryFn: () => api.getChannelProjectFile(channelId, path ?? ""),
    enabled: !!path,
  });
  const name = path ? path.slice(path.lastIndexOf("/") + 1) : "";
  const lang = fileLanguage(name);

  // Media files arrive base64-encoded; decode into a blob object URL so
  // <img>/<video>/<audio> can render them without an authenticated <img src>.
  const blobUrl = useMemo(() => {
    if (data?.encoding !== "base64" || !data.content) return null;
    try {
      const bytes = Uint8Array.from(atob(data.content), (c) => c.charCodeAt(0));
      return URL.createObjectURL(new Blob([bytes], { type: data.mime_type || "application/octet-stream" }));
    } catch {
      return null;
    }
  }, [data?.encoding, data?.content, data?.mime_type]);
  useEffect(() => () => {
    if (blobUrl) URL.revokeObjectURL(blobUrl);
  }, [blobUrl]);

  const mime = data?.mime_type ?? "";
  const renderBody = () => {
    if (isPending) {
      return (
        <div className="space-y-1.5 p-4">
          <Skeleton className="h-4" />
          <Skeleton className="h-4" />
          <Skeleton className="h-4 w-2/3" />
        </div>
      );
    }
    if (isError) return <CenteredNote>{t(($) => $.files.preview_error)}</CenteredNote>;
    if (data?.too_large) return <CenteredNote>{t(($) => $.files.preview_too_large)}</CenteredNote>;
    if (blobUrl && mime.startsWith("image/")) {
      return (
        <div className="flex h-full items-center justify-center p-4">
          <img src={blobUrl} alt={name} className="max-h-full max-w-full object-contain" />
        </div>
      );
    }
    if (blobUrl && mime.startsWith("video/")) {
      return (
        <div className="flex h-full items-center justify-center p-4">
          <video src={blobUrl} controls className="max-h-full max-w-full" />
        </div>
      );
    }
    if (blobUrl && mime.startsWith("audio/")) {
      return (
        <div className="flex h-full items-center justify-center p-4">
          <audio src={blobUrl} controls className="w-full max-w-lg" />
        </div>
      );
    }
    if (blobUrl && mime === "application/pdf") {
      return <iframe src={blobUrl} title={name} className="h-full w-full border-0" />;
    }
    if (data?.binary) return <CenteredNote>{t(($) => $.files.preview_binary)}</CenteredNote>;
    if (!data?.content) return <CenteredNote>{t(($) => $.files.preview_empty)}</CenteredNote>;
    // Text → syntax-highlighted code (shiki), like a VSCode file view.
    return (
      <div className="p-3">
        <CodeBlock code={data.content} language={lang} mode="full" className="text-xs" />
        {data.truncated && (
          <p className="mt-2 text-[11px] text-muted-foreground">{t(($) => $.files.preview_truncated)}</p>
        )}
      </div>
    );
  };

  return (
    <Dialog open={!!path} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[85vh] w-[92vw] max-w-[1400px] sm:max-w-[1400px] flex-col gap-0 overflow-hidden p-0">
        <DialogHeader className="shrink-0 border-b px-4 py-3">
          <DialogTitle className="truncate font-mono text-sm">{name}</DialogTitle>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-auto">{path ? renderBody() : null}</div>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Shows the channel's bound project working directory as a collapsible,
 * VSCode-style file tree (directories first, file-type icons/colors). The
 * files live on a daemon machine, so the data comes from a server→daemon RPC;
 * the panel renders the per-status empty states the endpoint reports.
 */
export function ChannelFilesPanel({ channelId }: { channelId: string }) {
  const { t } = useT("channels");
  const { data, isPending } = useQuery(channelProjectFilesOptions(channelId));
  const tree = useMemo(() => buildFileTree(data?.nodes ?? []), [data?.nodes]);
  // Track collapsed (not expanded) dirs so the default is fully expanded and a
  // user's collapses survive refetches (paths are stable).
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [selected, setSelected] = useState<string | null>(null);
  const toggle = (path: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  if (isPending) {
    return (
      <div className="space-y-1.5">
        <Skeleton className="h-5" />
        <Skeleton className="h-5" />
        <Skeleton className="h-5" />
      </div>
    );
  }

  const status = data?.status ?? "error";
  if (status !== "ok") {
    const msg =
      status === "no_project"
        ? t(($) => $.files.no_project)
        : status === "offline"
          ? t(($) => $.files.offline)
          : status === "missing"
            ? t(($) => $.files.missing)
            : status === "github_unlinked"
              ? t(($) => $.files.github_unlinked)
              : t(($) => $.files.error);
    return <p className="py-6 text-center text-xs text-muted-foreground">{msg}</p>;
  }

  if (tree.length === 0) {
    return <p className="py-6 text-center text-xs text-muted-foreground">{t(($) => $.files.empty)}</p>;
  }

  return (
    <>
      <div className="max-h-80 overflow-auto">
        <FileTree tree={tree} collapsed={collapsed} onToggle={toggle} onOpenFile={setSelected} />
        {data?.truncated && (
          <p className="mt-1 px-2 py-1 text-[11px] text-muted-foreground">{t(($) => $.files.truncated)}</p>
        )}
      </div>
      <FilePreviewDialog channelId={channelId} path={selected} onClose={() => setSelected(null)} />
    </>
  );
}
