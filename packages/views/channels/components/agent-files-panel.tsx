"use client";

import { type ReactNode, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import CodeMirror from "@uiw/react-codemirror";
import { css } from "@codemirror/lang-css";
import { go } from "@codemirror/lang-go";
import { html } from "@codemirror/lang-html";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { yaml } from "@codemirror/lang-yaml";
import { Eye, EyeOff, Save, X } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useAgentPresence } from "@multica/core/agents";
import type { Agent, AgentFileContentResponse, MemberWithUser } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { formatPresenceStatus } from "../../agents/presence";
import { FileTree } from "./file-tree";
import { buildFileTree, fileLanguage, type FileTreeNode } from "./file-tree-utils";

const agentFilesQueryKey = (agentId: string, includeHidden: boolean) =>
  ["agent-files", agentId, includeHidden] as const;
const agentFileContentQueryKey = (agentId: string, path: string | null) =>
  ["agent-file-content", agentId, path ?? ""] as const;

const FILE_LOAD_FAILED = "Failed to load file.";
const FILE_TOO_LARGE = "This file is too large to edit.";
const BINARY_FILE_READONLY = "Binary files cannot be edited.";
const MEDIA_FILE_READONLY = "Media files are read-only in this editor.";
const SAVE_FILE_LABEL = "Save";
const OWNER_ONLY_FILES_MESSAGE =
  "Only the creator can view and edit this agent's configuration files.";
const FILES_LABEL = "Files";
const NO_FILES_FOUND = "No files found.";
const FILE_LIST_TRUNCATED = "File list truncated.";
function ownerName(agent: Agent, members: readonly MemberWithUser[]): string {
  if (!agent.owner_id) return "Unknown";
  const member = members.find((m) => m.user_id === agent.owner_id);
  return member?.display_name || member?.name || member?.email || agent.owner_id;
}

function formatDate(value: string): string {
  if (!value) return "Unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function CenteredNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-32 items-center justify-center p-4 text-center text-xs text-muted-foreground">
      {children}
    </div>
  );
}

function directoryPaths(nodes: readonly FileTreeNode[]): string[] {
  return nodes.flatMap((node) =>
    node.isDir ? [node.path, ...directoryPaths(node.children)] : [],
  );
}

function languageExtensions(name: string) {
  const lang = fileLanguage(name);
  switch (lang) {
    case "ts":
    case "tsx":
      return [javascript({ typescript: true, jsx: lang === "tsx" })];
    case "js":
    case "jsx":
    case "mjs":
    case "cjs":
      return [javascript({ jsx: lang === "jsx" })];
    case "json":
      return [json()];
    case "md":
    case "mdx":
      return [markdown()];
    case "go":
      return [go()];
    case "yaml":
    case "yml":
      return [yaml()];
    case "html":
      return [html()];
    case "css":
    case "scss":
    case "less":
      return [css()];
    default:
      return [];
  }
}

function prettyInitialContent(path: string, data: AgentFileContentResponse | undefined): string {
  const content = data?.content ?? "";
  if (fileLanguage(path) !== "json" || !content.trim()) return content;
  try {
    return JSON.stringify(JSON.parse(content), null, 2);
  } catch {
    return content;
  }
}

