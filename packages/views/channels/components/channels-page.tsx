"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  FileText,
  MessageCircle,
  MoreHorizontal,
  Paperclip,
  PieChart,
  Plus,
  Search,
  Send,
  Share2,
  Smartphone,
  Trash2,
  X,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  activeChannelTasksKeys,
  activeChannelTasksOptions,
  channelKeys,
  channelsOptions,
  channelMessagesOptions,
  channelMembersOptions,
  channelProjectOptions,
  useSetChannelProject,
  useAddChannelMembers,
  useCreateChannel,
  useDeleteChannel,
  useMarkChannelRead,
  useRemoveChannelMember,
  useSendChannelMessage,
  useSetChannelTyping,
} from "@multica/core/channels";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { useFileUpload, type UploadResult } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useWSEvent } from "@multica/core/realtime";
import { toast } from "sonner";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import type {
  Channel,
  ChannelActiveTask,
  ChannelMemberBrief,
  ChannelTypingPayload,
} from "@multica/core/types";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { UnicodeSpinner } from "@multica/ui/components/common/unicode-spinner";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { cn } from "@multica/ui/lib/utils";
import { ContentEditor, type ContentEditorRef } from "../../editor";
import { useNavigation } from "../../navigation";
import { agentColor } from "../../common/agent-color";
import { ProjectPickerButton } from "../../common/project-picker-button";
import { initialsOf } from "../../common/initials";
import { useT, useTimeAgo } from "../../i18n";
import { ChannelMessageBubble } from "./channel-message-bubble";
import { ChannelFilesPanel } from "./channel-files-panel";
import { ChannelStatsPanel } from "./channel-stats-panel";
import { ChannelGroupAvatar } from "./channel-group-avatar";

interface TypingActor {
  key: string;
  channelId: string;
  actorName: string;
  actorType: ChannelTypingPayload["actor_type"];
  expiresAt: number;
}

// Overlapping avatar stack for the channel roster (agents tinted by identity
// color, humans as initials). The whole stack is the trigger that opens member
// management — matching the Figma header where the roster doubles as the
// add/remove entry point.
function MemberStack({
  members,
  max = 4,
  size = 28,
  emptyHint = true,
}: {
  members: ChannelMemberBrief[];
  max?: number;
  size?: number;
  emptyHint?: boolean;
}) {
  const { t } = useT("channels");
  if (members.length === 0) {
    return emptyHint ? (
      <span className="text-xs text-muted-foreground">{t(($) => $.members.empty)}</span>
    ) : null;
  }
  const visible = members.slice(0, max);
  const overflow = members.length - visible.length;
  const overlap = Math.round(size * 0.3);
  return (
    <span className="inline-flex items-center">
      {visible.map((m, i) => (
        <span
          key={`${m.member_type}:${m.member_id}`}
          style={{ marginLeft: i === 0 ? 0 : -overlap }}
          className="inline-flex rounded-full ring-2 ring-background"
        >
          <ActorAvatar
            name={m.name || (m.member_type === "agent" ? "Agent" : "Member")}
            initials={initialsOf(m.name || "?")}
            isAgent={m.member_type === "agent"}
            size={size}
            tint={m.member_type === "agent" ? agentColor(m.member_id) : undefined}
          />
        </span>
      ))}
      {overflow > 0 && (
        <span
          style={{ marginLeft: -overlap, width: size, height: size, fontSize: Math.max(9, Math.round(size * 0.36)) }}
          className="inline-flex items-center justify-center rounded-full bg-muted font-medium text-muted-foreground ring-2 ring-background"
        >
          +{overflow}
        </span>
      )}
    </span>
  );
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useT("channels");
  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="max-w-md rounded-3xl border bg-card p-8 text-center shadow-sm">
        <div className="mx-auto flex size-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
          <MessageCircle className="size-6" />
        </div>
        <h2 className="mt-5 text-xl font-semibold">{t(($) => $.empty_state.title)}</h2>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          {t(($) => $.empty_state.description)}
        </p>
        <Button className="mt-5" onClick={onCreate}>
          <Plus className="size-4" /> {t(($) => $.empty_state.cta)}
        </Button>
      </div>
    </div>
  );
}

