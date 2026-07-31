// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  HonorUnlockToast,
  honorUnlockToastOptions,
} from "./honor-unlock-toast";

describe("HonorUnlockToast", () => {
  it("renders a compact theme-aware notification and dismisses it", () => {
    const onDismiss = vi.fn();

    render(
      <HonorUnlockToast
        eyebrow="Achievement unlocked"
        title="Mars"
        meta="Unlocked by 12% of users"
        svgKey="mars"
        rare
        dismissLabel="Dismiss"
        onDismiss={onDismiss}
      />,
    );

    const toast = screen.getByTestId("honor-unlock-toast");
    expect(toast).toHaveClass(
      "w-[min(340px,calc(100vw-1.5rem))]",
      "bg-popover/95",
      "text-popover-foreground",
    );
    expect(toast.querySelector("[class~='size-9']")).not.toBeNull();
    expect(toast).toHaveTextContent("Mars");
    expect(screen.getByText("Unlocked by 12% of users")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(onDismiss).toHaveBeenCalledOnce();
  });

  it("appears above the composer and opts out of Sonner card styling", () => {
    expect(honorUnlockToastOptions).toMatchObject({
      position: "top-right",
      unstyled: true,
      duration: 6000,
    });
  });
});
