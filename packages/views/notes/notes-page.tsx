"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bot, Check, ChevronDown, ChevronRight, Copy, Download, FileText, Lock, MoreHorizontal, Plus, Share2, Sparkles, Trash2, Undo2, Users } from "lucide-react";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { resolveActorDisplayName } from "@multica/core/identity";
import { useWorkspaceId } from "@multica/core/hooks";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { useWorkspacePaths } from "@multica/core/paths";
import { noteDetailOptions, noteListOptions, noteTrashOptions } from "@multica/core/notes/queries";
import { useCreateNotePage, useDeleteNotePage, useDuplicateNotePage, useMoveNotePage, usePermanentlyDeleteNotePage, useRestoreNotePage, useUpdateNotePage, useUpdateNotePageShares } from "@multica/core/notes/mutations";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import type { Agent, MemberWithUser, NotePage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@multica/ui/components/ui/dropdown-menu";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@multica/ui/components/ui/hover-card";
import { Input } from "@multica/ui/components/ui/input";
import { Separator } from "@multica/ui/components/ui/separator";
import { cn } from "@multica/ui/lib/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { ContentEditor, type ContentEditorRef, type TextOptimizationRequest } from "../editor";
import { useNavigation } from "../navigation";
import { PageHeader } from "../layout/page-header";
import { useT } from "../i18n/use-t";

type NoteTreeNode = NotePage & { children: NoteTreeNode[] };
type NoteDropPosition = "before" | "after" | "inside";
type NoteDropTarget = { id: string; position: NoteDropPosition };

type NoteExpansionOverrides = { selectionId: string | null; expanded: Set<string>; collapsed: Set<string> };
type NoteDragState = { draggingId: string | null; dropTarget: NoteDropTarget | null };

type NoteExportFormat = "html" | "pdf";

type NotesPageUiState = {
  sharePage: NotePage | null;
  exportOpen: boolean;
  aiAgentOpen: boolean;
  showTrash: boolean;
};

type NoteAiAgentConfig = { workspaceId: string | null; agentId: string | null };

function lastViewedNoteKey(workspaceId: string) {
  return `multica:last-viewed-note:${workspaceId}`;
}

function readLastViewedNote(workspaceId: string) {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(lastViewedNoteKey(workspaceId));
}

function writeLastViewedNote(workspaceId: string, pageId: string) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(lastViewedNoteKey(workspaceId), pageId);
}

function noteAiAgentKey(workspaceId: string) {
  return `multica:note-ai-agent:${workspaceId}`;
}

function readNoteAiAgent(workspaceId: string) {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(noteAiAgentKey(workspaceId));
}

function writeNoteAiAgent(workspaceId: string, agentId: string | null) {
  if (typeof window === "undefined") return;
  const key = noteAiAgentKey(workspaceId);
  if (agentId) window.localStorage.setItem(key, agentId);
  else window.localStorage.removeItem(key);
}

function buildNoteOptimizationPrompt(request: TextOptimizationRequest, noteTitle: string) {
  const instruction = request.instruction.trim();
  return `You are editing a selected excerpt inside a user's note.
Use the nearby note context to understand references, terminology, and tone, but rewrite ONLY the selected excerpt.
${instruction ? "Follow the user's instruction for the selected excerpt." : "Default task: optimize the selected excerpt to be clearer, more fluent, and more natural."}
Preserve the original language, meaning, voice, and useful Markdown formatting unless the user explicitly asks otherwise.
Return ONLY content that can directly replace the selected excerpt. Do not include explanations, labels, greetings, surrounding context, or code fences.

Note title:
${noteTitle || "Untitled"}

Context before selection:
<context_before>
${request.contextBefore || "(none)"}
</context_before>

Selected excerpt to replace:
<selection>
${request.selectedText}
</selection>

Context after selection:
<context_after>
${request.contextAfter || "(none)"}
</context_after>

User instruction:
<instruction>
${instruction || "Optimize the selected excerpt."}
</instruction>`;
}

function normalizeOptimizedText(content: string) {
  const trimmed = content.trim();
  const fenced = /^```(?:markdown|md|text)?\s*\n([\s\S]*?)\n```$/i.exec(trimmed);
  return (fenced?.[1] ?? trimmed).trim();
}

async function waitForNoteOptimizationResult(
  sessionId: string,
  taskId: string,
  messages: { failed: string; timeout: string },
) {
  for (let attempt = 0; attempt < 75; attempt += 1) {
    // react-doctor-disable-next-line react-doctor/async-await-in-loop -- polling must wait between dependent reads until the agent writes the assistant message.
    const chatMessages = await api.listChatMessages(sessionId);
    let assistant = chatMessages[chatMessages.length - 1];
    for (let index = chatMessages.length - 1; index >= 0; index -= 1) {
      const message = chatMessages[index];
      if (message?.role === "assistant" && message.task_id === taskId) {
        assistant = message;
        break;
      }
    }
    if (assistant?.role === "assistant" && assistant.task_id === taskId) {
      if (assistant.failure_reason) throw new Error(assistant.content || messages.failed);
      const optimized = normalizeOptimizedText(assistant.content);
      if (optimized) return optimized;
    }
    // react-doctor-disable-next-line react-doctor/async-await-in-loop -- each delay spaces the next poll; parallel timers would hammer the API.
    await new Promise((resolve) => window.setTimeout(resolve, 1200));
  }
  throw new Error(messages.timeout);
}

