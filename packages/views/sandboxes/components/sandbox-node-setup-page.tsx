"use client";

import { useEffect, useState } from "react";
import { Check, Copy, FileCode2, Loader2, Server } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { sandboxNodeListOptions } from "@multica/core/sandboxes/queries";
import {
  buildSandboxdConfigPath,
  buildSandboxdSetupCommand,
  effectiveSandboxNodeStatus,
} from "@multica/core/sandboxes/utils";
import type { SandboxNode } from "@multica/core/types";
import { useRequiredWorkspaceSlug, useWorkspacePaths } from "@multica/core/paths";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";

type SetupCommandState =
  | { status: "loading" }
  | { status: "ready"; command: string; configPath: string }
  | { status: "error"; message: string };

function useNowTick(intervalMs = 10_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

function useSandboxNodeSetupCommand(node: SandboxNode | null): SetupCommandState {
  const workspaceSlug = useRequiredWorkspaceSlug();
  const user = useAuthStore((s) => s.user);
  const { t } = useT("layout");
  const [state, setState] = useState<SetupCommandState>({ status: "loading" });

  useEffect(() => {
    if (!node) {
      setState({ status: "loading" });
      return;
    }

    let cancelled = false;
    setState({ status: "loading" });

    void (async () => {
      try {
        const token = await api.createSandboxNodeToken(node.id, { name: "sandboxd setup" });
        if (cancelled) return;
        const serverUrl = api.getBaseUrl?.() || window.location.origin;
        const { command, configPath } = buildSandboxdSetupCommand({
          serverUrl,
          nodeToken: token.token,
          nodeKey: node.node_key,
          name: node.name,
          ownerUserId: node.owner_user_id || node.node_key,
          workspaceSlug,
          userName: user?.name,
          userEmail: user?.email,
          userId: user?.id,
          metadata: node.metadata,
        });
        setState({ status: "ready", command, configPath });
      } catch (e) {
        if (cancelled) return;
        setState({
          status: "error",
          message: e instanceof Error ? e.message : t(($) => $.sandboxes_page.view_setup_failed),
        });
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [node, t, user?.email, user?.id, user?.name, workspaceSlug]);

  return state;
}

export function SandboxNodeSetupPage({ nodeId }: { nodeId: string }) {
  const workspaceSlug = useRequiredWorkspaceSlug();
  const user = useAuthStore((s) => s.user);
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { t } = useT("layout");
  const now = useNowTick();
  const [copied, setCopied] = useState(false);

  const { data: nodes = [], isLoading: nodesLoading } = useQuery(sandboxNodeListOptions());
  const node = nodes.find((item) => item.id === nodeId) ?? null;
  const setup = useSandboxNodeSetupCommand(node);
  const nodeStatus = node
    ? effectiveSandboxNodeStatus(node.status, node.last_seen_at, now)
    : "offline";

  const handleCopy = async () => {
    if (setup.status !== "ready") return;
    if (await copyText(setup.command)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  useEffect(() => {
    if (setup.status === "error") {
      toast.error(setup.message);
    }
  }, [setup]);

  if (nodesLoading) {
    return (
      <div className="flex h-full min-h-0 flex-col bg-background">
        <BreadcrumbHeader
          segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
          leaf={<Skeleton className="h-4 w-32" />}
        />
        <div className="space-y-4 p-6">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-40 w-full max-w-3xl" />
        </div>
      </div>
    );
  }

  if (!node) {
    return (
      <div className="flex h-full min-h-0 flex-col bg-background">
        <BreadcrumbHeader
          segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
          leaf={
            <span className="truncate text-muted-foreground">
              {t(($) => $.sandboxes_page.view_setup_not_found)}
            </span>
          }
        />
        <div className="flex flex-1 flex-col items-center justify-center text-muted-foreground">
          <Server className="mb-3 size-10 text-muted-foreground/40" />
          <p className="text-sm">{t(($) => $.sandboxes_page.view_setup_not_found)}</p>
          <Button className="mt-4" variant="outline" onClick={() => navigation.push(paths.sandboxes())}>
            {t(($) => $.sandboxes_page.back_to_list)}
          </Button>
        </div>
      </div>
    );
  }

  const configPathFallback = buildSandboxdConfigPath({
    workspaceSlug,
    userName: user?.name,
    userEmail: user?.email,
    userId: user?.id,
    serverUrl: api.getBaseUrl?.() || window.location.origin,
  });

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <BreadcrumbHeader
        segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
        leaf={
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate font-medium">{node.name}</span>
            <span className="inline-flex shrink-0 items-center gap-1 rounded-md border bg-background px-2 py-0.5 text-xs text-muted-foreground">
              <span
                className={cn(
                  "h-1.5 w-1.5 rounded-full",
                  nodeStatus === "online" ? "bg-success" : "bg-muted-foreground/40",
                )}
              />
              {nodeStatus}
            </span>
          </span>
        }
      />

      <div className="min-h-0 flex-1 overflow-auto">
        <div className="mx-auto flex w-full max-w-3xl flex-col gap-6 p-6">
          <div>
            <div className="flex items-center gap-2">
              <FileCode2 className="size-5 text-muted-foreground" />
              <h1 className="text-xl font-semibold tracking-tight">
                {t(($) => $.sandboxes_page.setup_page_title)}
              </h1>
            </div>
            <p className="mt-2 text-sm text-muted-foreground">
              {t(($) => $.sandboxes_page.setup_page_description, {
                file: setup.status === "ready" ? setup.configPath : configPathFallback,
              })}
            </p>
          </div>

          <div className="rounded-lg border bg-card">
            <div className="flex items-center justify-between gap-3 border-b px-4 py-3">
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{node.name}</div>
                <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">{node.node_key}</div>
              </div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={handleCopy}
                disabled={setup.status !== "ready"}
              >
                {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                {copied
                  ? t(($) => $.sandboxes_page.copied_action)
                  : t(($) => $.sandboxes_page.copy_command_action)}
              </Button>
            </div>
            <div className="p-4">
              {setup.status === "loading" ? (
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  <Loader2 className="size-4 animate-spin" />
                  {t(($) => $.sandboxes_page.setup_page_loading)}
                </div>
              ) : setup.status === "error" ? (
                <p className="text-sm text-destructive">{setup.message}</p>
              ) : (
                <code
                  className={cn(
                    "block max-h-[min(28rem,60vh)] overflow-auto whitespace-pre-wrap break-all font-mono text-xs leading-relaxed select-all",
                    CODE_LIGATURE_CLASS,
                  )}
                >
                  {setup.command}
                </code>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
