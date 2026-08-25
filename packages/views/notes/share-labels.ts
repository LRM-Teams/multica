import type { Agent, Channel, MemberWithUser, Workspace } from "@multica/core/types";

export function memberLabel(member: MemberWithUser | undefined, fallback = "") {
  return member?.display_name || member?.name || member?.email || fallback;
}

export function workspaceLabel(workspace: Workspace | undefined) {
  return workspace?.name || workspace?.slug || "";
}

export function buildNoteShareNames({
  shareUserIds,
  membersByUserId,
  shareAgentIds = [],
  agentsById = new Map(),
  shareChannelIds = [],
  channelsById = new Map(),
  workspaceName,
  unknownMemberLabel,
  formatName,
}: {
  shareUserIds: readonly string[];
  membersByUserId: ReadonlyMap<string, MemberWithUser>;
  shareAgentIds?: readonly string[];
  agentsById?: ReadonlyMap<string, Pick<Agent, "name" | "display_name">>;
  shareChannelIds?: readonly string[];
  channelsById?: ReadonlyMap<string, Pick<Channel, "name">>;
  workspaceName: string;
  unknownMemberLabel: string;
  formatName: (name: string, workspace: string) => string;
}) {
  const users = shareUserIds.map((id) => {
    const name = memberLabel(membersByUserId.get(id), unknownMemberLabel);
    return workspaceName ? formatName(name, workspaceName) : name;
  });
  const agents = shareAgentIds.map((id) => {
    const agent = agentsById.get(id);
    return agent?.display_name?.trim() || agent?.name || unknownMemberLabel;
  });
  const channels = shareChannelIds.map((id) => channelsById.get(id)?.name || unknownMemberLabel);
  return [...users, ...agents, ...channels];
}
