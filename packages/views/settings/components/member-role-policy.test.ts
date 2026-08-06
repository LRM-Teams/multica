import { describe, expect, it } from "vitest";
import { editableWorkspaceRoles, workspaceMemberActions } from "./member-role-policy";

describe("workspace member role policy", () => {
  it("never exposes Owner promotion or Owner lifecycle actions", () => {
    expect(editableWorkspaceRoles).toEqual(["admin", "member"]);
    expect(workspaceMemberActions({ canManage: true, isSelf: false, targetRole: "owner" })).toEqual({
      canEditRole: false,
      canRemove: false,
    });
  });

  it("keeps Admin and Member management available", () => {
    expect(workspaceMemberActions({ canManage: true, isSelf: false, targetRole: "admin" })).toEqual({
      canEditRole: true,
      canRemove: true,
    });
    expect(workspaceMemberActions({ canManage: true, isSelf: true, targetRole: "member" })).toEqual({
      canEditRole: false,
      canRemove: false,
    });
  });
});