function AgentFileEditorDialog({
  agentId,
  path,
  canEdit,
  onClose,
}: {
  agentId: string;
  path: string | null;
  canEdit: boolean;
  onClose: () => void;
}) {
  const { data, isPending, isError } = useQuery({
    queryKey: agentFileContentQueryKey(agentId, path),
    queryFn: () => api.getAgentFileContent(agentId, path ?? ""),
    enabled: !!path,
  });
  const name = path ? path.slice(path.lastIndexOf("/") + 1) : "";

  // DialogContent defaults to showCloseButton — that absolute ✕ stacks on top
  // of the header's Close editor control (LRM-453 dual-X). Keep one close on
  // the preview card; Esc / backdrop dismiss still flow through onOpenChange.
  return (
    <Dialog open={!!path} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        showCloseButton={false}
        className="flex h-[85vh] w-[92vw] max-w-[1200px] sm:max-w-[1200px] flex-col gap-0 overflow-hidden p-0"
      >
        <DialogHeader className="flex-row items-center justify-between gap-3 border-b px-4 py-3">
          <DialogTitle className="truncate font-mono text-sm">{name}</DialogTitle>
          <Button type="button" variant="ghost" size="icon" onClick={onClose} aria-label="Close editor">
            <X className="size-4" aria-hidden />
          </Button>
        </DialogHeader>
        {isPending ? (
          <div className="space-y-2 p-4">
            <Skeleton className="h-4" />
            <Skeleton className="h-4" />
            <Skeleton className="h-4 w-2/3" />
          </div>
        ) : isError ? (
          <CenteredNote>{FILE_LOAD_FAILED}</CenteredNote>
        ) : data?.too_large ? (
          <CenteredNote>{FILE_TOO_LARGE}</CenteredNote>
        ) : data?.binary ? (
          <CenteredNote>{BINARY_FILE_READONLY}</CenteredNote>
        ) : data?.encoding === "base64" ? (
          <CenteredNote>{MEDIA_FILE_READONLY}</CenteredNote>
        ) : path && data ? (
          <AgentFileEditorForm
            key={`${path}:${data.content_hash}`}
            agentId={agentId}
            path={path}
            data={data}
            canEdit={canEdit}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function AgentFileEditorForm({
  agentId,
  path,
  data,
  canEdit,
}: {
  agentId: string;
  path: string;
  data: AgentFileContentResponse;
  canEdit: boolean;
}) {
  const qc = useQueryClient();
  const name = path.slice(path.lastIndexOf("/") + 1);
  const initialContent = prettyInitialContent(path, data);
  const [draft, setDraft] = useState(initialContent);
  const [jsonError, setJsonError] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: async () => {
      if (fileLanguage(path) === "json" && draft.trim()) {
        try {
          JSON.parse(draft);
          setJsonError(null);
        } catch (err) {
          const message = err instanceof Error ? err.message : "Invalid JSON";
          setJsonError(message);
          throw new Error(message);
        }
      }
      return api.updateAgentFileContent(agentId, {
        path,
        content: draft,
        expected_content_hash: data.content_hash,
      });
    },
    onSuccess: async (resp) => {
      if (resp.conflict) {
        showErrorToast("File changed on disk. Reload before saving again.");
        return;
      }
      toast.success("File saved");
      await Promise.all([
        qc.invalidateQueries({ queryKey: agentFilesQueryKey(agentId, false) }),
        qc.invalidateQueries({ queryKey: agentFilesQueryKey(agentId, true) }),
        qc.invalidateQueries({ queryKey: agentFileContentQueryKey(agentId, path) }),
      ]);
    },
    onError: (err) => {
      if (err instanceof Error && err.message) {
        showErrorToast(err.message);
      } else {
        showErrorToast("Failed to save file");
      }
    },
  });

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      {canEdit && (
        <div className="flex items-center justify-end border-b px-4 py-2">
          <Button
            type="button"
            size="sm"
            onClick={() => save.mutate()}
            disabled={save.isPending || draft === initialContent}
          >
            <Save className="mr-1.5 size-3.5" aria-hidden />
            {SAVE_FILE_LABEL}
          </Button>
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-hidden">
        <CodeMirror
          value={draft}
          height="100%"
          editable={canEdit}
          readOnly={!canEdit}
          basicSetup={{
            foldGutter: true,
            highlightActiveLine: true,
            lineNumbers: true,
          }}
          extensions={languageExtensions(name)}
          onChange={setDraft}
          className="h-full text-sm"
        />
        {jsonError && <p className="border-t px-4 py-2 text-xs text-destructive">{jsonError}</p>}
      </div>
    </div>
  );
}

export function AgentFilesPanel({
  agent,
  currentUserId,
  members,
  canReadFiles,
  canEditFiles,
  onClose,
  hideHeader = false,
}: {
  agent: Agent;
  currentUserId: string | null;
  members: readonly MemberWithUser[];
  canReadFiles?: boolean;
  canEditFiles?: boolean;
  onClose: () => void;
  /**
   * Skips the identity/info header and the outer `<aside>` chrome — used
   * when this panel is embedded as a tab inside AgentSidePanel, which
   * already renders its own header shared across tabs.
   */
  hideHeader?: boolean;
}) {
  const { t } = useT("agents");
  const isOwner = !!currentUserId && agent.owner_id === currentUserId;
  const canRead = canReadFiles ?? isOwner;
  const canEdit = canEditFiles ?? isOwner;
  // Reuse the shared #288 presence token → localized word so the Status row
  // agrees with the agent's presence dot everywhere else, instead of leaking
  // the raw `agent.status` enum ("idle"/"offline"/…) as hardcoded English.
  const presence = useAgentPresence(agent.workspace_id, agent.id);
  const statusLabel = formatPresenceStatus(presence, t) ?? "—";
  const [includeHidden, setIncludeHidden] = useState(false);
  // Start with a quiet Files tab: directories reveal their contents only when
  // the reader asks. Tracking the exceptional *expanded* paths also keeps
  // directories discovered by a later refetch collapsed by default.
  const [expandedDirectories, setExpandedDirectories] = useState<Set<string>>(new Set());
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const { data, isPending } = useQuery({
    queryKey: agentFilesQueryKey(agent.id, includeHidden),
    queryFn: () => api.listAgentFiles(agent.id, { include_hidden: includeHidden }),
    enabled: canRead,
  });
  const tree = useMemo(() => buildFileTree(data?.nodes ?? []), [data?.nodes]);
  const collapsed = useMemo(
    () => new Set(directoryPaths(tree).filter((path) => !expandedDirectories.has(path))),
    [tree, expandedDirectories],
  );
  const toggle = (path: string) =>
    setExpandedDirectories((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  const status = data?.status ?? "error";

  const body = (
    <>
      {!hideHeader && (
        <div className="space-y-2 border-b p-4 text-xs">
          <InfoRow label={t(($) => $.files_panel.info_id)} value={agent.id} mono />
          <InfoRow label={t(($) => $.files_panel.info_created)} value={formatDate(agent.created_at)} />
          <InfoRow label={t(($) => $.files_panel.info_creator)} value={ownerName(agent, members)} />
          <InfoRow label={t(($) => $.files_panel.info_status)} value={statusLabel} />
        </div>
      )}
      {!canRead ? (
        <CenteredNote>{OWNER_ONLY_FILES_MESSAGE}</CenteredNote>
      ) : (
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <div className="flex items-center justify-between border-b px-3 py-2">
            <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {FILES_LABEL}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={includeHidden ? "Hide hidden files" : "Show hidden files"}
              onClick={() => setIncludeHidden((v) => !v)}
              className={cn("size-7", includeHidden && "text-primary")}
            >
              {includeHidden ? <Eye className="size-4" aria-hidden /> : <EyeOff className="size-4" aria-hidden />}
            </Button>
          </div>
          <div className="min-h-0 min-w-0 flex-1 overflow-auto p-2">
            {isPending ? (
              <div className="space-y-1.5">
                <Skeleton className="h-5" />
                <Skeleton className="h-5" />
                <Skeleton className="h-5" />
              </div>
            ) : status !== "ok" ? (
              <CenteredNote>
                {status === "offline"
                  ? "Runtime is offline. Connect the agent runtime to browse files."
                  : status === "missing"
                    ? "Agent files have not been created yet."
                    : "Failed to load agent files."}
              </CenteredNote>
            ) : tree.length === 0 ? (
              <CenteredNote>{NO_FILES_FOUND}</CenteredNote>
            ) : (
              <>
                <FileTree tree={tree} collapsed={collapsed} onToggle={toggle} onOpenFile={setSelectedPath} />
                {data?.truncated && (
                  <p className="mt-1 px-2 py-1 text-[11px] text-muted-foreground">
                    {FILE_LIST_TRUNCATED}
                  </p>
                )}
              </>
            )}
          </div>
        </div>
      )}
      {canRead && (
        <AgentFileEditorDialog
          agentId={agent.id}
          path={selectedPath}
          canEdit={canEdit}
          onClose={() => setSelectedPath(null)}
        />
      )}
    </>
  );

  if (hideHeader) {
    return <div className="flex h-full min-h-0 min-w-0 flex-col">{body}</div>;
  }

  return (
    <aside className="flex h-full min-h-0 min-w-0 flex-col border-l bg-background">
      <div className="flex items-start justify-between gap-3 border-b p-4">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">{agent.display_name || agent.name}</p>
          <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{agent.description || agent.name}</p>
        </div>
        <Button type="button" variant="ghost" size="icon" onClick={onClose} aria-label="Close agent panel">
          <X className="size-4" aria-hidden />
        </Button>
      </div>
      {body}
    </aside>
  );
}

function InfoRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[72px_minmax(0,1fr)] gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className={cn("truncate text-foreground", mono && "font-mono")}>{value}</span>
    </div>
  );
}
