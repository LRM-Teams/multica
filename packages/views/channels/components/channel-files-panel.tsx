"use client";

import { type ReactNode, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { channelProjectFilesOptions } from "@multica/core/channels";
import { api } from "@multica/core/api";
import { CodeBlock } from "@multica/ui/markdown";
import type { ChannelProjectFile } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import {
  ChevronRight,
  File,
  FileCode,
  FileImage,
  FileJson,
  FileText,
  Folder,
} from "lucide-react";
import { useT } from "../../i18n";

interface TreeNode {
  name: string;
  path: string;
  isDir: boolean;
  children: TreeNode[];
}

// Rebuild a nested tree from the flat, slash-separated node list. Intermediate
// directories are synthesized if the backend didn't list them explicitly.
function buildTree(nodes: ChannelProjectFile[]): TreeNode[] {
  const root: TreeNode = { name: "", path: "", isDir: true, children: [] };
  const byPath = new Map<string, TreeNode>([["", root]]);

  for (const n of nodes) {
    const parts = n.path.split("/").filter(Boolean);
    let parent = root;
    let acc = "";
    for (let i = 0; i < parts.length; i++) {
      const part = parts[i]!;
      acc = acc ? `${acc}/${part}` : part;
      let node = byPath.get(acc);
      if (!node) {
        const isLeaf = i === parts.length - 1;
        node = { name: part, path: acc, isDir: isLeaf ? n.is_dir : true, children: [] };
        byPath.set(acc, node);
        parent.children.push(node);
      }
      parent = node;
    }
  }

  const sortRec = (t: TreeNode) => {
    t.children.sort((a, b) =>
      a.isDir !== b.isDir ? (a.isDir ? -1 : 1) : a.name.localeCompare(b.name),
    );
    t.children.forEach(sortRec);
  };
  sortRec(root);
  return root.children;
}

// Per-extension icon + color so file kinds are scannable, VSCode-style.
function fileMeta(name: string): { Icon: typeof File; className: string } {
  const ext = name.includes(".") ? name.slice(name.lastIndexOf(".") + 1).toLowerCase() : "";
  switch (ext) {
    case "ts":
    case "tsx":
    case "js":
    case "jsx":
    case "mjs":
    case "cjs":
      return { Icon: FileCode, className: "text-sky-500" };
    case "go":
      return { Icon: FileCode, className: "text-cyan-500" };
    case "py":
      return { Icon: FileCode, className: "text-amber-500" };
    case "rs":
    case "java":
    case "c":
    case "cpp":
    case "rb":
    case "php":
    case "sh":
      return { Icon: FileCode, className: "text-orange-500" };
    case "html":
    case "vue":
    case "svelte":
      return { Icon: FileCode, className: "text-orange-600" };
    case "css":
    case "scss":
    case "less":
      return { Icon: FileCode, className: "text-blue-500" };
    case "json":
    case "yaml":
    case "yml":
    case "toml":
      return { Icon: FileJson, className: "text-amber-600" };
    case "md":
    case "mdx":
    case "txt":
      return { Icon: FileText, className: "text-muted-foreground" };
    case "png":
    case "jpg":
    case "jpeg":
    case "gif":
    case "svg":
    case "webp":
    case "ico":
      return { Icon: FileImage, className: "text-purple-500" };
    default:
      return { Icon: File, className: "text-muted-foreground" };
  }
}

const INDENT = 12;

function TreeRow({
  node,
  depth,
  isCollapsed,
  toggle,
  onOpenFile,
}: {
  node: TreeNode;
  depth: number;
  isCollapsed: (path: string) => boolean;
  toggle: (path: string) => void;
  onOpenFile: (path: string) => void;
}) {
  if (node.isDir) {
    const collapsed = isCollapsed(node.path);
    return (
      <>
        <button
          type="button"
          onClick={() => toggle(node.path)}
          style={{ paddingLeft: depth * INDENT + 6 }}
          className="flex w-full items-center gap-1 rounded py-1 pr-2 text-left text-xs hover:bg-accent"
        >
          <ChevronRight
            className={cn(
              "size-3.5 shrink-0 text-muted-foreground transition-transform",
              !collapsed && "rotate-90",
            )}
          />
          <Folder className="size-4 shrink-0 text-sky-500" />
          <span className="truncate text-foreground">{node.name}</span>
        </button>
        {!collapsed &&
          node.children.map((c) => (
            <TreeRow
              key={c.path}
              node={c}
              depth={depth + 1}
              isCollapsed={isCollapsed}
              toggle={toggle}
              onOpenFile={onOpenFile}
            />
          ))}
      </>
    );
  }
  const { Icon, className } = fileMeta(node.name);
  return (
    <button
      type="button"
      onClick={() => onOpenFile(node.path)}
      style={{ paddingLeft: depth * INDENT + 6 + 18 }}
      className="flex w-full items-center gap-1.5 rounded py-1 pr-2 text-left text-xs hover:bg-accent"
    >
      <Icon className={cn("size-4 shrink-0", className)} />
      <span className="truncate text-foreground">{node.name}</span>
    </button>
  );
}

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
  const lang = name.includes(".") ? name.slice(name.lastIndexOf(".") + 1).toLowerCase() : "text";

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
  const tree = useMemo(() => buildTree(data?.nodes ?? []), [data?.nodes]);
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
        {tree.map((node) => (
          <TreeRow
            key={node.path}
            node={node}
            depth={0}
            isCollapsed={(p) => collapsed.has(p)}
            toggle={toggle}
            onOpenFile={setSelected}
          />
        ))}
        {data?.truncated && (
          <p className="mt-1 px-2 py-1 text-[11px] text-muted-foreground">{t(($) => $.files.truncated)}</p>
        )}
      </div>
      <FilePreviewDialog channelId={channelId} path={selected} onClose={() => setSelected(null)} />
    </>
  );
}
