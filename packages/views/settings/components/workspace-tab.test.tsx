import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockUpdateWorkspace = vi.hoisted(() => vi.fn());
const mockInvalidateQueries = vi.hoisted(() => vi.fn());
const workspaceRef = vi.hoisted(() => ({
  current: {
    id: "workspace-1",
    name: "Test Workspace",
    slug: "test-workspace",
    description: "",
    context: "",
    issue_prefix: "TES",
  },
}));
const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as "owner" | "admin" | "member" }],
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: membersRef.current, isFetched: true }),
  useQueryClient: () => ({
    setQueryData: vi.fn(),
    getQueryData: vi.fn(() => []),
    invalidateQueries: mockInvalidateQueries,
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => workspaceRef.current,
  useHasOnboarded: () => true,
  resolvePostAuthDestination: () => "/",
}));

vi.mock("@multica/core/platform", () => ({
  setCurrentWorkspace: vi.fn(),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
  workspaceListOptions: () => ({ queryKey: ["workspaces"], queryFn: vi.fn() }),
  workspaceKeys: { list: () => ["workspaces"] },
}));

vi.mock("@multica/core/issues/queries", () => ({
  issueKeys: { all: (wsId: string) => ["issues", wsId] },
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  useLeaveWorkspace: () => ({ mutateAsync: vi.fn() }),
  useDeleteWorkspace: () => ({ mutateAsync: vi.fn() }),
}));

vi.mock("@multica/core/api", () => ({
  api: { updateWorkspace: mockUpdateWorkspace, getBaseUrl: () => "http://127.0.0.1:8080" },
}));

vi.mock("@multica/core/auth", () => {
  const useAuthStore = Object.assign(
    (sel?: (s: { user: { id: string } }) => unknown) =>
      sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});

vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: vi.fn() }),
}));

vi.mock("./delete-workspace-dialog", () => ({
  DeleteWorkspaceDialog: () => null,
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { WorkspaceTab } from "./workspace-tab";

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

describe("WorkspaceTab — issue prefix editing", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    workspaceRef.current = {
      id: "workspace-1",
      name: "Test Workspace",
      slug: "test-workspace",
      description: "",
      context: "",
      issue_prefix: "TES",
    };
    membersRef.current = [{ user_id: "user-1", role: "owner" }];
    mockUpdateWorkspace.mockImplementation(
      async (
        _id: string,
        payload: { issue_prefix?: string; name?: string },
      ) => ({
        ...workspaceRef.current,
        ...payload,
        issue_prefix: payload.issue_prefix ?? workspaceRef.current.issue_prefix,
      }),
    );
  });

  it("renders the current prefix in the input", () => {
    render(<WorkspaceTab />, { wrapper: I18nWrapper });
    const input = screen.getByPlaceholderText("TES") as HTMLInputElement;
    expect(input.value).toBe("TES");
  });

  it("uppercases and strips non-alphanumeric input as the user types", async () => {
    const user = userEvent.setup();
    render(<WorkspaceTab />, { wrapper: I18nWrapper });
    const input = screen.getByPlaceholderText("TES") as HTMLInputElement;

    await user.clear(input);
    await user.type(input, "ab-12!cd");

    expect(input.value).toBe("AB12CD");
  });



  it("cancelling the confirm dialog does not save", async () => {
    const user = userEvent.setup();
    render(<WorkspaceTab />, { wrapper: I18nWrapper });

    const input = screen.getByPlaceholderText("TES") as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "NEW");

    await user.click(screen.getByRole("button", { name: /^Save$/ }));

    await screen.findByText(/Change issue prefix/i);
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(mockUpdateWorkspace).not.toHaveBeenCalled();
    // The user's edited value is preserved so they can resume.
    expect(input.value).toBe("NEW");
  });

  it("disables Save when the prefix is empty", async () => {
    const user = userEvent.setup();
    render(<WorkspaceTab />, { wrapper: I18nWrapper });

    const input = screen.getByPlaceholderText("TES") as HTMLInputElement;
    await user.clear(input);

    expect(screen.getByRole("button", { name: /^Save$/ })).toBeDisabled();
  });

  it("disables the prefix input for non-admins", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    render(<WorkspaceTab />, { wrapper: I18nWrapper });
    expect(screen.getByPlaceholderText("TES")).toBeDisabled();
  });
});
