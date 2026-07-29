import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CollapsibleMessageBody } from "./collapsible-message-body";

describe("CollapsibleMessageBody", () => {
  it("clips tall content behind See more and expands on click", async () => {
    render(
      <CollapsibleMessageBody
        contentKey="m1"
        expandLabel="See more"
        collapseLabel="See less"
      >
        <div>
          {Array.from({ length: 20 }, (_, index) => (
            <p key={index}>Line {index}</p>
          ))}
        </div>
      </CollapsibleMessageBody>,
    );

    const body = screen.getByTestId("collapsible-message-body");
    Object.defineProperties(body, {
      scrollHeight: { configurable: true, value: 420 },
      clientHeight: { configurable: true, value: 160 },
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(body).toHaveAttribute("data-collapsed", "true");
    });
    expect(body).toHaveClass("max-h-[160px]");
    expect(screen.getByTestId("message-collapse-fade")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "See more" })).toBeInTheDocument();
    // Full DOM stays mounted for copy / expand.
    expect(body).toHaveTextContent("Line 19");

    await userEvent.click(screen.getByRole("button", { name: "See more" }));
    expect(body).not.toHaveAttribute("data-collapsed");
    expect(screen.getByRole("button", { name: "See less" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "See less" }));
    expect(body).toHaveAttribute("data-collapsed", "true");
    expect(screen.getByRole("button", { name: "See more" })).toBeInTheDocument();
  });

  it("does not collapse when enabled is false", async () => {
    render(
      <CollapsibleMessageBody
        contentKey="m2"
        enabled={false}
        expandLabel="See more"
        collapseLabel="See less"
      >
        <p>Short</p>
      </CollapsibleMessageBody>,
    );

    const body = screen.getByTestId("collapsible-message-body");
    Object.defineProperties(body, {
      scrollHeight: { configurable: true, value: 420 },
      clientHeight: { configurable: true, value: 160 },
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(body).not.toHaveAttribute("data-collapsed");
    });
    expect(screen.queryByRole("button", { name: "See more" })).not.toBeInTheDocument();
  });
});