function TypingIndicator({ actors }: { actors: TypingActor[] }) {
  const { t } = useT("channels");
  if (actors.length === 0) return null;
  const names = actors.map((a) => a.actorName);
  const label =
    names.length === 1
      ? t(($) => $.typing.single, { name: names[0]! })
      : names.length === 2
        ? t(($) => $.typing.pair, { a: names[0]!, b: names[1]! })
        : t(($) => $.typing.overflow, { a: names[0]!, b: names[1]!, count: names.length });
  return (
    <div className="flex items-center gap-2 px-2 py-1.5 text-xs text-muted-foreground" aria-live="polite">
      <span className="flex h-7 items-center gap-1 rounded-full border bg-card px-3 shadow-sm">
        <span>{label}</span>
        <span className="ml-1 flex items-end gap-0.5" aria-hidden="true">
          <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground/70 [animation-delay:-0.24s]" />
          <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground/70 [animation-delay:-0.12s]" />
          <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground/70" />
        </span>
      </span>
    </div>
  );
}

// Query-authoritative, per-agent working indicator. Unlike the transient typing
// broadcast, this reflects the agent's recoverable task lifecycle stage. Unknown
// statuses downgrade to the generic "thinking" label (enum-drift rule) rather
// than rendering nothing.
function AgentWorkingIndicator({ tasks }: { tasks: ChannelActiveTask[] }) {
  const { t } = useT("channels");
  if (tasks.length === 0) return null;
  const labelFor = (status: string): string => {
    switch (status) {
      case "queued":
        return t(($) => $.agent_status.queued);
      case "dispatched":
        return t(($) => $.agent_status.dispatched);
      case "waiting_local_directory":
        return t(($) => $.agent_status.waiting_local_directory);
      case "running":
        return t(($) => $.agent_status.running);
      default:
        return t(($) => $.agent_status.running);
    }
  };
  return (
    <div className="flex flex-col gap-1 px-2 py-1.5" aria-live="polite">
      {tasks.map((task) => (
        <div
          key={task.agent_id}
          className="flex items-center gap-2 text-xs text-muted-foreground"
        >
          <span className="flex h-7 items-center gap-1.5 rounded-full border bg-card px-3 shadow-sm">
            <UnicodeSpinner className="text-muted-foreground/70" />
            <span className="font-medium text-foreground">{task.agent_name}</span>
            <span>{labelFor(task.status)}</span>
          </span>
        </div>
      ))}
    </div>
  );
}

