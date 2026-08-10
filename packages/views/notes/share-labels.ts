import type { MemberWithUser, Workspace } from "@multica/core/types";

export function memberLabel(member: MemberWithUser | undefined, fallback = "") {
  return member?.display_name || member?.name || member?.email || fallback;
}

export function workspaceLabel(workspace: Workspace | undefined) {
  return workspace?.name || workspace?.slug || "";
}

export function buildNoteShareNames({
  shareUserIds,
  membersByUserId,
  workspaceName,
  unknownMemberLabel,
  formatName,
}: {
  shareUserIds: readonly string[];
  membersByUserId: ReadonlyMap<string, MemberWithUser>;
  workspaceName: string;
  unknownMemberLabel: string;
  formatName: (name: string, workspace: string) => string;
}) {
  return shareUserIds.map((id) => {
    const name = memberLabel(membersByUserId.get(id), unknownMemberLabel);
    return workspaceName ? formatName(name, workspaceName) : name;
  });
}
