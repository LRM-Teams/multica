"use client";

import { useState } from "react";
import {
  AlertCircle,
  ArrowLeft,
  Lock,
  MoreHorizontal,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import {
  agentDetailOptions,
  type AgentPresence,
  useWorkspaceAgentPresence,
} from "@multica/core/agents";
import { api, ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import {
  agentListOptions,
  memberListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { agentRuntimeConfigOptions, runtimeListOptions } from "@multica/core/runtimes";
import { useAgentPermissions } from "@multica/core/permissions";
import { resolveActorDisplayName } from "@multica/core/identity";
import { Button } from "@multica/ui/components/ui/button";
import { CapabilityBanner } from "@multica/ui/components/common/capability-banner";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { AppLink, useNavigation } from "../../navigation";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { ConfirmDeleteAgent } from "./confirm-delete-agent";
import { PageHeader } from "../../layout/page-header";
import { AgentDetailInspector } from "./agent-detail-inspector";
import { AgentOverviewPane, type DetailTab } from "./agent-overview-pane";
import { useUpdateAgent } from "../hooks/use-update-agent";
import { useT } from "../../i18n";

interface AgentDetailPageProps {
  agentId: string;
}

export function AgentDetailPage({ agentId }: AgentDetailPageProps) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const workspace = useCurrentWorkspace();
  const navigation = useNavigation();
  const qc = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);

  const {
    data: agents = [],
    isLoading: agentsLoading,
    error: agentsError,
    refetch: refetchAgents,
  } = useQuery(agentListOptions(wsId));
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  // Single server-owned Workspace Presence snapshot; this page just reads its
  // slot and treats loading/malformed data as unknown rather than Offline.
  const { byAgent: presenceMap, loading: presenceLoading } =
    useWorkspaceAgentPresence(wsId);

  const listedAgent = agents.find((a) => a.id === agentId) ?? null;

  // Fallback fetch: when the agent is missing from the default directory
  // (archived, private, etc.), GET /api/agents/{id} is authoritative
  // (LRM-292 / LRM-410). Only fires after the list has settled.
  const {
    data: detailAgent,
    error: detailError,
    isLoading: detailLoading,
    refetch: refetchDetail,
  } = useQuery({
    ...agentDetailOptions(wsId, agentId),
    enabled: !agentsLoading && !listedAgent && !!agentId,
  });
  const agent = listedAgent ?? detailAgent ?? null;
  // Assembled server-side; see AgentDetailInspector's runtimeConfig prop.
  const { data: runtimeConfig } = useQuery(
    agentRuntimeConfigOptions(wsId, agent?.id ?? ""),
  );
  const presence: AgentPresence | null = agent && !presenceLoading
    ? presenceMap.get(agent.id) ?? null
    : null;
  const isForbidden =
    detailError instanceof ApiError && detailError.status === 403;

  // Permission hook MUST be called unconditionally — its `agent | null`
  // signature handles the not-found / loading case internally so the early
  // returns below don't violate the rules of hooks. Backend gates archive
  // and restore identically to edit, so a single `canEdit` covers them all.
  const { canEdit, canChangeRole } = useAgentPermissions(agent, wsId);
  const currentMemberRole = members.find((member) => member.user_id === currentUser?.id)?.role;
  const canManageLifecycle =
    canEdit.allowed &&
    (workspace?.onboarding_agent_id !== agent?.id || currentMemberRole === "owner");

  const [confirmArchive, setConfirmArchive] = useState(false);
  const [archiving, setArchiving] = useState(false);

  // One-shot channel: the inspector's compact Lark status row asks the
  // overview pane to focus a tab. The pane clears it after consuming.
  const [tabNavIntent, setTabNavIntent] = useState<DetailTab | null>(null);

  const handleUpdate = useUpdateAgent(wsId);

  const handleArchive = async (id: string) => {
    setArchiving(true);
    try {
      await api.archiveAgent(id);
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      toast.success(t(($) => $.detail.agent_archived_toast));
      setConfirmArchive(false);
      navigation.push(paths.agents());
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.detail.archive_failed_toast));
    } finally {
      setArchiving(false);
    }
  };

  const handleRestore = async (id: string) => {
    try {
      await api.restoreAgent(id);
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      toast.success(t(($) => $.detail.agent_restored_toast));
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.detail.restore_failed_toast));
    }
  };

  // --- Loading ---
  if ((agentsLoading || detailLoading) && !agent) {
    return <DetailLoadingSkeleton />;
  }

  // --- No permission (private agent the caller is not in allowed_principals for) ---
  if (!agent && isForbidden) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <BackHeader paths={paths.agents()} title={t(($) => $.detail.back_to_agents)} />
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
          <Lock className="h-8 w-8 text-muted-foreground" />
          <div>
            <p className="text-sm font-medium">{t(($) => $.detail.no_access_title)}</p>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.detail.no_access_hint)}
            </p>
          </div>
          <Button
            type="button"
            size="sm"
            onClick={() => navigation.push(paths.agents())}
          >
            {t(($) => $.detail.back_to_agents_full)}
          </Button>
        </div>
      </div>
    );
  }

  // --- Not found / error ---
  if (!agent) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <BackHeader paths={paths.agents()} title={t(($) => $.detail.back_to_agents)} />
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
          <AlertCircle className="h-8 w-8 text-destructive" />
          <div>
            <p className="text-sm font-medium">{t(($) => $.detail.not_found_title)}</p>
            <p className="mt-1 text-xs text-muted-foreground">
              {detailError instanceof Error
                ? detailError.message
                : agentsError instanceof Error
                  ? agentsError.message
                  : t(($) => $.detail.not_found_default)}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                void refetchAgents();
                void refetchDetail();
              }}
            >
              {t(($) => $.detail.try_again)}
            </Button>
            <Button
              type="button"
              size="sm"
              onClick={() => navigation.push(paths.agents())}
            >
              {t(($) => $.detail.back_to_agents_full)}
            </Button>
          </div>
        </div>
      </div>
    );
  }

  const isArchived = !!agent.archived_at;
  const owner = agent.owner_id
    ? members.find((m) => m.user_id === agent.owner_id) ?? null
    : null;

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <DetailHeader
        agent={agent}
        backHref={paths.agents()}
        canArchive={canManageLifecycle}
        onArchive={() => setConfirmArchive(true)}
      />

      {!canEdit.allowed && (
        <div className="px-6 pt-3">
          <CapabilityBanner
            reason={canEdit.reason}
            resource="agent"
            ownerName={owner?.name}
          />
        </div>
      )}

      {isArchived && (
        <div className="flex shrink-0 items-center gap-2 border-b bg-muted/50 px-6 py-2 text-xs text-muted-foreground">
          <AlertCircle className="h-3.5 w-3.5 shrink-0" />
          <span className="flex-1">
            {t(($) => $.detail.archived_banner)}
          </span>
          {canManageLifecycle && (
            <Button
              variant="outline"
              size="sm"
              className="h-6 text-xs"
              onClick={() => handleRestore(agent.id)}
            >
              {t(($) => $.detail.restore)}
            </Button>
          )}
        </div>
      )}

      <div className="flex flex-1 min-h-0 flex-col gap-3 overflow-y-auto p-3 md:grid md:grid-cols-[320px_minmax(0,1fr)] md:gap-4 md:overflow-hidden md:p-6">
        <AgentDetailInspector
          agent={agent}
          runtimeConfig={runtimeConfig}
          owner={owner}
          presence={presence}
          runtimes={runtimes}
          members={members}
          currentUserId={currentUser?.id ?? null}
          canEdit={canEdit.allowed}
          canChangeRole={canChangeRole}
          wsId={wsId}
          onRoleChanged={() =>
            qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) })
          }
          onUpdate={handleUpdate}
          onShowIntegrations={() => setTabNavIntent("integrations")}
        />

        <AgentOverviewPane
          agent={agent}
          runtimes={runtimes}
          onUpdate={handleUpdate}
          canManage={canEdit.allowed}
          initialTab={
            navigation.searchParams.get("tab") === "honor"
              ? "honor"
              : undefined
          }
          navIntent={tabNavIntent}
          onNavIntentHandled={() => setTabNavIntent(null)}
        />
      </div>

      <ConfirmDeleteAgent
        open={confirmArchive}
        displayName={resolveActorDisplayName(agent, agent.id)}
        pending={archiving}
        onConfirm={() => void handleArchive(agent.id)}
        onOpenChange={setConfirmArchive}
      />
    </div>
  );
}

