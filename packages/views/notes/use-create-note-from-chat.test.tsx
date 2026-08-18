/**
 * @vitest-environment happy-dom
 */
import { renderHook, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "../locales";
import { useCreateNoteFromChat } from "./use-create-note-from-chat";

const createNotePage = vi.fn();
const updateNotePage = vi.fn();
const push = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    createNotePage: (...args: unknown[]) => createNotePage(...args),
    updateNotePage: (...args: unknown[]) => updateNotePage(...args),
  },
}));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspaceSlug: () => "acme",
}));

vi.mock("../navigation/context", () => ({
  useOptionalNavigation: () => ({ push }),
}));

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

function wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("useCreateNoteFromChat", () => {
  beforeEach(() => {
    createNotePage.mockReset();
    updateNotePage.mockReset();
    push.mockReset();
    vi.mocked(toast.success).mockReset();
    createNotePage.mockResolvedValue({ id: "new-1", title: "Ship it", content: "" });
    updateNotePage.mockResolvedValue({
      id: "new-1",
      title: "Ship it",
      content: "Ship it\n\nDetails.",
    });
  });

  it("creates a note from chat text and toasts with an open action", async () => {
    const { result } = renderHook(() => useCreateNoteFromChat(), { wrapper });

    await result.current.createNoteFromText("Ship it\n\nDetails.");

    await waitFor(() => {
      expect(createNotePage).toHaveBeenCalledWith({ title: "Ship it" });
      expect(updateNotePage).toHaveBeenCalledWith("new-1", {
        content: "Ship it\n\nDetails.",
      });
    });
    expect(toast.success).toHaveBeenCalledWith(
      "Note created",
      expect.objectContaining({
        action: expect.objectContaining({ label: "Open note" }),
      }),
    );
  });

  it("does not create a note from blank text", async () => {
    const { result } = renderHook(() => useCreateNoteFromChat(), { wrapper });
    await result.current.createNoteFromText("   ");
    expect(createNotePage).not.toHaveBeenCalled();
  });
});