export function ChannelsPage() {
  const { t } = useT("channels");
  const timeAgo = useTimeAgo();
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const { searchParams, replace, getShareableUrl } = useNavigation();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? null);
  const { mutate: markChannelRead } = useMarkChannelRead();
  // Initialize from ?channel= so shared deep links open the right channel.
  const [activeId, setActiveId] = useState<string | null>(() => searchParams.get("channel"));
  const [search, setSearch] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<Channel | null>(null);
  const editorRef = useRef<ContentEditorRef>(null);
  const [draftEmpty, setDraftEmpty] = useState(true);
  const [typingActors, setTypingActors] = useState<Record<string, TypingActor>>({});
  const [newName, setNewName] = useState("");
  const [newLarkChatId, setNewLarkChatId] = useState("");
  // Multi-select invite: keys are `${type}:${id}` so users and agents share one set.
  const [selectedInvites, setSelectedInvites] = useState<Set<string>>(new Set());
  const [memberTab, setMemberTab] = useState<"invite" | "members">("invite");
  const [memberQuery, setMemberQuery] = useState("");
  const bottomRef = useRef<HTMLDivElement | null>(null);
  const typingStartedRef = useRef(false);
  const typingStopTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const typingPulseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const { data: channels = [], isLoading } = useQuery(channelsOptions(wsId));
  const { data: workspaceMembers = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const active = useMemo(
    () => channels.find((c) => c.id === activeId) ?? channels[0] ?? null,
    [channels, activeId],
  );
  const { data: messages = [] } = useQuery(channelMessagesOptions(active?.id ?? ""));
  const { data: channelMembers = [] } = useQuery(channelMembersOptions(active?.id ?? ""));
  const { data: channelProjectId = "" } = useQuery(channelProjectOptions(wsId, active?.id ?? ""));
  const { data: activeTasks = [] } = useQuery(activeChannelTasksOptions(active?.id ?? ""));
  const setChannelProject = useSetChannelProject(wsId, active?.id ?? "");
  const createChannel = useCreateChannel();
  const deleteChannel = useDeleteChannel();
  const sendMessage = useSendChannelMessage();
  const setTyping = useSetChannelTyping();
  const addMembers = useAddChannelMembers();
  const removeMember = useRemoveChannelMember();
  const { uploadWithToast } = useFileUpload(api);
  // Maps the URL the editor wrote into the markdown body → attachment row id,
  // so on send we bind only attachments still referenced in the content.
  // Mirrors the chat-input flow. Cleared after every successful send.
  const uploadMapRef = useRef<Map<string, string>>(new Map());
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const memberIds = useMemo(
    () => new Set(channelMembers.filter((m) => m.member_type === "user").map((m) => m.member_id)),
    [channelMembers],
  );
  const agentIds = useMemo(
    () => new Set(channelMembers.filter((m) => m.member_type === "agent").map((m) => m.member_id)),
    [channelMembers],
  );
  const availableMembers = workspaceMembers.filter((m) => !memberIds.has(m.user_id));
  const availableAgents = agents.filter((a) => !agentIds.has(a.id) && !a.archived_at);
  // Flat, searchable candidate list (users + agents) for the invite tab.
  const inviteCandidates = useMemo(() => {
    const q = memberQuery.trim().toLowerCase();
    const list = [
      ...availableMembers.map((m) => ({
        key: `user:${m.user_id}`,
        type: "user" as const,
        id: m.user_id,
        name: m.name || m.email,
      })),
      ...availableAgents.map((a) => ({
        key: `agent:${a.id}`,
        type: "agent" as const,
        id: a.id,
        name: a.name,
      })),
    ];
    return q ? list.filter((c) => c.name.toLowerCase().includes(q)) : list;
  }, [availableMembers, availableAgents, memberQuery]);
  const filteredMembers = useMemo(() => {
    const q = memberQuery.trim().toLowerCase();
    return q
      ? channelMembers.filter((m) => (m.name || "").toLowerCase().includes(q))
      : channelMembers;
  }, [channelMembers, memberQuery]);
  // Scope the composer's @ picker to this channel's members only.
  const channelMemberIds = useMemo(
    () => new Set(channelMembers.map((m) => m.member_id)),
    [channelMembers],
  );
  // Agents surface their lifecycle stage via the query-driven working indicator
  // (which shows "Thinking"/"Starting up" rather than a premature "typing"), so
  // filter agent actors out of the typing render to avoid showing them twice.
  // Human/member typing keeps using the transient broadcast.
  const activeTypingActors = useMemo(
    () =>
      Object.values(typingActors).filter(
        (a) => a.channelId === active?.id && a.actorType !== "agent",
      ),
    [active?.id, typingActors],
  );
  const rosterSummary = useMemo(
    () => channelMembers.map((m) => m.name || (m.member_type === "agent" ? "Agent" : "成员")).join("，"),
    [channelMembers],
  );
  const filteredChannels = useMemo(() => {
    const q = search.trim().toLowerCase();
    return q ? channels.filter((c) => c.name.toLowerCase().includes(q)) : channels;
  }, [channels, search]);

  useEffect(() => {
    if (!activeId && channels[0]) setActiveId(channels[0].id);
  }, [activeId, channels]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: "end" });
  }, [messages.length, active?.id, activeTypingActors.length, activeTasks.length]);

  // Clear the unread badge when a channel becomes active (select / deep link /
  // auto-select). `markChannelRead` (mutate) is referentially stable.
  useEffect(() => {
    if (active?.id) markChannelRead(active.id);
  }, [active?.id, markChannelRead]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now();
      setTypingActors((current) => {
        const next = Object.fromEntries(Object.entries(current).filter(([, actor]) => actor.expiresAt > now));
        return Object.keys(next).length === Object.keys(current).length ? current : next;
      });
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    typingStartedRef.current = false;
    if (typingStopTimerRef.current) window.clearTimeout(typingStopTimerRef.current);
    if (typingPulseTimerRef.current) window.clearTimeout(typingPulseTimerRef.current);
  }, [active?.id]);

  // New messages (from others / agents) refresh the list (unread + preview)
  // and the open thread. Keep the active channel marked read while viewing it.
  useWSEvent("channel:message", (payload) => {
    const e = payload as { channel_id?: string };
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    if (e.channel_id) {
      qc.invalidateQueries({ queryKey: channelKeys.messages(e.channel_id) });
      qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(e.channel_id) });
      if (e.channel_id === active?.id) markChannelRead(active.id);
    }
  });

  // Another client deleted a channel — drop it from the list. If it was the
  // open one, `active` falls back to the first remaining channel via the memo.
  useWSEvent("channel:deleted", (payload) => {
    const e = payload as { id?: string };
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    if (e.id && e.id === activeId) setActiveId(null);
  });

  useWSEvent("channel:typing", (payload) => {
    const event = payload as ChannelTypingPayload;
    if (!event.channel_id || event.channel_id !== active?.id) return;
    // A typing pulse from an agent often coincides with a task starting or
    // ending — refresh the authoritative lifecycle view promptly.
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(event.channel_id) });
    const actorKey = `${event.actor_type}:${event.actor_id ?? event.actor_name}`;
    if (event.actor_type === "user" && event.actor_id && event.actor_id === currentUserId) return;
    setTypingActors((current) => {
      if (!event.is_typing) {
        const next = { ...current };
        delete next[actorKey];
        return next;
      }
      return {
        ...current,
        [actorKey]: {
          key: actorKey,
          channelId: event.channel_id,
          actorName: event.actor_name,
          actorType: event.actor_type,
          expiresAt: Date.now() + (event.expires_in_ms ?? 5000),
        },
      };
    });
  });

  const handleCreate = () => {
    const name = newName.trim() || "general";
    createChannel.mutate(
      { name, lark_chat_id: newLarkChatId.trim() || undefined },
      {
        onSuccess: (channel: Channel) => {
          selectChannel(channel.id);
          setNewName("");
          setNewLarkChatId("");
          setCreateOpen(false);
        },
      },
    );
  };

  // Select a channel and reflect it in the URL so the address is shareable.
  const selectChannel = (id: string) => {
    setActiveId(id);
    replace(`${wsPaths.channels()}?channel=${id}`);
  };

  const handleDelete = () => {
    const target = deleteTarget;
    if (!target) return;
    deleteChannel.mutate(target.id, {
      onSuccess: () => {
        toast.success(t(($) => $.delete_dialog.toast_success));
        // If the open channel was the one removed, drop the selection so the
        // `active` memo falls back to the first remaining channel.
        if (target.id === activeId) setActiveId(null);
        setDeleteTarget(null);
      },
      onError: () => toast.error(t(($) => $.delete_dialog.toast_failed)),
    });
  };

  const handleShare = async () => {
    if (!active) return;
    const url = getShareableUrl(`${wsPaths.channels()}?channel=${active.id}`);
    try {
      await navigator.clipboard.writeText(url);
      toast.success(t(($) => $.share.copied));
    } catch {
      toast.error(t(($) => $.share.copy_failed));
    }
  };

  const publishTyping = (isTyping: boolean) => {
    if (!active) return;
    setTyping.mutate({ channelId: active.id, isTyping });
  };

  const scheduleTypingStop = () => {
    if (typingStopTimerRef.current) clearTimeout(typingStopTimerRef.current);
    typingStopTimerRef.current = setTimeout(() => {
      if (typingStartedRef.current) {
        typingStartedRef.current = false;
        publishTyping(false);
      }
    }, 1200);
  };

  const scheduleTypingPulse = () => {
    if (typingPulseTimerRef.current) clearTimeout(typingPulseTimerRef.current);
    typingPulseTimerRef.current = setTimeout(() => {
      if (typingStartedRef.current) {
        publishTyping(true);
        scheduleTypingPulse();
      }
    }, 3500);
  };

  const handleEditorUpdate = (value: string) => {
    setDraftEmpty(!value.trim());
    if (!active) return;
    if (value.trim()) {
      if (!typingStartedRef.current) {
        typingStartedRef.current = true;
        publishTyping(true);
        scheduleTypingPulse();
      }
      scheduleTypingStop();
      return;
    }
    if (typingStartedRef.current) {
      typingStartedRef.current = false;
      publishTyping(false);
    }
  };

  // Upload a file against the active channel and remember its URL → id so the
  // attachment binds to the message on send. The editor inserts the markdown
  // link itself; we only track the mapping.
  const handleUpload = useCallback(
    async (file: File): Promise<UploadResult | null> => {
      if (!active) return null;
      const result = await uploadWithToast(file, { channelId: active.id });
      if (result) {
        uploadMapRef.current.set(result.markdownLink || result.link, result.id);
      }
      return result;
    },
    [active, uploadWithToast],
  );

  const handlePickFiles = (files: FileList | null) => {
    if (!files) return;
    for (const file of Array.from(files)) {
      editorRef.current?.uploadFile(file);
    }
  };

  const handleSend = () => {
    const content = editorRef.current?.getMarkdown()?.trim();
    if (!content || !active) return;
    // Block while an upload is still in flight — otherwise the attachment id
    // isn't in uploadMapRef yet and the file would only bind to the channel,
    // not the message.
    if (editorRef.current?.hasActiveUploads()) return;
    // Only bind attachments still referenced in the body — edits that removed
    // the markdown link also drop the binding.
    const attachmentIds: string[] = [];
    for (const [url, id] of uploadMapRef.current) {
      if (content.includes(url)) attachmentIds.push(id);
    }
    if (typingStartedRef.current) {
      typingStartedRef.current = false;
      publishTyping(false);
    }
    sendMessage.mutate(
      { channelId: active.id, content, attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined },
      {
        onSuccess: () => {
          editorRef.current?.clearContent();
          uploadMapRef.current.clear();
          setDraftEmpty(true);
        },
      },
    );
  };

  const toggleInvite = (key: string) => {
    setSelectedInvites((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const inviteSelected = () => {
    if (!active || selectedInvites.size === 0) return;
    const members = Array.from(selectedInvites).map((key) => {
      const sep = key.indexOf(":");
      return {
        member_type: key.slice(0, sep) as "user" | "agent",
        member_id: key.slice(sep + 1),
      };
    });
    addMembers.mutate(
      { channelId: active.id, members },
      { onSuccess: () => setSelectedInvites(new Set()) },
    );
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="grid min-h-0 flex-1 grid-cols-[280px_1fr] gap-0 bg-background">
        <aside className="flex min-h-0 flex-col border-r bg-muted/20">
          <div className="flex items-center justify-between px-4 pb-1 pt-4">
            <h2 className="text-lg font-semibold">{t(($) => $.sidebar.heading)}</h2>
            <Popover open={createOpen} onOpenChange={setCreateOpen}>
              <PopoverTrigger
                className="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent"
                aria-label={t(($) => $.sidebar.create_aria)}
              >
                <Plus className="size-4" />
              </PopoverTrigger>
              <PopoverContent align="end" className="w-72 space-y-2">
                <Input
                  placeholder={t(($) => $.sidebar.name_placeholder)}
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") handleCreate();
                  }}
                />
                <Input
                  placeholder={t(($) => $.sidebar.lark_placeholder)}
                  value={newLarkChatId}
                  onChange={(e) => setNewLarkChatId(e.target.value)}
                />
                <Button className="w-full" onClick={handleCreate} disabled={createChannel.isPending}>
                  {t(($) => $.sidebar.create_aria)}
                </Button>
              </PopoverContent>
            </Popover>
          </div>
          <div className="px-3 pb-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t(($) => $.sidebar.search)}
                className="h-9 pl-8"
              />
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
            <p className="px-2 pb-1 pt-1.5 text-xs font-medium text-muted-foreground">
              {t(($) => $.sidebar.groups)}
            </p>
            {isLoading ? (
              <div className="space-y-2 p-2">
                <Skeleton className="h-12" />
                <Skeleton className="h-12" />
              </div>
            ) : channels.length === 0 ? (
              <div className="p-3 text-sm text-muted-foreground">{t(($) => $.sidebar.empty)}</div>
            ) : (
              filteredChannels.map((channel) => {
                const unread = channel.unread_count ?? 0;
                const last = channel.last_message;
                const preview = last ? `${last.author_name}: ${last.content}`.replace(/\s+/g, " ") : "";
                return (
                  <div
                    key={channel.id}
                    className={cn(
                      "group/row relative mb-0.5 rounded-lg transition-colors",
                      active?.id === channel.id ? "bg-primary/[0.08]" : "hover:bg-accent",
                    )}
                  >
                    <button
                      type="button"
                      onClick={() => selectChannel(channel.id)}
                      className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 pr-7 text-left"
                    >
                      <ChannelGroupAvatar members={channel.members ?? []} size={40} />
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center justify-between gap-2">
                          <span className="flex min-w-0 items-center gap-1 truncate text-sm font-medium text-foreground">
                            <span className="truncate">{channel.name}</span>
                            {channel.lark_chat_id && (
                              <Smartphone className="size-3 shrink-0 text-emerald-600" />
                            )}
                          </span>
                          {last && (
                            <span className="shrink-0 text-[11px] text-muted-foreground">
                              {timeAgo(last.created_at)}
                            </span>
                          )}
                        </div>
                        <div className="mt-0.5 flex items-center justify-between gap-2">
                          <span className="truncate text-xs text-muted-foreground">{preview}</span>
                          {unread > 0 && (
                            <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium text-primary-foreground">
                              {unread > 99 ? "99+" : unread}
                            </span>
                          )}
                        </div>
                      </div>
                    </button>
                    <DropdownMenu>
                      <DropdownMenuTrigger
                        render={
                          <button
                            type="button"
                            aria-label={t(($) => $.sidebar.menu_aria)}
                            className="absolute right-1 top-1.5 flex size-6 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground focus-visible:opacity-100 group-hover/row:opacity-100 data-[popup-open]:opacity-100"
                          >
                            <MoreHorizontal className="size-4" />
                          </button>
                        }
                      />
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem variant="destructive" onClick={() => setDeleteTarget(channel)}>
                          <Trash2 className="size-4" /> {t(($) => $.sidebar.delete)}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                );
              })
            )}
          </div>
        </aside>

        <main className="flex min-h-0 min-w-0 flex-col">
          {!active ? (
            <EmptyState onCreate={handleCreate} />
          ) : (
            <>
              <header className="flex items-center justify-between gap-3 border-b px-5 py-2.5">
                <div className="flex min-w-0 items-center gap-3">
                  <ChannelGroupAvatar members={channelMembers} size={40} />
                  <div className="min-w-0">
                    <div className="flex items-center gap-2 font-semibold">
                      <span className="truncate">{active.name}</span>
                      {active.lark_chat_id && (
                        <Badge variant="secondary" className="shrink-0">
                          {t(($) => $.header.feishu)}
                        </Badge>
                      )}
                    </div>
                    <p className="mt-0.5 truncate text-xs text-muted-foreground">
                      {t(($) => $.header.running)}
                      {rosterSummary ? ` · ${rosterSummary}` : ""}
                    </p>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-3">
                  <Popover>
                    <PopoverTrigger
                      className="flex items-center gap-1.5 rounded-full p-0.5 transition-colors hover:bg-accent"
                      aria-label={t(($) => $.header.manage_members_aria)}
                    >
                      <MemberStack members={channelMembers} />
                      <span className="flex size-7 items-center justify-center rounded-full border border-dashed text-muted-foreground">
                        <Plus className="size-3.5" />
                      </span>
                    </PopoverTrigger>
                    <PopoverContent align="end" className="w-80 p-0">
                      <div className="flex border-b">
                        <button
                          type="button"
                          onClick={() => setMemberTab("invite")}
                          className={cn(
                            "flex-1 border-b-2 px-3 py-2 text-sm font-medium transition-colors",
                            memberTab === "invite"
                              ? "border-primary text-foreground"
                              : "border-transparent text-muted-foreground hover:text-foreground",
                          )}
                        >
                          {t(($) => $.members.tab_invite)}
                        </button>
                        <button
                          type="button"
                          onClick={() => setMemberTab("members")}
                          className={cn(
                            "flex-1 border-b-2 px-3 py-2 text-sm font-medium transition-colors",
                            memberTab === "members"
                              ? "border-primary text-foreground"
                              : "border-transparent text-muted-foreground hover:text-foreground",
                          )}
                        >
                          {t(($) => $.members.tab_members)} · {channelMembers.length}
                        </button>
                      </div>
                      <div className="p-2">
                        <div className="relative">
                          <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                          <Input
                            value={memberQuery}
                            onChange={(e) => setMemberQuery(e.target.value)}
                            placeholder={t(($) => $.members.search)}
                            className="h-8 pl-7"
                          />
                        </div>
                      </div>
                      {memberTab === "invite" ? (
                        <>
                          <div className="max-h-64 overflow-y-auto px-1.5 pb-1.5">
                            {inviteCandidates.length === 0 ? (
                              <p className="px-2 py-6 text-center text-xs text-muted-foreground">
                                {memberQuery
                                  ? t(($) => $.members.no_results)
                                  : t(($) => $.members.no_candidates)}
                              </p>
                            ) : (
                              inviteCandidates.map((c) => (
                                <label
                                  key={c.key}
                                  className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 hover:bg-accent"
                                >
                                  <Checkbox
                                    checked={selectedInvites.has(c.key)}
                                    onCheckedChange={() => toggleInvite(c.key)}
                                  />
                                  <ActorAvatar
                                    name={c.name}
                                    initials={initialsOf(c.name || "?")}
                                    isAgent={c.type === "agent"}
                                    size={26}
                                    tint={c.type === "agent" ? agentColor(c.id) : undefined}
                                  />
                                  <span className="min-w-0 flex-1 truncate text-sm">{c.name}</span>
                                </label>
                              ))
                            )}
                          </div>
                          {selectedInvites.size > 0 && (
                            <div className="border-t p-2">
                              <Button
                                size="sm"
                                className="w-full"
                                onClick={inviteSelected}
                                disabled={addMembers.isPending}
                              >
                                {t(($) => $.members.invite_count, { count: selectedInvites.size })}
                              </Button>
                            </div>
                          )}
                        </>
                      ) : (
                        <div className="max-h-72 overflow-y-auto px-1.5 pb-2">
                          {filteredMembers.length === 0 ? (
                            <p className="px-2 py-6 text-center text-xs text-muted-foreground">
                              {channelMembers.length === 0
                                ? t(($) => $.members.empty)
                                : t(($) => $.members.no_results)}
                            </p>
                          ) : (
                            filteredMembers.map((m) => {
                              const isAgent = m.member_type === "agent";
                              const name =
                                m.name ||
                                (isAgent
                                  ? t(($) => $.message.agent_badge)
                                  : t(($) => $.members.title));
                              return (
                                <div
                                  key={`${m.member_type}:${m.member_id}`}
                                  className="group flex items-center gap-2.5 rounded-md px-2 py-1.5 hover:bg-accent"
                                >
                                  <ActorAvatar
                                    name={name}
                                    initials={initialsOf(m.name || "?")}
                                    isAgent={isAgent}
                                    size={26}
                                    tint={isAgent ? agentColor(m.member_id) : undefined}
                                  />
                                  <span className="min-w-0 flex-1 truncate text-sm">{name}</span>
                                  <button
                                    type="button"
                                    onClick={() =>
                                      removeMember.mutate({
                                        channelId: active.id,
                                        memberType: m.member_type,
                                        memberId: m.member_id,
                                      })
                                    }
                                    aria-label={t(($) => $.members.remove_aria)}
                                    className="rounded p-1 text-muted-foreground opacity-0 transition hover:text-destructive group-hover:opacity-100"
                                  >
                                    <X className="size-3.5" />
                                  </button>
                                </div>
                              );
                            })
                          )}
                        </div>
                      )}
                    </PopoverContent>
                  </Popover>
                  <div className="flex items-center gap-1 text-muted-foreground">
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8"
                      aria-label={t(($) => $.header.share_aria)}
                      onClick={handleShare}
                    >
                      <Share2 className="size-4" />
                    </Button>
                    <Popover>
                      <PopoverTrigger
                        className="flex size-8 items-center justify-center rounded-md transition-colors hover:bg-accent"
                        aria-label={t(($) => $.header.stats_aria)}
                      >
                        <PieChart className="size-4" />
                      </PopoverTrigger>
                      <PopoverContent align="end" className="w-72">
                        <p className="mb-3 text-sm font-medium">{t(($) => $.stats.title)}</p>
                        <ChannelStatsPanel channelId={active.id} />
                      </PopoverContent>
                    </Popover>
                    <Popover>
                      <PopoverTrigger
                        className="flex size-8 items-center justify-center rounded-md transition-colors hover:bg-accent"
                        aria-label={t(($) => $.header.files_aria)}
                      >
                        <FileText className="size-4" />
                      </PopoverTrigger>
                      <PopoverContent align="end" className="w-80">
                        <p className="mb-3 text-sm font-medium">{t(($) => $.files.title)}</p>
                        <ChannelFilesPanel channelId={active.id} />
                      </PopoverContent>
                    </Popover>
                  </div>
                </div>
              </header>

              <div className="min-h-0 min-w-0 flex-1 overflow-y-auto px-4 py-4">
                {messages.length === 0 ? (
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                    {t(($) => $.thread.empty)}
                  </div>
                ) : (
                  messages.map((message) => (
                    <ChannelMessageBubble
                      key={message.id}
                      message={message}
                      currentUserId={currentUserId}
                      ownName={currentUserName ?? undefined}
                    />
                  ))
                )}
                <AgentWorkingIndicator tasks={activeTasks} />
                <TypingIndicator actors={activeTypingActors} />
                <div ref={bottomRef} />
              </div>

              <div className="px-4 pb-4">
                <div className="rounded-xl border bg-card shadow-sm">
                  <div className="max-h-40 min-h-16 overflow-y-auto px-4 pt-3">
                    <ContentEditor
                      key={active.id}
                      ref={editorRef}
                      placeholder={t(($) => $.composer.placeholder)}
                      onUpdate={handleEditorUpdate}
                      onSubmit={handleSend}
                      onUploadFile={handleUpload}
                      submitOnEnter
                      showBubbleMenu={false}
                      mentionAllowedActorIds={channelMemberIds}
                    />
                  </div>
                  <div className="flex items-center justify-between px-2 pb-2">
                    <div className="flex items-center gap-0.5 text-muted-foreground">
                      <input
                        ref={fileInputRef}
                        type="file"
                        multiple
                        className="hidden"
                        onChange={(e) => {
                          handlePickFiles(e.target.files);
                          e.target.value = "";
                        }}
                      />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8"
                        aria-label={t(($) => $.composer.attach_aria)}
                        onClick={() => fileInputRef.current?.click()}
                      >
                        <Paperclip className="size-4" />
                      </Button>
                      <ProjectPickerButton
                        wsId={wsId}
                        value={channelProjectId || null}
                        onChange={(projectId) => setChannelProject.mutate(projectId)}
                        label={t(($) => $.composer.project_label)}
                        noneLabel={t(($) => $.composer.project_none)}
                        tooltip={t(($) => $.composer.project_tooltip)}
                      />
                    </div>
                    <Button onClick={handleSend} disabled={draftEmpty || sendMessage.isPending} size="sm">
                      <Send className="size-4" /> {t(($) => $.composer.send)}
                    </Button>
                  </div>
                </div>
              </div>
            </>
          )}
        </main>
      </div>

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.delete_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.delete_dialog.description, { name: deleteTarget?.name ?? "" })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.delete_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={handleDelete}
              disabled={deleteChannel.isPending}
            >
              {t(($) => $.delete_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
