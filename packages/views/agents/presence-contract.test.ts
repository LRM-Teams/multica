// @vitest-environment node

import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = dirname(fileURLToPath(import.meta.url));

function source(relativePath: string): string {
  return readFileSync(resolve(here, relativePath), "utf8");
}

const livePresenceEntryPoints = [
  "../common/actor-avatar.tsx",
  "components/agents-page.tsx",
  "components/agent-detail-page.tsx",
  "components/agent-detail-inspector.tsx",
  "components/agent-detail-overview.tsx",
  "components/agent-coarse-presence-line.tsx",
  "../chat/components/chat-window.tsx",
  "../issues/components/comment-trigger-chips.tsx",
  "../runtimes/components/delete-computer-dialog.tsx",
  "../runtimes/components/delete-runtime-dialog.tsx",
  "../runtimes/components/runtimes-page.tsx",
  "../members/members-directory-page.tsx",
] as const;

describe("Agent Presence hard-cut contract", () => {
  it("keeps the deleted Runtime/Task-derived Presence modules deleted", () => {
    for (const relativePath of [
      "../../core/agents/derive-presence.ts",
      "../../core/agents/use-agent-presence.ts",
    ]) {
      expect(existsSync(resolve(here, relativePath)), relativePath).toBe(false);
    }
  });

  it("keeps live Presence entry points off Runtime, Task, Health, and lifecycle inference", () => {
    const combined = livePresenceEntryPoints
      .map((relativePath) => source(relativePath))
      .join("\n");

    for (const forbidden of [
      "deriveAgentAvailability",
      "runtimeReachabilityFromAgent",
      "runtime_display_status",
      "resolveHealthDotClass",
      "useAgentPresenceDetail",
      "useWorkspacePresenceMap",
    ]) {
      expect(combined, forbidden).not.toContain(forbidden);
    }
  });

  it("keeps the shared avatar dot binary and independent from Runner Activity", () => {
    const avatar = source("../common/actor-avatar.tsx");
    expect(avatar).toContain("useAgentPresence");
    expect(avatar).not.toContain("useRunnerActivity");
    expect(avatar).not.toContain("deriveWorkload");
    expect(avatar).not.toContain("animate-ping");
    expect(avatar).not.toContain("bg-warning");
  });

  it("does not leave a Health-to-Presence adapter beside diagnostic Health UI", () => {
    expect(source("health.ts")).not.toContain("resolveHealthDotClass");
  });

  it("keeps compact Agent Presence locale vocabulary binary", () => {
    for (const relativePath of [
      "../locales/en/agents.json",
      "../locales/zh-Hans/agents.json",
    ]) {
      const locale = JSON.parse(source(relativePath)) as Record<string, unknown>;
      expect(Object.keys(locale.availability as Record<string, string>)).toEqual([
        "all",
        "online",
        "offline",
      ]);
      expect(locale).not.toHaveProperty("lifecycle_status");
    }
  });

  it("passes the one page-level snapshot into large Agent row avatars", () => {
    const agentsPage = source("components/agents-page.tsx");
    const runtimesPage = source("../runtimes/components/runtimes-page.tsx");
    const membersPage = source("../members/members-directory-page.tsx");
    expect(agentsPage).toContain("agentPresence={presence}");
    expect(runtimesPage).toContain("presence={presenceMap.get(agent.id) ?? null}");
    expect(membersPage).toContain("useWorkspaceAgentPresence");
    expect(membersPage).toContain("agentPresence.get(a.id)");
  });
});
