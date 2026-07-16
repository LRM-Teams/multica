"use client";

import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { inboxListOptions, useMentionPopupStore } from "@multica/core/inbox";
import { useMarkInboxRead } from "@multica/core/inbox";
import { useWorkspacePaths } from "@multica/core/paths";
import type { InboxItem } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { X } from "lucide-react";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";

// A channel @-mention (not an issue mention) that the popup can act on.
function isActionableChannelMention(item: InboxItem): boolean {
  return (
    item.type === "mentioned" &&
    !item.read &&
    !item.archived &&
    item.issue_id == null &&
    !!item.details?.channel_id
  );
}

/**
 * Global quick-reply popup for @-mentions. Mounted once (dashboard layout) so it
 * can surface on any page. Watches the inbox for a newly-arrived unread channel
 * mention and pops a card that "flies out" of the sidebar inbox icon, offering
 * four one-click actions. Pre-existing unread mentions at mount are NOT popped —
 * only mentions that arrive while the app is open.
 */
export function MentionQuickReplyPopup() {
  const wsId = useWorkspaceId();
  const { data: items = [] } = useQuery(inboxListOptions(wsId));
  const [queue, setQueue] = useState<InboxItem[]>([]);
  const seenRef = useRef<Set<string> | null>(null);
  const triggerBounce = useMentionPopupStore((s) => s.triggerBounce);

  useEffect(() => {
    const mentions = items.filter(isActionableChannelMention);
    // First data load: baseline everything currently present so we only pop
    // mentions that arrive AFTER the app is open.
    if (seenRef.current === null) {
      seenRef.current = new Set(items.map((i) => i.id));
      return;
    }
    const fresh = mentions.filter((m) => !seenRef.current!.has(m.id));
    if (fresh.length === 0) return;
    for (const m of fresh) seenRef.current!.add(m.id);
    setQueue((q) => [...q, ...fresh]);
    triggerBounce();
  }, [items, triggerBounce]);

  const current = queue[0] ?? null;
  const dismiss = () => setQueue((q) => q.slice(1));

  return (
    <AnimatePresence mode="wait">
      {current && (
        <MentionQuickReplyCard key={current.id} item={current} onDismiss={dismiss} />
      )}
    </AnimatePresence>
  );
}

function originOffset(rect: { x: number; y: number; width: number; height: number } | null) {
  if (!rect || typeof window === "undefined") return { x: 0, y: 0 };
  const iconCx = rect.x + rect.width / 2;
  const iconCy = rect.y + rect.height / 2;
  return { x: iconCx - window.innerWidth / 2, y: iconCy - window.innerHeight / 2 };
}

export function MentionQuickReplyCard({
  item,
  onDismiss,
}: {
  item: InboxItem;
  onDismiss: () => void;
}) {
  const { t } = useT("inbox");
  const { push } = useNavigation();
  const paths = useWorkspacePaths();
  const markRead = useMarkInboxRead();
  const iconRect = useMentionPopupStore((s) => s.iconRect);
  const [busy, setBusy] = useState(false);

  const d = item.details ?? {};
  const channelId = d.channel_id ?? "";
  const channelName = d.channel_name ?? "";
  const actorName = d.actor_name ?? "";
  const gmId = d.group_manager_agent_id ?? "";
  const gmName = d.group_manager_name ?? "贝克汉姆";
  const origin = originOffset(iconRect);

  const send = async (content: string) => {
    if (!channelId) return;
    setBusy(true);
    try {
      await api.sendChannelMessage(channelId, { content });
      markRead.mutate(item.id);
      onDismiss();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.mention_popup.send_failed));
    } finally {
      setBusy(false);
    }
  };

  const onContinue = () => {
    const mention =
      item.actor_id && (item.actor_type === "agent" || item.actor_type === "member")
        ? ` [@${actorName || "?"}](mention://${item.actor_type}/${item.actor_id})`
        : "";
    void send(`可以，继续吧${mention}`);
  };

  const onDelegate = () => {
    if (!gmId) {
      toast.error(t(($) => $.mention_popup.no_manager));
      return;
    }
    void send(
      `[@${gmName}](mention://agent/${gmId}) 我把这条全权交给你处理，按你的判断推进到达标，期间无需再回来问我。`,
    );
  };

  const onManual = () => {
    markRead.mutate(item.id);
    onDismiss();
    if (channelId) push(paths.channelDetail(channelId));
  };

  return (
    <motion.div
      className="fixed inset-0 z-[60] flex items-center justify-center p-4"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
    >
      {/* Backdrop — click = 稍后处理 (shrink back into inbox, stays unread). */}
      <motion.div
        className="absolute inset-0 bg-black/30"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        onClick={onDismiss}
      />
      <motion.div
        className="relative w-full max-w-sm overflow-hidden rounded-xl border bg-background shadow-2xl"
        initial={{ opacity: 0, scale: 0.2, x: origin.x, y: origin.y }}
        animate={{ opacity: 1, scale: 1, x: 0, y: 0 }}
        exit={{ opacity: 0, scale: 0.2, x: origin.x, y: origin.y }}
        transition={{ type: "spring", stiffness: 320, damping: 30 }}
      >
        <div className="flex items-start justify-between gap-2 border-b px-4 py-3">
          <div className="min-w-0">
            <p className="text-xs font-medium text-muted-foreground">
              {channelName
                ? t(($) => $.mention_popup.title_in, { channel: channelName })
                : t(($) => $.mention_popup.title)}
            </p>
            <p className="mt-1 line-clamp-3 text-sm text-foreground">{item.body || item.title}</p>
          </div>
          <button
            type="button"
            onClick={onDismiss}
            aria-label={t(($) => $.mention_popup.later)}
            className="shrink-0 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="size-4" />
          </button>
        </div>
        <div className="grid grid-cols-2 gap-2 p-3">
          <Button size="sm" onClick={onContinue} disabled={busy}>
            {t(($) => $.mention_popup.continue)}
          </Button>
          <Button size="sm" variant="secondary" onClick={onDelegate} disabled={busy}>
            {t(($) => $.mention_popup.delegate, { name: gmName })}
          </Button>
          <Button size="sm" variant="ghost" onClick={onDismiss} disabled={busy}>
            {t(($) => $.mention_popup.later)}
          </Button>
          <Button size="sm" variant="outline" onClick={onManual} disabled={busy}>
            {t(($) => $.mention_popup.manual)}
          </Button>
        </div>
      </motion.div>
    </motion.div>
  );
}
