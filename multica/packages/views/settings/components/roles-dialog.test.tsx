import type { ReactNode } from "react";
import { describe, expect, it, beforeEach, vi } from "vitest";
import { render as rtlRender, screen, type RenderOptions } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import { RolesDialog } from "./roles-dialog";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function render(ui: React.ReactElement, options?: RenderOptions) {
  return rtlRender(ui, { wrapper: I18nWrapper, ...options });
}

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({ children, open }: { children: ReactNode; open: boolean }) =>
    open ? <div>{children}</div> : null,
  DialogContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DialogHeader: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: ReactNode }) => <h1>{children}</h1>,
  DialogDescription: ({ children }: { children: ReactNode }) => <p>{children}</p>,
  DialogFooter: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

describe("RolesDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("titles the dialog Roles (never Agent Roles) and lists Owner/Admin/Member", () => {
    render(<RolesDialog open onOpenChange={vi.fn()} mode="info" />);

    expect(screen.getByRole("heading", { name: "Roles" })).toBeInTheDocument();
    expect(screen.queryByText(/Agent Roles/i)).not.toBeInTheDocument();
    expect(screen.getAllByText("Owner").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Admin").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Member").length).toBeGreaterThan(0);
    expect(screen.getByText("Workspace owner")).toBeInTheDocument();
    expect(
      screen.getByText(/Unrelated to Agent identity/i),
    ).toBeInTheDocument();
  });

  it("select mode requires Save and calls onSave with the chosen role", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    render(
      <RolesDialog
        open
        onOpenChange={vi.fn()}
        mode="select"
        value="member"
        onSave={onSave}
      />,
    );

    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();

    const radios = screen.getAllByRole("radio");
    expect(radios).toHaveLength(3);
    await user.click(radios[1]!); // Admin
    expect(save).toBeEnabled();
    await user.click(save);
    expect(onSave).toHaveBeenCalledWith("admin");
  });

  it("disables demoting the last owner without silent swap", async () => {
    const onSave = vi.fn();
    render(
      <RolesDialog
        open
        onOpenChange={vi.fn()}
        mode="select"
        value="owner"
        disabledReasons={{
          admin: "Cannot demote the last owner",
          member: "Cannot demote the last owner",
        }}
        onSave={onSave}
      />,
    );

    const radios = screen.getAllByRole("radio");
    expect(radios[1]).toBeDisabled();
    expect(radios[2]).toBeDisabled();
    expect(screen.getAllByText("Cannot demote the last owner").length).toBe(2);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(onSave).not.toHaveBeenCalled();
  });
});
