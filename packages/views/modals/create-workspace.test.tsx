import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CreateWorkspaceModal } from "./create-workspace";

const push = vi.fn();

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push }),
}));

vi.mock("../platform", () => ({
  DragStrip: () => null,
}));

vi.mock("../i18n", () => ({
  useT: () => ({ t: () => "copy" }),
}));

vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (state: { workspaceCreationDisabled: boolean }) => unknown) =>
    selector({ workspaceCreationDisabled: false }),
}));

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DialogContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: ReactNode }) => <h1>{children}</h1>,
  DialogDescription: ({ children }: { children: ReactNode }) => <p>{children}</p>,
}));

vi.mock("../workspace/create-workspace-form", () => ({
  CreateWorkspaceForm: ({
    onSuccess,
  }: {
    onSuccess: (workspace: { slug: string }) => void;
  }) => (
    <button type="button" onClick={() => onSuccess({ slug: "new-team" })}>
      Finish creation
    </button>
  ),
}));

describe("CreateWorkspaceModal", () => {
  beforeEach(() => {
    push.mockReset();
  });

  it("enters the new Workspace through Messages after setup completes", () => {
    const onClose = vi.fn();
    render(<CreateWorkspaceModal onClose={onClose} />);

    fireEvent.click(screen.getByRole("button", { name: "Finish creation" }));

    expect(onClose).toHaveBeenCalledOnce();
    expect(push).toHaveBeenCalledWith("/new-team/channels");
  });
});