function DetailHeader({
  agent,
  backHref,
  canArchive,
  onArchive,
}: {
  agent: Agent;
  backHref: string;
  canArchive: boolean;
  onArchive: () => void;
}) {
  const { t } = useT("agents");
  const isArchived = !!agent.archived_at;
  const displayName = resolveActorDisplayName(agent, agent.id);
  // LRM-248: live Online/Offline is avatar-badge only on profile surfaces —
  // breadcrumb leaf keeps name (+ muted Archived), never Online/Offline text.
  // Last-task state stays in Recent work, not the header.

  return (
    <BreadcrumbHeader
      segments={[{ href: backHref, label: t(($) => $.page.title) }]}
      leaf={
        <>
          <h1 className="min-w-0 truncate text-sm font-medium text-foreground">{displayName}</h1>
          {isArchived ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              {t(($) => $.row.archived)}
            </span>
          ) : null}
        </>
      }
      actions={
        !isArchived && canArchive ? (
          <DropdownMenu>
            <DropdownMenuTrigger
              render={<Button variant="ghost" size="icon-sm" />}
            >
              <MoreHorizontal className="h-4 w-4 text-muted-foreground" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-auto">
              <DropdownMenuItem
                className="text-destructive"
                onClick={onArchive}
              >
                <Trash2 className="h-3.5 w-3.5" />
                {t(($) => $.detail.more_archive)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        ) : null
      }
    />
  );
}

function BackHeader({ paths, title }: { paths: string; title: string }) {
  return (
    <PageHeader className="justify-between px-5">
      <div className="flex items-center gap-2">
        <AppLink
          href={paths}
          className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          {title}
        </AppLink>
      </div>
    </PageHeader>
  );
}

function DetailLoadingSkeleton() {
  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="px-5">
        <Skeleton className="h-5 w-48" />
      </PageHeader>
      <div className="flex flex-1 min-h-0 flex-col gap-3 overflow-y-auto p-3 md:grid md:grid-cols-[320px_minmax(0,1fr)] md:gap-4 md:overflow-hidden md:p-6">
        <div className="flex flex-col gap-4 rounded-lg border p-5">
          <Skeleton className="h-14 w-14 rounded-lg" />
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-3 w-full" />
          <div className="space-y-2">
            <Skeleton className="h-3 w-3/4" />
            <Skeleton className="h-3 w-2/3" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        </div>
        <div className="flex flex-col gap-4 rounded-lg border p-6">
          <Skeleton className="h-6 w-64" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-5/6" />
          <Skeleton className="h-4 w-4/6" />
        </div>
      </div>
    </div>
  );
}