function escapeHtml(value: string) {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function safeExportFilename(title: string, extension: string) {
  const basename = (title || "Untitled")
    .trim()
    .replace(/[\\/:*?"<>|]+/g, "-")
    .replace(/\s+/g, " ")
    .slice(0, 80) || "Untitled";
  return `${basename}.${extension}`;
}

function renderInlineMarkdown(value: string) {
  const tokens: string[] = [];
  let text = escapeHtml(value);
  text = text.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (_match, alt: string, src: string) => {
    const token = `@@NOTE_IMAGE_${tokens.length}@@`;
    tokens.push(`<img src="${escapeHtml(src)}" alt="${escapeHtml(alt)}" />`);
    return token;
  });
  text = text.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_match, label: string, href: string) => {
    const token = `@@NOTE_LINK_${tokens.length}@@`;
    tokens.push(`<a href="${escapeHtml(href)}">${label}</a>`);
    return token;
  });
  text = text.replace(/`([^`]+)`/g, "<code>$1</code>");
  text = text.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  text = text.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  tokens.forEach((tokenHtml, index) => {
    text = text.replace(`@@NOTE_IMAGE_${index}@@`, tokenHtml).replace(`@@NOTE_LINK_${index}@@`, tokenHtml);
  });
  return text;
}

function renderNoteMarkdown(content: string) {
  const lines = content.split(/\r?\n/);
  const html: string[] = [];
  let paragraph: string[] = [];
  let list: string[] = [];

  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    html.push(`<p>${renderInlineMarkdown(paragraph.join(" "))}</p>`);
    paragraph = [];
  };
  const flushList = () => {
    if (list.length === 0) return;
    html.push(`<ul>${list.map((item) => `<li>${renderInlineMarkdown(item)}</li>`).join("")}</ul>`);
    list = [];
  };

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = /^(#{1,3})\s+(.+)$/.exec(trimmed);
    if (heading) {
      flushParagraph();
      flushList();
      const level = heading[1]?.length ?? 1;
      html.push(`<h${level}>${renderInlineMarkdown(heading[2] ?? "")}</h${level}>`);
      continue;
    }
    const bullet = /^[-*]\s+(.+)$/.exec(trimmed);
    if (bullet) {
      flushParagraph();
      list.push(bullet[1] ?? "");
      continue;
    }
    flushList();
    paragraph.push(trimmed);
  }
  flushParagraph();
  flushList();
  return html.join("\n");
}

function buildNoteExportHtml(page: NotePage) {
  const title = escapeHtml(page.title || "Untitled");
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<title>${title}</title>
<style>
  body { color: #111827; font-family: Georgia, 'Times New Roman', serif; line-height: 1.65; margin: 48px auto; max-width: 820px; padding: 0 24px; }
  h1 { font-size: 40px; line-height: 1.15; margin: 0 0 28px; }
  h2, h3 { margin-top: 28px; }
  p { margin: 14px 0; }
  img { border-radius: 8px; display: block; height: auto; margin: 18px 0; max-width: 100%; }
  code { background: #f3f4f6; border-radius: 4px; padding: 2px 5px; }
  a { color: #2563eb; }
  @media print { body { margin: 0 auto; } }
</style>
</head>
<body>
<h1>${title}</h1>
${renderNoteMarkdown(page.content)}
</body>
</html>`;
}

