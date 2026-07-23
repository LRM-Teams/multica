import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RolesDialog } from "./roles-dialog";

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: Record<string, unknown>) => unknown) => {
      const bundle = {
        members: {
          roles_dialog: {
            title: "Roles",
            description: "Workspace roles. Unrelated to Agent identity.",
            close: "Close",
            cancel: "Cancel",
            save: "Save",
            saving: "Saving…",
          },
          roles: {
            owner: {
              label: "Owner",
              badge: "Workspace owner",
              description: "Full access",
            },
            admin: {
              label: "Admin",
              badge: "Administrator",
              description: "Manage members",
            },
            member: {
              label: "Member",
              badge: "Member",
              description: "Work on issues",
            },
          },
        },
      };
      return selector(bundle);
    },
  }),
}));

describe("RolesDialog (LRM-524)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows Roles title and Owner/Admin/Member — never Agent Roles", () => {
    render(
      <RolesDialog open onOpenChange={() => {}} mode="info" />,
    );

    const dialog = screen.getByTestId("roles-dialog");
    expect(dialog).toBeInTheDocument();
    expect(screen.getByText("Roles")).toBeInTheDocument();
    expect(
      screen.getByText("Workspace roles. Unrelated to Agent identity."),
    ).toBeInTheDocument();
    expect(screen.getByTestId("roles-dialog-card-owner")).toBeInTheDocument();
    expect(screen.getByTestId("roles-dialog-card-admin")).toBeInTheDocument();
    expect(screen.getByTestId("roles-dialog-card-member")).toBeInTheDocument();
    expect(dialog.textContent).not.toMatch(/Agent Roles/i);
    expect(dialog.textContent).not.toMatch(/AGENT ROLES/i);
    expect(dialog.textContent).not.toMatch(/server admin/i);
  });

  it("select mode saves chosen role (lock A)", () => {
    const onSave = vi.fn();
    render(
      <RolesDialog
        open
        onOpenChange={() => {}}
        mode="select"
        value="member"
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("roles-dialog-card-admin"));
    fireEvent.click(screen.getByTestId("roles-dialog-save"));
    expect(onSave).toHaveBeenCalledWith("admin");
  });

  it("uses token chrome classes — no neo-brutal black border/shadow utilities", () => {
    render(
      <RolesDialog open onOpenChange={() => {}} mode="info" />,
    );
    const dialog = screen.getByTestId("roles-dialog");
    expect(dialog.className).toContain("border-border");
    expect(dialog.className).toContain("rounded-2xl");
    expect(dialog.className).not.toMatch(/border-black|shadow-\[/);
  });
});
