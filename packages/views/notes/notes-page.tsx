"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FileText, Lock, MoreHorizontal, Plus, Share2, Trash2, Undo2, Users } from "lucide-react";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { useWorkspacePaths } from "@multica/core/paths";
import { noteDetailOptions, noteListOptions, noteTrashOptions } from "@multica/core/notes/queries";
import { useCreateNotePage, useDeleteNotePage, useRestoreNotePage, useUpdateNotePage, useUpdateNotePageShares } from "@multica/core/notes/mutations";
import { memberListOptions } from "@multica/core/workspace/queries";
import type { MemberWithUser, NotePage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@multica/ui/components/ui/dropdown-menu";
import { Input } from "@multica/ui/components/ui/input";
import { Separator } from "@multica/ui/components/ui/separator";
import { cn } from "@multica/ui/lib/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { ContentEditor } from "../editor";
import { useNavigation } from "../navigation";
import { PageHeader } from "../layout/page-header";
import { useT } from "../i18n/use-t";

type NoteTreeNode = NotePage & { children: NoteTreeNode[] };

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

function findNote(pages: NotePage[], id?: string): NotePage | null {
  if (!id) return null;
  return pages.find((page) => page.id === id) ?? null;
}

function NoteTreeRow({
  node,
  depth,
  activeId,
  onOpen,
  onCreateChild,
  onShare,
  onDelete,
}: {
  node: NoteTreeNode;
  depth: number;
  activeId?: string;
  onOpen: (id: string) => void;
  onCreateChild: (parentId: string) => void;
  onShare: (page: NotePage) => void;
  onDelete: (page: NotePage) => void;
}) {
  const { t } = useT("layout");
  const updatePage = useUpdateNotePage();
  const [menuOpen, setMenuOpen] = useState(false);
  const [editingTitle, setEditingTitle] = useState(false);
  const [draftTitle, setDraftTitle] = useState("");
  const titleInputRef = useRef<HTMLInputElement | null>(null);
  const isActive = activeId === node.id;

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
        className={cn(
          "group relative flex h-8 items-center gap-1 rounded-md px-2 text-sm text-muted-foreground hover:bg-muted/70 hover:text-foreground",
          isActive && "bg-muted text-foreground",
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
            <span className="truncate">{node.title || "Untitled"}</span>
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
      {node.children.map((child) => (
        <NoteTreeRow key={child.id} node={child} depth={depth + 1} activeId={activeId} onOpen={onOpen} onCreateChild={onCreateChild} onShare={onShare} onDelete={onDelete} />
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

function NoteEditor({
  selected,
  currentUserId,
  onOpenShare,
}: {
  selected: NotePage;
  currentUserId?: string;
  onOpenShare: () => void;
}) {
  const { t } = useT("layout");
  const { mutateAsync: updateNotePage } = useUpdateNotePage();
  const { uploadWithToast, uploading } = useFileUpload(api, (error) => {
    showErrorToast(error.message || t(($) => $.notes_page.image_paste_failed));
  });
  // react-doctor-disable-next-line react-doctor/no-derived-useState -- NoteEditor is keyed by selected.id, so each page gets an isolated editable draft.
  const [draftTitle, setDraftTitle] = useState(selected.title);
  // react-doctor-disable-next-line react-doctor/no-derived-useState -- Content is an explicit unsaved draft, not a mirrored server-state cache.
  const [draftContent, setDraftContent] = useState(selected.content);
  const dirty = draftTitle !== selected.title || draftContent !== selected.content;
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
      updateNotePage({ id: selected.id, data: { title: draftTitle, content: draftContent } })
        .then(() => {
          if (active) setSaveState("saved");
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
  }, [dirty, draftContent, draftTitle, selected.id, t, updateNotePage]);

  return (
    <div className="mx-auto flex min-h-full max-w-4xl flex-col px-8 py-6">
      <div className="mb-4 flex items-center gap-2">
        <div className="flex min-w-0 flex-1 items-center gap-2 text-xs text-muted-foreground">
          {selected.owner_user_id === currentUserId ? <Lock className="size-3.5" /> : <Users className="size-3.5" />}
          <span>{selected.owner_user_id === currentUserId ? t(($) => $.notes_page.visibility_private) : t(($) => $.notes_page.visibility_shared)}</span>
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
        value={draftTitle}
        onChange={(event) => setDraftTitle(event.target.value)}
        className="border-0 px-0 text-3xl font-semibold shadow-none focus-visible:ring-0 md:text-4xl"
        placeholder="Untitled"
      />
      <ContentEditor
        defaultValue={selected.content}
        onUpdate={setDraftContent}
        onUploadFile={uploadWithToast}
        placeholder={t(($) => $.notes_page.content_placeholder)}
        className="mt-6 min-h-[55vh] px-0 py-2"
        debounceMs={150}
        disableMentions
        showBubbleMenu
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
  const selectedFromList = findNote(list.pages, pageId);
  const selectedId = pageId ?? selectedFromList?.id;
  const detailQuery = useQuery(noteDetailOptions(wsId, selectedId ?? ""));
  const selected = detailQuery.data?.id ? detailQuery.data : selectedFromList;
  const tree = useMemo(() => buildNoteTree(list.pages), [list.pages]);
  const createPage = useCreateNotePage();
  const deletePage = useDeleteNotePage();
  const restorePage = useRestoreNotePage();
  const [sharePage, setSharePage] = useState<NotePage | null>(null);
  const [showTrash, setShowTrash] = useState(false);

  const openPage = (id: string) => {
    setShowTrash(false);
    navigation.push(paths.noteDetail(id));
  };

  const handleCreate = async (parentId?: string | null) => {
    try {
      const page = await createPage.mutateAsync({ parent_id: parentId ?? null, title: "Untitled" });
      setShowTrash(false);
      navigation.push(paths.noteDetail(page.id));
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.create_failed));
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

  const handleRestore = async (page: NotePage) => {
    try {
      const restored = await restorePage.mutateAsync(page.id);
      toast.success(t(($) => $.notes_page.restored));
      setShowTrash(false);
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
        <Button size="sm" onClick={() => handleCreate(null)} disabled={createPage.isPending}>
          <Plus className="size-4" />
          {t(($) => $.notes_page.new_page)}
        </Button>
      </PageHeader>
      <div className="flex min-h-0 flex-1">
        <aside className="flex w-72 shrink-0 flex-col border-r bg-muted/20">
          <div className="flex items-center justify-between px-3 py-2 text-xs font-medium text-muted-foreground">
            <span>{t(($) => $.notes_page.directory)}</span>
            <Button size="icon" variant="ghost" className="size-7" onClick={() => {
              setShowTrash(false);
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
            ) : tree.length === 0 ? (
              <button type="button" className="w-full rounded-lg border border-dashed p-4 text-left text-sm text-muted-foreground hover:bg-muted/50" onClick={() => handleCreate(null)}>
                {t(($) => $.notes_page.empty_create_hint)}
              </button>
            ) : (
              <div className="space-y-0.5">
                {tree.map((node) => (
                  <NoteTreeRow key={node.id} node={node} depth={0} activeId={selected?.id} onOpen={openPage} onCreateChild={(parentId) => handleCreate(parentId)} onShare={setSharePage} onDelete={handleDelete} />
                ))}
              </div>
            )}
          </div>
          <div className="border-t p-2">
            <Button variant={showTrash ? "secondary" : "ghost"} size="sm" className="w-full justify-start" onClick={() => setShowTrash((v) => !v)}>
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
              currentUserId={currentUserId}
              onOpenShare={() => setSharePage(selected)}
            />
          )}
        </main>
      </div>
      <ShareDialog page={sharePage} members={members} open={!!sharePage} onOpenChange={(open) => {
        if (!open) setSharePage(null);
      }} />
    </div>
  );
}
