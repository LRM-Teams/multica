"use client";

import { useState } from "react";
import { Activity, FileText, User, X } from "lucide-react";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { resolveActorDisplayName } from "@multica/core/identity";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ActivityTab } from "../../agents/components/tabs/activity-tab";
import { AgentPresenceStatusLine } from "../../agents/components/agent-presence-status-line";
import { initialsOf } from "../../common/initials";
import { AgentFilesPanel } from "./agent-files-panel";
import { useT } from "../../i18n/use-t";

type OwnerTab = "activity" | "profile" | "files";

const OWNER_TABS: { id: OwnerTab; icon: typeof Activity }[] = [
  { id: "profile", icon: User },
  { id: "activity", icon: Activity },
  { id: "files", icon: FileText },
];

interface AgentSidePanelProps {
  agent: Agent;
  currentUserId: string | null;
  members: readonly MemberWithUser[];
  onClose: () => void;
}

/**
 * Right-pane surface opened by clicking an agent's avatar/name in the
 * conversation — mutually exclusive with the thread panel (same slot,
 * per Frank's direction 2026-07-09: inline panel, not a route jump).
 * Per Frank's follow-up correction: owner sees Profile/Activity/Files
 * (Profile first + default — the one tab that's always present, identity
 * before observation), non-owner sees Profile only (Files was always
 * owner-gated; Activity follows the same gate here per his explicit call,
 * not a generic observability surface for this panel).
 */
export function AgentSidePanel({ agent, currentUserId, members, onClose }: AgentSidePanelProps) {
  const { t } = useT("agents");
  const isOwner = !!currentUserId && agent.owner_id === currentUserId;
  const [tab, setTab] = useState<OwnerTab>("profile");
  const displayName = resolveActorDisplayName(agent, agent.id);
  const initials = initialsOf(displayName);

  return (
    <aside className="flex h-full min-h-0 flex-col border-l bg-background">
      <div className="flex items-center justify-between gap-3 border-b p-4">
        <div className="flex min-w-0 items-center gap-2.5">
          <ActorAvatarBase
            name={displayName}
            initials={initials}
            avatarUrl={resolvePublicFileUrl(agent.avatar_url)}
            isAgent
            size={32}
            className="rounded-md"
          />
          <p className="truncate text-sm font-semibold">{displayName}</p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {/* #371: live presence on the right of the header (same row as the
              name), so the state is visible before opening the Activity tab. */}
          <AgentPresenceStatusLine agentId={agent.id} />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={onClose}
            aria-label={t(($) => $.side_panel.close_aria)}
          >
            <X className="size-4" />
          </Button>
        </div>
      </div>

      {isOwner ? (
        <>
          <div className="flex shrink-0 items-center gap-0 border-b px-2">
            {OWNER_TABS.map((tabDef) => (
              <button
                key={tabDef.id}
                type="button"
                onClick={() => setTab(tabDef.id)}
                className={cn(
                  "flex shrink-0 items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2.5 text-xs font-medium transition-colors",
                  tab === tabDef.id
                    ? "border-foreground text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                <tabDef.icon className="size-3.5" />
                {t(($) => $.tabs[tabDef.id])}
              </button>
            ))}
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {tab === "activity" && <ActivityTab agent={agent} />}
            {tab === "profile" && <AgentProfileTabContent agent={agent} members={members} />}
            {tab === "files" && (
              <AgentFilesPanel
                agent={agent}
                currentUserId={currentUserId}
                members={members}
                onClose={onClose}
                hideHeader
              />
            )}
          </div>
        </>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <AgentProfileTabContent agent={agent} members={members} />
        </div>
      )}
    </aside>
  );
}

function ownerName(agent: Agent, members: readonly MemberWithUser[]): string {
  if (!agent.owner_id) return "—";
  const member = members.find((m) => m.user_id === agent.owner_id);
  return member?.display_name || member?.name || member?.email || agent.owner_id;
}

function formatDate(value: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function AgentProfileTabContent({
  agent,
  members,
}: {
  agent: Agent;
  members: readonly MemberWithUser[];
}) {
  const { t } = useT("agents");
  return (
    <div className="flex flex-col">
      <div className="border-b p-4">
        <p className="text-xs leading-5 text-foreground/85">
          {agent.description || t(($) => $.side_panel.no_description)}
        </p>
      </div>
      <div className="space-y-2 border-b p-4 text-xs">
        <InfoRow label={t(($) => $.side_panel.model_label)} value={agent.model} mono />
        <InfoRow
          label={t(($) => $.side_panel.reasoning_label)}
          value={agent.thinking_level?.trim() || t(($) => $.side_panel.reasoning_default)}
        />
        <InfoRow label={t(($) => $.side_panel.created_label)} value={formatDate(agent.created_at)} />
        <InfoRow label={t(($) => $.side_panel.owner_label)} value={ownerName(agent, members)} />
      </div>
    </div>
  );
}

function InfoRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className={cn("truncate text-foreground", mono && "font-mono")} title={value}>
        {value}
      </span>
    </div>
  );
}
