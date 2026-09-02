/**
 * @vitest-environment happy-dom
 */
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";

vi.mock("next-themes", () => ({
  useTheme: () => ({ resolvedTheme: "light" }),
}));

const { Toaster } = await import("@multica/ui/components/ui/sonner");

describe("Toaster close control", () => {
  afterEach(() => {
    toast.dismiss();
  });

  it("dismisses a product toast from the top-right close button", async () => {
    const user = userEvent.setup();
    render(<Toaster />);
    toast.success("采集目录已保存");

    expect(await screen.findByText("采集目录已保存")).toBeInTheDocument();
    const close = screen.getByRole("button", { name: "Close toast" });
    expect(close.className).toContain("cn-toast-close");

    await user.click(close);
    await waitFor(() => {
      expect(screen.queryByText("采集目录已保存")).not.toBeInTheDocument();
    });
  });
});