function exportNoteAsHtml(page: NotePage) {
  const blob = new Blob([buildNoteExportHtml(page)], { type: "text/html;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = safeExportFilename(page.title, "html");
  anchor.click();
  URL.revokeObjectURL(url);
}

function exportNoteAsPdf(page: NotePage) {
  const printWindow = window.open("", "_blank");
  if (!printWindow) return false;
  printWindow.opener = null;
  printWindow.document.write(buildNoteExportHtml(page));
  printWindow.document.close();
  printWindow.focus();
  printWindow.print();
  return true;
}

function buildNoteTree(pages: NotePage[]): NoteTreeNode[] {
  const nodes = new Map<string, NoteTreeNode>();
  for (const page of pages) nodes.set(page.id, { ...page, children: [] });
  const roots: NoteTreeNode[] = [];
  for (const node of nodes.values()) {
    const parent = node.parent_id ? nodes.get(node.parent_id) : null;
    if (parent) parent.children.push(node);
    else roots.push(node);
  }
  const sort = (items: NoteTreeNode[]) => {
    items.sort((a, b) => a.sort_key.localeCompare(b.sort_key) || a.created_at.localeCompare(b.created_at));
    for (const item of items) sort(item.children);
  };
  sort(roots);
  return roots;
}

function noteSortKeyBetween(previous?: string, next?: string) {
  const nowKey = `${Date.now()}${String(Math.floor(Math.random() * 1000)).padStart(3, "0")}`;
  const toBigInt = (value?: string) => (/^\d+$/.test(value ?? "") ? BigInt(value ?? "0") : null);
  const previousNumber = toBigInt(previous);
  const nextNumber = toBigInt(next);
  if (previousNumber !== null && nextNumber !== null && nextNumber - previousNumber > 1n) {
    return ((previousNumber + nextNumber) / 2n).toString().padStart(20, "0");
  }
  if (previous) return `${previous}~${nowKey}`;
  if (next) return `!${next}:${nowKey}`;
  return nowKey.padStart(20, "0");
}

function isNoteDescendant(pages: NotePage[], ancestorId: string, targetId: string) {
  const byId = new Map(pages.map((page) => [page.id, page]));
  const seen = new Set<string>();
  let current = byId.get(targetId);
  while (current?.parent_id) {
    if (current.parent_id === ancestorId) return true;
    if (seen.has(current.parent_id)) return false;
    seen.add(current.parent_id);
    current = byId.get(current.parent_id);
  }
  return false;
}

function findNoteDropPosition(event: DragEvent<HTMLElement>): NoteDropPosition {
  const rect = event.currentTarget.getBoundingClientRect();
  const ratio = (event.clientY - rect.top) / Math.max(rect.height, 1);
  if (ratio < 0.25) return "before";
  if (ratio > 0.75) return "after";
  return "inside";
}

function findSiblingSortKeys(pages: NotePage[], parentId: string | null, draggedId: string, targetId: string, position: NoteDropPosition) {
  const siblings = pages
    .filter((page) => page.parent_id === parentId && page.id !== draggedId)
    .sort((a, b) => a.sort_key.localeCompare(b.sort_key) || a.created_at.localeCompare(b.created_at));
  if (position === "inside") return { previous: siblings.at(-1)?.sort_key, next: undefined };
  const targetIndex = siblings.findIndex((page) => page.id === targetId);
  if (targetIndex < 0) return { previous: siblings.at(-1)?.sort_key, next: undefined };
  return position === "before"
    ? { previous: siblings[targetIndex - 1]?.sort_key, next: siblings[targetIndex]?.sort_key }
    : { previous: siblings[targetIndex]?.sort_key, next: siblings[targetIndex + 1]?.sort_key };
}

function findNote(pages: NotePage[], id?: string): NotePage | null {
  if (!id) return null;
  return pages.find((page) => page.id === id) ?? null;
}

function collectNoteAncestorIds(pages: NotePage[], id?: string) {
  const expanded = new Set<string>();
  if (!id) return expanded;
  const byId = new Map(pages.map((page) => [page.id, page]));
  const seen = new Set<string>();
  let current = byId.get(id);
  while (current?.parent_id) {
    if (seen.has(current.parent_id)) break;
    seen.add(current.parent_id);
    expanded.add(current.parent_id);
    current = byId.get(current.parent_id);
  }
  return expanded;
}

function memberLabel(member: MemberWithUser | undefined, fallback = "") {
  return member?.display_name || member?.name || member?.email || fallback;
}

function NoteTreeRow({
  node,
  depth,
  activeId,
  expandedIds,
  onOpen,
  onToggle,
  onCreateChild,
  onShare,
  onDuplicate,
  onDelete,
  draggingId,
  dropTarget,
  onDragStart,
  onDragEnd,
  onDragOverNode,
  onDropNode,
}: {
  node: NoteTreeNode;
  depth: number;
  activeId?: string;
  expandedIds: ReadonlySet<string>;
  onOpen: (id: string) => void;
  onToggle: (id: string) => void;
  onCreateChild: (parentId: string) => void;
  onShare: (page: NotePage) => void;
  onDuplicate: (page: NotePage) => void;
  onDelete: (page: NotePage) => void;
  draggingId?: string | null;
  dropTarget?: NoteDropTarget | null;
  onDragStart: (id: string) => void;
  onDragEnd: () => void;
  onDragOverNode: (node: NotePage, event: DragEvent<HTMLDivElement>) => void;
  onDropNode: (node: NotePage, event: DragEvent<HTMLDivElement>) => void;
}) {
  const { t } = useT("layout");
  const updatePage = useUpdateNotePage();
  const [menuOpen, setMenuOpen] = useState(false);
  const [editingTitle, setEditingTitle] = useState(false);
  const [draftTitle, setDraftTitle] = useState("");
  const titleInputRef = useRef<HTMLInputElement | null>(null);
  const isActive = activeId === node.id;
  const hasChildren = node.children.length > 0;
  const isExpanded = expandedIds.has(node.id);
  const title = node.title || "Untitled";
  const toggleLabel = isExpanded ? t(($) => $.notes_page.collapse_page, { title }) : t(($) => $.notes_page.expand_page, { title });
  const canDrag = node.can_manage_shares && !editingTitle;
  const activeDropPosition = dropTarget?.id === node.id ? dropTarget.position : null;

  const startEditingTitle = () => {
    if (!node.can_manage_shares) return;
    setDraftTitle(node.title || "Untitled");
    setEditingTitle(true);
    window.setTimeout(() => titleInputRef.current?.focus(), 0);
  };

  const commitTitle = async () => {
    const nextTitle = draftTitle.trim() || "Untitled";
    setEditingTitle(false);
    if (nextTitle === node.title) return;
    try {
      await updatePage.mutateAsync({ id: node.id, data: { title: nextTitle } });
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.note_save_failed));
    }
  };

  return (
    <>
      <div
        draggable={canDrag}
        onDragStart={(event) => {
          if (!canDrag) return;
          event.dataTransfer.effectAllowed = "move";
          event.dataTransfer.setData("text/plain", node.id);
          onDragStart(node.id);
        }}
        onDragEnd={onDragEnd}
        onDragOver={(event) => onDragOverNode(node, event)}
        onDrop={(event) => onDropNode(node, event)}
        className={cn(
          "group relative flex h-8 items-center gap-1 rounded-md px-2 text-sm text-muted-foreground hover:bg-muted/70 hover:text-foreground",
          canDrag && "cursor-grab active:cursor-grabbing",
          draggingId === node.id && "opacity-45",
          isActive && "bg-muted text-foreground",
          activeDropPosition === "inside" && "bg-primary/10 ring-1 ring-primary/50",
          activeDropPosition === "before" && "before:absolute before:left-2 before:right-2 before:top-0 before:h-0.5 before:rounded-full before:bg-primary",
          activeDropPosition === "after" && "after:absolute after:bottom-0 after:left-2 after:right-2 after:h-0.5 after:rounded-full after:bg-primary",
        )}
        style={{ paddingLeft: 8 + depth * 14 }}
      >
        {!editingTitle && (
          <button
            type="button"
            className="absolute inset-0 rounded-md"
            onClick={() => onOpen(node.id)}
            aria-label={node.title || "Untitled"}
          />
        )}
        {hasChildren ? (
          <button
            type="button"
            className="relative z-10 flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground"
            onClick={(event) => {
              event.stopPropagation();
              onToggle(node.id);
            }}
            aria-expanded={isExpanded}
            aria-label={toggleLabel}
          >
            {isExpanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          </button>
        ) : (
          <span className="relative z-10 size-5 shrink-0" aria-hidden="true" />
        )}
        {editingTitle ? (
          <input
            ref={titleInputRef}
            value={draftTitle}
            onChange={(event) => setDraftTitle(event.target.value)}
            onClick={(event) => event.stopPropagation()}
            onBlur={() => void commitTitle()}
            onKeyDown={(event) => {
              if (event.key === "Enter") void commitTitle();
              if (event.key === "Escape") setEditingTitle(false);
            }}
            className="relative z-10 h-6 min-w-0 flex-1 rounded border bg-background px-1 text-sm text-foreground outline-none focus:ring-1 focus:ring-ring"
            aria-label={t(($) => $.notes_page.rename_page)}
          />
        ) : (
          <button
            type="button"
            className="relative z-10 flex h-full min-w-0 flex-1 items-center text-left"
            onClick={(event) => {
              event.stopPropagation();
              onOpen(node.id);
            }}
            onDoubleClick={(event) => {
              event.stopPropagation();
              startEditingTitle();
            }}
          >
            <span className="truncate">{title}</span>
          </button>
        )}
        <div className={cn("relative z-10 ml-auto items-center gap-0.5", menuOpen ? "flex" : "hidden group-hover:flex")} onClick={(event) => event.stopPropagation()}>
          {node.can_manage_shares && (
            <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
              <DropdownMenuTrigger render={<button type="button" aria-label={t(($) => $.notes_page.page_menu)} />} className="flex size-5 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground">
                <MoreHorizontal className="size-3.5" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => onShare(node)}>
                  <Share2 className="size-3.5" />
                  {t(($) => $.notes_page.share_action)}
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => onDuplicate(node)}>
                  <Copy className="size-3.5" />
                  {t(($) => $.notes_page.duplicate_action)}
                </DropdownMenuItem>
                <DropdownMenuItem variant="destructive" onClick={() => onDelete(node)}>
                  <Trash2 className="size-3.5" />
                  {t(($) => $.notes_page.delete_action)}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <button
            type="button"
            className="flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground"
            onClick={() => onCreateChild(node.id)}
            aria-label={t(($) => $.notes_page.create_child)}
          >
            <Plus className="size-3.5" />
          </button>
        </div>
      </div>
      {isExpanded &&
        node.children.map((child) => (
          <NoteTreeRow key={child.id} node={child} depth={depth + 1} activeId={activeId} expandedIds={expandedIds} onOpen={onOpen} onToggle={onToggle} onCreateChild={onCreateChild} onShare={onShare} onDuplicate={onDuplicate} onDelete={onDelete} draggingId={draggingId} dropTarget={dropTarget} onDragStart={onDragStart} onDragEnd={onDragEnd} onDragOverNode={onDragOverNode} onDropNode={onDropNode} />
        ))}
    </>
  );
}

function ShareDialogBody({
  page,
  members,
  onOpenChange,
}: {
  page: NotePage;
  members: MemberWithUser[];
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("layout");
  const [selected, setSelected] = useState<Set<string>>(() => new Set(page.share_user_ids));
  const updateShares = useUpdateNotePageShares();
  const shareableMembers = members.filter((member) => member.user_id !== page.owner_user_id);
  const allShareableSelected = shareableMembers.length > 0 && shareableMembers.every((member) => selected.has(member.user_id));

  const save = async () => {
    try {
      await updateShares.mutateAsync({ id: page.id, data: { user_ids: [...selected] } });
      toast.success(t(($) => $.notes_page.share_saved));
      onOpenChange(false);
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.share_save_failed));
    }
  };

  return (
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{t(($) => $.notes_page.share_title)}</DialogTitle>
        <DialogDescription>{t(($) => $.notes_page.share_description)}</DialogDescription>
      </DialogHeader>
      <div className="max-h-72 space-y-2 overflow-y-auto py-2">
        {shareableMembers.length === 0 ? (
          <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">{t(($) => $.notes_page.no_other_members)}</div>
        ) : (
          shareableMembers.map((member) => {
            const checked = selected.has(member.user_id);
            return (
              <label key={member.user_id} className="flex cursor-pointer items-center gap-3 rounded-md px-2 py-2 hover:bg-muted/60">
                <Checkbox
                  checked={checked}
                  onCheckedChange={(value) => {
                    setSelected((current) => {
                      const next = new Set(current);
                      if (value) next.add(member.user_id);
                      else next.delete(member.user_id);
                      return next;
                    });
                  }}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{member.display_name || member.name}</span>
                  <span className="block truncate text-xs text-muted-foreground">{member.email}</span>
                </span>
              </label>
            );
          })
        )}
      </div>
      <DialogFooter>
        {shareableMembers.length > 0 && (
          <label className="mr-auto flex cursor-pointer items-center gap-2 rounded-md px-1 py-1 text-sm font-medium text-foreground hover:text-foreground/80">
            <Checkbox
              checked={allShareableSelected}
              onCheckedChange={(value) => {
                setSelected((current) => {
                  const next = new Set(current);
                  shareableMembers.forEach((member) => {
                    if (value) next.add(member.user_id);
                    else next.delete(member.user_id);
                  });
                  return next;
                });
              }}
            />
            <span>{t(($) => $.notes_page.select_all)}</span>
          </label>
        )}
        <Button variant="outline" onClick={() => onOpenChange(false)}>{t(($) => $.notes_page.cancel)}</Button>
        <Button onClick={save} disabled={updateShares.isPending}>{t(($) => $.notes_page.save)}</Button>
      </DialogFooter>
    </DialogContent>
  );
}

