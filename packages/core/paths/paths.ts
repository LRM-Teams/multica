/**
 * Centralized URL path builder. All navigation in shared packages (packages/views)
 * MUST go through this module — no hardcoded string paths.
 *
 * Two kinds of paths:
 *  - workspace-scoped: paths.workspace(slug).xxx() — carry workspace in URL
 *  - global: paths.login(), paths.newWorkspace(), paths.invite(id) — pre-workspace routes
 *
 * Why pure functions + builder pattern:
 *  - Changing a route shape (e.g. adding workspace slug prefix) becomes a single-file edit
 *  - IDs are always URL-encoded here so callers can't forget
 *  - Zero runtime deps means this module is safe in Node (tests) and browsers
 */

import { membersPathWithSelection } from "./members-selection";

const encode = (id: string) => encodeURIComponent(id);

function workspaceScoped(slug: string) {
  const ws = `/${encode(slug)}`;
  const membersBase = () => `${ws}/members`;
  return {
    root: () => `${ws}/channels`,
    overview: () => `${ws}/overview`,
    usage: () => `${ws}/usage`,
    evolution: () => `${ws}/evolution`,
    notes: () => `${ws}/notes`,
    noteDetail: (id: string) => `${ws}/notes/${encode(id)}`,
    /** Member-visible memory Wiki (LRM-1001). Nav label: 知识 / Knowledge. */
    wiki: () => `${ws}/wiki`,
    wikiDetail: (id: string) => `${ws}/wiki/${encode(id)}`,
    planBilling: () => `${ws}/plan-billing`,
    issues: () => `${ws}/issues`,
    issueDetail: (id: string) => `${ws}/issues/${encode(id)}`,
    projects: () => `${ws}/projects`,
    projectDetail: (id: string) => `${ws}/projects/${encode(id)}`,
    research: () => `${ws}/research`,
    researchDetail: (id: string) => `${ws}/research/${encode(id)}`,
    channels: () => `${ws}/channels`,
    channelDetail: (id: string) => `${ws}/channels/${encode(id)}`,
    /**
     * Members Directory primary route (ADR 0013).
     * Optional selection: pass kind+id to set `?member=agent|user:<id>`.
     */
    members: (selection?: { kind: "agent" | "user"; id: string }) =>
      selection
        ? membersPathWithSelection(membersBase(), selection.kind, selection.id)
        : membersBase(),
    /**
     * @deprecated Redirect target only — use `members()`. Kept so call sites
     * that still navigate via agents() land on the Directory.
     */
    agents: () => membersBase(),
    /**
     * @deprecated Prefer `members({ kind: "agent", id })`. Resolves to the
     * Directory with the Agent selected (not a separate management page).
     */
    agentDetail: (id: string) =>
      membersPathWithSelection(membersBase(), "agent", id),
    /**
     * Human selected on Members Directory (query selection). Legacy
     * `/members/:id` full page is retired with the Directory cutover.
     */
    memberDetail: (id: string) =>
      membersPathWithSelection(membersBase(), "user", id),
    // Actor-generic lightweight profile (agent OR user). Distinct from
    // the Members Directory page: this is the mobile full-page host for the
    // same peek content the desktop HoverCard shows, so Recent activity can
    // scroll instead of being capped by an 80dvh drawer.
    actorProfile: (memberType: "agent" | "user", memberId: string) =>
      `${ws}/profile/${encode(memberType)}/${encode(memberId)}`,
    inbox: () => `${ws}/inbox`,
    myIssues: () => `${ws}/my-issues`,
    // Page path is "computers" (task #18) — the page lists computers, not
    // the "runtime" concept (a computer's bound code-agent process), which
    // keeps its existing name everywhere else per Frank's explicit ruling.
    computers: () => `${ws}/computers`,
    computersAttention: (runtimeId: string) =>
      `${ws}/computers?attention_runtime=${encode(runtimeId)}`,
    computerDetail: (id: string) => `${ws}/computers/${encode(id)}`,
    sandboxes: () => `${ws}/sandboxes`,
    sandboxDetail: (id: string) => `${ws}/sandboxes/${encode(id)}`,
    sandboxNodeSetup: (nodeId: string) => `${ws}/sandboxes/nodes/${encode(nodeId)}`,
    skills: () => `${ws}/skills`,
    skillDetail: (id: string) => `${ws}/skills/${encode(id)}`,
    settings: () => `${ws}/settings`,
    attachmentPreview: (id: string) => `${ws}/attachments/${encode(id)}/preview`,
  };
}

export const paths = {
  workspace: workspaceScoped,

  // Global (pre-workspace) routes
  login: () => "/login",
  newWorkspace: () => "/workspaces/new",
  invite: (id: string) => `/invite/${encode(id)}`,
  invitations: () => "/invitations",
  device: () => "/device",
  onboarding: () => "/onboarding",
  authCallback: () => "/auth/callback",
  root: () => "/",
};

export type WorkspacePaths = ReturnType<typeof workspaceScoped>;

// Prefixes — not slug names — because we match against full URL paths.
// A path is global if it equals or begins with any of these.
// Note: `/workspaces/` (trailing slash) is the prefix — `workspaces` is reserved,
// so any path starting with `/workspaces/...` is system-owned, not user-owned.
const GLOBAL_PREFIXES = ["/login", "/workspaces/", "/invite/", "/invitations", "/onboarding", "/auth/", "/logout", "/signup", "/device"];

export function isGlobalPath(path: string): boolean {
  return GLOBAL_PREFIXES.some((p) => path === p || path.startsWith(p));
}