function ShareDialog({
  page,
  members,
  open,
  onOpenChange,
}: {
  page: NotePage | null;
  members: MemberWithUser[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {page ? <ShareDialogBody key={page.id} page={page} members={members} onOpenChange={onOpenChange} /> : null}
    </Dialog>
  );
}

function NoteAiAgentDialog({
  agents,
  selectedAgentId,
  open,
  onOpenChange,
  onSelect,
}: {
  agents: Agent[];
  selectedAgentId: string | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (agentId: string | null) => void;
}) {
  const { t } = useT("layout");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.notes_page.ai_agent_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.notes_page.ai_agent_description)}</DialogDescription>
        </DialogHeader>
        <div className="max-h-72 space-y-1 overflow-y-auto py-2">
          {agents.length === 0 ? (
            <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">{t(($) => $.notes_page.ai_agent_empty)}</div>
          ) : (
            agents.map((agent) => {
              const selected = selectedAgentId === agent.id;
              const name = resolveActorDisplayName(agent, agent.name || agent.id);
              return (
                <button
                  key={agent.id}
                  type="button"
                  className={cn("flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-muted/70", selected && "bg-muted text-foreground")}
                  onClick={() => onSelect(agent.id)}
                >
                  <Bot className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate">{name}</span>
                  {selected && <Check className="size-4 text-primary" />}
                </button>
              );
            })
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onSelect(null)} disabled={!selectedAgentId}>{t(($) => $.notes_page.ai_agent_clear)}</Button>
          <Button onClick={() => onOpenChange(false)}>{t(($) => $.notes_page.done)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ExportDialog({
  page,
  open,
  onOpenChange,
}: {
  page: NotePage | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("layout");
  const [format, setFormat] = useState<NoteExportFormat>("pdf");

  const exportNote = () => {
    if (!page) return;
    if (format === "html") {
      exportNoteAsHtml(page);
      onOpenChange(false);
      return;
    }
    if (!exportNoteAsPdf(page)) {
      showErrorToast(t(($) => $.notes_page.export_popup_blocked));
      return;
    }
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.notes_page.export_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.notes_page.export_description)}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-2 py-2">
          <button
            type="button"
            className={cn("rounded-lg border p-3 text-left text-sm hover:bg-muted/60", format === "pdf" && "border-primary bg-muted")}
            onClick={() => setFormat("pdf")}
          >
            <div className="font-medium">{t(($) => $.notes_page.export_pdf)}</div>
            <div className="mt-1 text-xs text-muted-foreground">{t(($) => $.notes_page.export_pdf_description)}</div>
          </button>
          <button
            type="button"
            className={cn("rounded-lg border p-3 text-left text-sm hover:bg-muted/60", format === "html" && "border-primary bg-muted")}
            onClick={() => setFormat("html")}
          >
            <div className="font-medium">{t(($) => $.notes_page.export_html)}</div>
            <div className="mt-1 text-xs text-muted-foreground">{t(($) => $.notes_page.export_html_description)}</div>
          </button>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t(($) => $.notes_page.cancel)}</Button>
          <Button onClick={exportNote}>{t(($) => $.notes_page.export_action)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function NoteEditor({
  selected,
  childPages,
  currentUserId,
  ownerName,
  shareNames,
  onOpenPage,
  onOpenShare,
  onOptimizeSelection,
}: {
  selected: NotePage;
  childPages: NotePage[];
  currentUserId?: string;
  ownerName: string;
  shareNames: string[];
  onOpenPage: (id: string) => void;
  onOpenShare: () => void;
  onOptimizeSelection: (request: TextOptimizationRequest) => Promise<string>;
}) {
  const { t } = useT("layout");
  const editorRef = useRef<ContentEditorRef | null>(null);
  const { mutateAsync: updateNotePage } = useUpdateNotePage();
  const { uploadWithToast, uploading } = useFileUpload(api, (error) => {
    showErrorToast(error.message || t(($) => $.notes_page.image_paste_failed));
  });
  const [draft, setDraft] = useState(() => ({
    title: selected.title,
    content: selected.content,
    serverTitle: selected.title,
    serverContent: selected.content,
  }));
  if (draft.serverTitle !== selected.title || draft.serverContent !== selected.content) {
    const title = draft.title === draft.serverTitle ? selected.title : draft.title;
    const content = draft.content === draft.serverContent ? selected.content : draft.content;
    setDraft({ title, content, serverTitle: selected.title, serverContent: selected.content });
  }
  const dirty = draft.title !== draft.serverTitle || draft.content !== draft.serverContent;
  const [saveState, setSaveState] = useState<"saved" | "pending" | "saving" | "error">("saved");

  // react-doctor-disable-next-line react-doctor/no-cascading-set-state -- autosave status follows local draft dirtiness and async save lifecycle.
  useEffect(() => {
    if (!dirty) {
      // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- local draft just reached the saved server snapshot.
      setSaveState("saved");
      return;
    }
    // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- status is an autosave side effect, not a rendered copy of props.
    setSaveState("pending");
    let active = true;
    const timeout = window.setTimeout(() => {
      setSaveState("saving");
      updateNotePage({ id: selected.id, data: { title: draft.title, content: draft.content } })
        .then((page) => {
          if (!active) return;
          setDraft((current) => ({ ...current, serverTitle: page.title, serverContent: page.content }));
          setSaveState("saved");
        })
        .catch((error: unknown) => {
          if (!active) return;
          setSaveState("error");
          showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.note_save_failed));
        });
    }, 900);
    return () => {
      active = false;
      window.clearTimeout(timeout);
    };
  }, [dirty, draft.content, draft.title, selected.id, t, updateNotePage]);

  return (
    <div className="mx-auto flex min-h-full max-w-4xl flex-col px-8 py-6">
      <div className="mb-4 flex items-center gap-2">
        <div className="flex min-w-0 flex-1 items-center gap-2 text-xs text-muted-foreground">
          {selected.owner_user_id === currentUserId ? <Lock className="size-3.5" /> : <Users className="size-3.5" />}
          {selected.owner_user_id !== currentUserId ? (
            <span>
              <span className="font-medium text-foreground/70 underline decoration-muted-foreground/50 underline-offset-2">{ownerName}</span>
              <span>{t(($) => $.notes_page.visibility_shared_by_suffix)}</span>
            </span>
          ) : shareNames.length > 0 ? (
            <span className="inline-flex min-w-0 items-center gap-1">
              <span className="text-muted-foreground">{t(($) => $.notes_page.visibility_shared_to_prefix)}</span>
              <HoverCard>
                <HoverCardTrigger
                  render={<button type="button" aria-label={t(($) => $.notes_page.current_shares)} />}
                  delay={120}
                  closeDelay={160}
                  className="inline-flex max-w-36 items-center truncate rounded-sm font-medium text-foreground/70 underline decoration-muted-foreground/50 underline-offset-2 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                >
                  <span className="truncate">{shareNames[0]}</span>
                </HoverCardTrigger>
                <HoverCardContent align="start" className="w-56 p-2">
                  <div className="px-1 pb-1 text-xs font-medium text-muted-foreground">{t(($) => $.notes_page.current_shares)}</div>
                  <div className="max-h-56 overflow-y-auto">
                    {shareNames.map((name, index) => (
                      <div key={`${name}:${index}`} className="truncate rounded-md px-2 py-1.5 text-sm text-foreground/80">
                        {name}
                      </div>
                    ))}
                  </div>
                </HoverCardContent>
              </HoverCard>
              {shareNames.length > 1 && <span className="text-muted-foreground/70">{t(($) => $.notes_page.visibility_shared_etc)}</span>}
            </span>
          ) : (
            <span>{t(($) => $.notes_page.visibility_private)}</span>
          )}
        </div>
        <span className="text-xs text-muted-foreground">
          {saveState === "saving" || uploading ? t(($) => $.notes_page.autosave_saving) : saveState === "error" ? t(($) => $.notes_page.autosave_error) : dirty ? t(($) => $.notes_page.autosave_pending) : t(($) => $.notes_page.autosave_saved)}
        </span>
        {selected.can_manage_shares && (
          <Button variant="outline" size="sm" onClick={onOpenShare}>
            <Share2 className="size-4" />
            {t(($) => $.notes_page.share_action)}
          </Button>
        )}
      </div>
      <Input
        value={draft.title}
        onChange={(event) => setDraft((current) => ({ ...current, title: event.target.value }))}
        onKeyDown={(event) => {
          const input = event.currentTarget;
          const caretAtEnd = input.selectionStart === input.value.length && input.selectionEnd === input.value.length;
          if (event.key === "Enter" && caretAtEnd) {
            event.preventDefault();
            editorRef.current?.insertBlankLineAtStart();
          }
        }}
        className="h-auto border-0 px-0 py-1 text-3xl font-semibold leading-tight shadow-none focus-visible:ring-0 md:text-4xl"
        placeholder="Untitled"
      />
      {childPages.length > 0 && (
        <div className="mt-5 space-y-1">
          <div className="px-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">{t(($) => $.notes_page.child_links)}</div>
          {childPages.map((page) => (
            <button
              key={page.id}
              type="button"
              className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm text-muted-foreground hover:bg-muted/70 hover:text-foreground"
              onClick={() => onOpenPage(page.id)}
            >
              <FileText className="size-4 shrink-0" />
              <span className="truncate">{page.title || "Untitled"}</span>
            </button>
          ))}
        </div>
      )}
      <ContentEditor
        ref={editorRef}
        defaultValue={selected.content}
        onUpdate={(content) => setDraft((current) => ({ ...current, content }))}
        onUploadFile={uploadWithToast}
        placeholder={t(($) => $.notes_page.content_placeholder)}
        className="mt-6 min-h-[55vh] px-0 pb-[45vh] pt-2"
        debounceMs={150}
        disableMentions
        enableSlashCommands
        slashCommandMode="block"
        showBubbleMenu
        onOptimizeSelection={onOptimizeSelection}
      />
    </div>
  );
}

export function NotesPage({ pageId }: { pageId?: string }) {
  const { t } = useT("layout");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const currentUserId = useAuthStore((s) => s.user?.id);
  const { data: list = { pages: [] }, isLoading } = useQuery(noteListOptions(wsId));
  const { data: trash = { pages: [] } } = useQuery(noteTrashOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const selectedFromList = findNote(list.pages, pageId);
  const selectedId = pageId ?? selectedFromList?.id;
  const { data: detailPage } = useQuery(noteDetailOptions(wsId, selectedId ?? ""));
  const selected = detailPage?.id ? detailPage : selectedFromList;
  const [noteExpansionOverrides, setNoteExpansionOverrides] = useState<NoteExpansionOverrides>(() => ({ selectionId: null, expanded: new Set(), collapsed: new Set() }));
  const tree = useMemo(() => buildNoteTree(list.pages), [list.pages]);
  const ownTree = useMemo(() => tree.filter((node) => node.owner_user_id === currentUserId), [currentUserId, tree]);
  const sharedTree = useMemo(() => tree.filter((node) => node.owner_user_id !== currentUserId), [currentUserId, tree]);
  const selectedExpansionId = selected?.id ?? pageId ?? null;
  const selectedAncestorNoteIds = useMemo(() => collectNoteAncestorIds(list.pages, selectedExpansionId ?? undefined), [list.pages, selectedExpansionId]);
  const expandedNoteIds = useMemo(() => {
    const next = new Set([...noteExpansionOverrides.expanded, ...selectedAncestorNoteIds]);
    if (noteExpansionOverrides.selectionId === selectedExpansionId) {
      for (const id of noteExpansionOverrides.collapsed) next.delete(id);
    }
    return next;
  }, [noteExpansionOverrides, selectedAncestorNoteIds, selectedExpansionId]);
  const selectedChildPages = useMemo(
    () =>
      selected
        ? list.pages
            .filter((page) => page.parent_id === selected.id)
            .sort((a, b) => a.sort_key.localeCompare(b.sort_key) || a.created_at.localeCompare(b.created_at))
        : [],
    [list.pages, selected],
  );
  const membersByUserId = useMemo(() => new Map(members.map((member) => [member.user_id, member])), [members]);
  const selectedShareNames = useMemo(
    () => selected?.share_user_ids.flatMap((id) => {
      const name = memberLabel(membersByUserId.get(id), id);
      return name ? [name] : [];
    }) ?? [],
    [membersByUserId, selected?.share_user_ids],
  );
  const selectedOwnerName = selected ? memberLabel(membersByUserId.get(selected.owner_user_id), selected.owner_user_id) : "";
  const createPage = useCreateNotePage();
  const duplicatePage = useDuplicateNotePage();
  const movePage = useMoveNotePage();
  const deletePage = useDeleteNotePage();
  const permanentlyDeletePage = usePermanentlyDeleteNotePage();
  const restorePage = useRestoreNotePage();
  const [uiState, setUiState] = useState<NotesPageUiState>(() => ({ sharePage: null, exportOpen: false, aiAgentOpen: false, showTrash: false }));
  const [dragState, setDragState] = useState<NoteDragState>({ draggingId: null, dropTarget: null });
  const [aiAgentConfig, setAiAgentConfig] = useState<NoteAiAgentConfig>(() => ({ workspaceId: null, agentId: null }));
  const configuredAiAgentId = aiAgentConfig.workspaceId === wsId ? aiAgentConfig.agentId : wsId ? readNoteAiAgent(wsId) : null;
  const { sharePage, exportOpen, aiAgentOpen, showTrash } = uiState;
  const { draggingId: draggingNoteId } = dragState;

  useEffect(() => {
    if (!wsId || !selected?.id || showTrash) return;
    writeLastViewedNote(wsId, selected.id);
  }, [selected?.id, showTrash, wsId]);

  useEffect(() => {
    if (!wsId || pageId || isLoading || showTrash || list.pages.length === 0) return;
    const lastViewedId = readLastViewedNote(wsId);
    const target = list.pages.find((page) => page.id === lastViewedId)?.id ?? ownTree[0]?.id ?? sharedTree[0]?.id ?? list.pages[0]?.id;
    if (target) navigation.replace(paths.noteDetail(target));
  }, [isLoading, list.pages, navigation, ownTree, pageId, paths, sharedTree, showTrash, wsId]);

  const openPage = (id: string) => {
    setUiState((current) => ({ ...current, showTrash: false }));
    navigation.push(paths.noteDetail(id));
  };

  const noteDragAllowed = (draggedId: string, target: NotePage, position: NoteDropPosition) => {
    const dragged = findNote(list.pages, draggedId);
    if (!dragged?.can_manage_shares || !target.can_manage_shares || dragged.id === target.id) return false;
    if (position === "inside" && isNoteDescendant(list.pages, dragged.id, target.id)) return false;
    if (position !== "inside" && target.parent_id && isNoteDescendant(list.pages, dragged.id, target.parent_id)) return false;
    return true;
  };

  const handleNoteDragStart = (id: string) => {
    setDragState({ draggingId: id, dropTarget: null });
  };

  const handleNoteDragEnd = () => {
    setDragState({ draggingId: null, dropTarget: null });
  };

  const handleNoteDragOver = (target: NotePage, event: DragEvent<HTMLDivElement>) => {
    if (!draggingNoteId) return;
    const position = findNoteDropPosition(event);
    if (!noteDragAllowed(draggingNoteId, target, position)) {
      setDragState((current) => (current.dropTarget ? { ...current, dropTarget: null } : current));
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    event.dataTransfer.dropEffect = "move";
    setDragState((current) => (current.dropTarget?.id === target.id && current.dropTarget.position === position ? current : { ...current, dropTarget: { id: target.id, position } }));
  };

  const handleNoteDrop = async (target: NotePage, event: DragEvent<HTMLDivElement>) => {
    if (!draggingNoteId) return;
    const position = findNoteDropPosition(event);
    if (!noteDragAllowed(draggingNoteId, target, position)) return;
    event.preventDefault();
    event.stopPropagation();
    const parentId = position === "inside" ? target.id : target.parent_id;
    const { previous, next } = findSiblingSortKeys(list.pages, parentId, draggingNoteId, target.id, position);
    try {
      await movePage.mutateAsync({ id: draggingNoteId, data: { parent_id: parentId, sort_key: noteSortKeyBetween(previous, next) } });
      if (position === "inside") {
        setNoteExpansionOverrides((current) => ({ ...current, expanded: new Set(current.expanded).add(target.id) }));
      }
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.note_save_failed));
    } finally {
      handleNoteDragEnd();
    }
  };

  const saveConfiguredAiAgent = (agentId: string | null) => {
    setAiAgentConfig({ workspaceId: wsId || null, agentId });
    if (wsId) writeNoteAiAgent(wsId, agentId);
    if (agentId) toast.success(t(($) => $.notes_page.ai_agent_saved));
  };

  const optimizeSelectedNoteText = useCallback(
    async (request: TextOptimizationRequest) => {
      const agent = agents.find((item) => item.id === configuredAiAgentId);
      if (!agent) {
        setUiState((current) => ({ ...current, aiAgentOpen: true }));
        throw new Error(t(($) => $.notes_page.ai_agent_required));
      }
      const session = await api.createChatSession({
        agent_id: agent.id,
        title: t(($) => $.notes_page.ai_optimize_chat_title, { title: selected?.title || t(($) => $.notes_page.title) }),
      });
      try {
        const sent = await api.sendChatMessage(session.id, buildNoteOptimizationPrompt(request, selected?.title || "Untitled"));
        const optimized = await waitForNoteOptimizationResult(session.id, sent.task_id, {
          failed: t(($) => $.notes_page.ai_optimize_failed),
          timeout: t(($) => $.notes_page.ai_optimize_timeout),
        });
        await api.updateChatSession(session.id, { status: "archived" }).catch(() => undefined);
        return optimized;
      } catch (error) {
        await api.updateChatSession(session.id, { status: "archived" }).catch(() => undefined);
        throw error;
      }
    },
    [agents, configuredAiAgentId, selected?.title, t],
  );

  const toggleNoteExpanded = (id: string) => {
    const isExpanded = expandedNoteIds.has(id);
    setNoteExpansionOverrides((current) => {
      const expanded = new Set(current.expanded);
      const collapsed = new Set(current.collapsed);
      if (isExpanded) {
        expanded.delete(id);
        collapsed.add(id);
      } else {
        expanded.add(id);
        collapsed.delete(id);
      }
      return { selectionId: selectedExpansionId, expanded, collapsed };
    });
  };

  const handleCreate = async (parentId?: string | null) => {
    try {
      const page = await createPage.mutateAsync({ parent_id: parentId ?? null, title: "Untitled" });
      setUiState((current) => ({ ...current, showTrash: false }));
      navigation.push(paths.noteDetail(page.id));
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.create_failed));
    }
  };

  const handleDuplicate = async (page: NotePage) => {
    if (!page.can_manage_shares) return;
    try {
      const response = await duplicatePage.mutateAsync({
        id: page.id,
        data: { title: t(($) => $.notes_page.duplicate_title, { title: page.title }) },
      });
      const copiedPage = response.pages[0];
      toast.success(t(($) => $.notes_page.duplicated));
      if (copiedPage) {
        setUiState((current) => ({ ...current, showTrash: false }));
        navigation.push(paths.noteDetail(copiedPage.id));
      }
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.duplicate_failed));
    }
  };

  const handleDelete = async (page: NotePage) => {
    if (!page.can_manage_shares) return;
    if (!window.confirm(t(($) => $.notes_page.delete_confirm, { title: page.title }))) return;
    try {
      await deletePage.mutateAsync(page.id);
      if (selected?.id === page.id) navigation.push(paths.notes());
      toast.success(t(($) => $.notes_page.moved_to_trash));
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.delete_failed));
    }
  };

  const handlePermanentlyDelete = async (page: NotePage) => {
    if (!window.confirm(t(($) => $.notes_page.permanent_delete_confirm, { title: page.title }))) return;
    try {
      await permanentlyDeletePage.mutateAsync(page.id);
      toast.success(t(($) => $.notes_page.permanently_deleted));
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.permanent_delete_failed));
    }
  };

  const handleRestore = async (page: NotePage) => {
    try {
      const restored = await restorePage.mutateAsync(page.id);
      toast.success(t(($) => $.notes_page.restored));
      setUiState((current) => ({ ...current, showTrash: false }));
      navigation.push(paths.noteDetail(restored.id));
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.restore_failed));
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <PageHeader>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <FileText className="size-4 text-muted-foreground" />
          <div className="truncate font-medium">{t(($) => $.notes_page.title)}</div>
        </div>
        {selected && !showTrash && (
          <DropdownMenu>
            <DropdownMenuTrigger render={<button type="button" aria-label={t(($) => $.notes_page.page_menu)} />} className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground">
              <MoreHorizontal className="size-4" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setUiState((current) => ({ ...current, aiAgentOpen: true }))}>
                <Sparkles className="size-3.5" />
                {t(($) => $.notes_page.ai_agent_action)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => setUiState((current) => ({ ...current, exportOpen: true }))}>
                <Download className="size-3.5" />
                {t(($) => $.notes_page.export_action)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </PageHeader>
      <div className="flex min-h-0 flex-1">
        <aside className="flex w-72 shrink-0 flex-col border-r bg-muted/20">
          <div className="flex items-center justify-between px-3 py-2 text-xs font-medium text-muted-foreground">
            <span>{t(($) => $.notes_page.my_directory)}</span>
            <Button size="icon" variant="ghost" className="size-7" onClick={() => {
              setUiState((current) => ({ ...current, showTrash: false }));
              handleCreate(null);
            }}>
              <Plus className="size-4" />
            </Button>
          </div>
          <Separator />
          <div className="min-h-0 flex-1 overflow-y-auto p-2">
            {isLoading ? (
              <div className="space-y-2 p-2">
                <div className="h-4 w-36 rounded bg-muted" />
                <div className="h-4 w-28 rounded bg-muted" />
              </div>
            ) : (
              <div className="space-y-3">
                <div className="space-y-0.5">
                  {ownTree.length === 0 ? (
                    <button type="button" className="w-full rounded-lg border border-dashed p-4 text-left text-sm text-muted-foreground hover:bg-muted/50" onClick={() => handleCreate(null)}>
                      {t(($) => $.notes_page.empty_create_hint)}
                    </button>
                  ) : (
                    ownTree.map((node) => (
                      <NoteTreeRow key={node.id} node={node} depth={0} activeId={selected?.id} expandedIds={expandedNoteIds} onOpen={openPage} onToggle={toggleNoteExpanded} onCreateChild={(parentId) => handleCreate(parentId)} onShare={(page) => setUiState((current) => ({ ...current, sharePage: page }))} onDuplicate={handleDuplicate} onDelete={handleDelete} draggingId={dragState.draggingId} dropTarget={dragState.dropTarget} onDragStart={handleNoteDragStart} onDragEnd={handleNoteDragEnd} onDragOverNode={handleNoteDragOver} onDropNode={handleNoteDrop} />
                    ))
                  )}
                </div>
                {sharedTree.length > 0 && (
                  <div className="space-y-0.5">
                    <div className="px-2 pb-1 pt-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                      {t(($) => $.notes_page.shared_directory)}
                    </div>
                    {sharedTree.map((node) => (
                      <NoteTreeRow key={node.id} node={node} depth={0} activeId={selected?.id} expandedIds={expandedNoteIds} onOpen={openPage} onToggle={toggleNoteExpanded} onCreateChild={(parentId) => handleCreate(parentId)} onShare={(page) => setUiState((current) => ({ ...current, sharePage: page }))} onDuplicate={handleDuplicate} onDelete={handleDelete} draggingId={dragState.draggingId} dropTarget={dragState.dropTarget} onDragStart={handleNoteDragStart} onDragEnd={handleNoteDragEnd} onDragOverNode={handleNoteDragOver} onDropNode={handleNoteDrop} />
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
          <div className="border-t p-2">
            <Button variant={showTrash ? "secondary" : "ghost"} size="sm" className="w-full justify-start" onClick={() => setUiState((current) => ({ ...current, showTrash: !current.showTrash }))}>
              <Trash2 className="size-4" />
              {t(($) => $.notes_page.trash)}
              {trash.pages.length > 0 && <span className="ml-auto text-xs text-muted-foreground">{trash.pages.length}</span>}
            </Button>
          </div>
        </aside>
        <main className="min-w-0 flex-1 overflow-y-auto">
          {showTrash ? (
            <div className="mx-auto max-w-3xl px-8 py-8">
              <h1 className="text-2xl font-semibold">{t(($) => $.notes_page.trash)}</h1>
              <p className="mt-1 text-sm text-muted-foreground">{t(($) => $.notes_page.trash_description)}</p>
              <div className="mt-6 space-y-2">
                {trash.pages.length === 0 ? (
                  <div className="rounded-xl border border-dashed p-6 text-sm text-muted-foreground">{t(($) => $.notes_page.trash_empty)}</div>
                ) : (
                  trash.pages.map((page) => (
                    <div key={page.id} className="flex items-center gap-3 rounded-xl border p-3">
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium">{page.title}</div>
                        {page.deleted_at && <div className="text-xs text-muted-foreground">{page.deleted_at}</div>}
                      </div>
                      <Button size="sm" variant="outline" onClick={() => handleRestore(page)} disabled={restorePage.isPending}>
                        <Undo2 className="size-4" />
                        {t(($) => $.notes_page.restore_action)}
                      </Button>
                      <Button size="sm" variant="destructive" onClick={() => handlePermanentlyDelete(page)} disabled={permanentlyDeletePage.isPending}>
                        <Trash2 className="size-4" />
                        {t(($) => $.notes_page.permanent_delete_action)}
                      </Button>
                    </div>
                  ))
                )}
              </div>
            </div>
          ) : !selected ? (
            <div className="mx-auto flex h-full max-w-xl flex-col items-center justify-center px-6 text-center">
              <div className="mb-4 flex size-12 items-center justify-center rounded-2xl bg-muted">
                <FileText className="size-6 text-muted-foreground" />
              </div>
              <h1 className="text-xl font-semibold">{t(($) => $.notes_page.empty_title)}</h1>
              <p className="mt-2 text-sm text-muted-foreground">{t(($) => $.notes_page.empty_description)}</p>
              <Button className="mt-5" onClick={() => handleCreate(null)}>
                <Plus className="size-4" />
                {t(($) => $.notes_page.create_first)}
              </Button>
            </div>
          ) : (
            <NoteEditor
              key={selected.id}
              selected={selected}
              childPages={selectedChildPages}
              currentUserId={currentUserId}
              ownerName={selectedOwnerName}
              shareNames={selectedShareNames}
              onOpenPage={openPage}
              onOpenShare={() => setUiState((current) => ({ ...current, sharePage: selected }))}
              onOptimizeSelection={optimizeSelectedNoteText}
            />
          )}
        </main>
      </div>
      <ShareDialog page={sharePage} members={members} open={!!sharePage} onOpenChange={(open) => {
        if (!open) setUiState((current) => ({ ...current, sharePage: null }));
      }} />
      <NoteAiAgentDialog agents={agents} selectedAgentId={configuredAiAgentId} open={aiAgentOpen} onOpenChange={(open) => setUiState((current) => ({ ...current, aiAgentOpen: open }))} onSelect={saveConfiguredAiAgent} />
      <ExportDialog page={selected} open={exportOpen} onOpenChange={(open) => setUiState((current) => ({ ...current, exportOpen: open }))} />
    </div>
  );
}
