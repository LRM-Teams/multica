import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CollapsibleMessageBody } from "./collapsible-message-body";
import { resetCollapsibleMessageExpandedMemoryForTests } from "./collapsible-message-expanded-memory";

afterEach(() => {
  resetCollapsibleMessageExpandedMemoryForTests();
});

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
      clientHeight: { configurable: true, value: 320 },
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(body).toHaveAttribute("data-collapsed", "true");
    });
    expect(body).toHaveClass("max-h-[320px]");
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

  it("notifies when expanded state changes", async () => {
    const onExpandedChange = vi.fn();
    render(
      <CollapsibleMessageBody
        contentKey="m3"
        expandLabel="See more"
        collapseLabel="See less"
        onExpandedChange={onExpandedChange}
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
      clientHeight: { configurable: true, value: 320 },
    });
    fireEvent(window, new Event("resize"));

    await userEvent.click(await screen.findByRole("button", { name: "See more" }));
    expect(onExpandedChange).toHaveBeenLastCalledWith(true);

    await userEvent.click(screen.getByRole("button", { name: "See less" }));
    expect(onExpandedChange).toHaveBeenLastCalledWith(false);
  });

  it("does not bubble expand or collapse taps to parent mobile gesture handlers", async () => {
    const onParentPointerDown = vi.fn();
    const onParentClick = vi.fn();
    render(
      <div onPointerDown={onParentPointerDown} onClick={onParentClick}>
        <CollapsibleMessageBody
          contentKey="m-mobile"
          expandLabel="See more"
          collapseLabel="See less"
        >
          <div>
            {Array.from({ length: 20 }, (_, index) => (
              <p key={index}>Line {index}</p>
            ))}
          </div>
        </CollapsibleMessageBody>
      </div>,
    );

    const body = screen.getByTestId("collapsible-message-body");
    Object.defineProperties(body, {
      scrollHeight: { configurable: true, value: 420 },
      clientHeight: { configurable: true, value: 320 },
    });
    fireEvent(window, new Event("resize"));

    const expand = await screen.findByRole("button", { name: "See more" });
    fireEvent.pointerDown(expand);
    await userEvent.click(expand);

    expect(onParentPointerDown).not.toHaveBeenCalled();
    expect(onParentClick).not.toHaveBeenCalled();
    expect(body).not.toHaveAttribute("data-collapsed");

    const collapse = screen.getByRole("button", { name: "See less" });
    fireEvent.pointerDown(collapse);
    await userEvent.click(collapse);

    expect(onParentPointerDown).not.toHaveBeenCalled();
    expect(onParentClick).not.toHaveBeenCalled();
    expect(body).toHaveAttribute("data-collapsed", "true");
  });

  it("does not collapse mid-length content under the widened threshold (LRM-750)", async () => {
    render(
      <CollapsibleMessageBody
        contentKey="m4"
        expandLabel="See more"
        collapseLabel="See less"
      >
        <div>
          {Array.from({ length: 10 }, (_, index) => (
            <p key={index}>Line {index}</p>
          ))}
        </div>
      </CollapsibleMessageBody>,
    );

    const body = screen.getByTestId("collapsible-message-body");
    Object.defineProperties(body, {
      scrollHeight: { configurable: true, value: 200 },
      clientHeight: { configurable: true, value: 200 },
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(body).not.toHaveAttribute("data-collapsed");
    });
    expect(screen.queryByTestId("message-collapse-fade")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "See more" })).not.toBeInTheDocument();
  });

  it("keeps expand across remount with the same contentKey (LRM-987)", async () => {
    const { unmount } = render(
      <CollapsibleMessageBody
        contentKey="remount-key"
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
      clientHeight: { configurable: true, value: 320 },
    });
    fireEvent(window, new Event("resize"));
    await userEvent.click(await screen.findByRole("button", { name: "See more" }));
    expect(body).not.toHaveAttribute("data-collapsed");
    unmount();

    render(
      <CollapsibleMessageBody
        contentKey="remount-key"
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
    const remounted = screen.getByTestId("collapsible-message-body");
    Object.defineProperties(remounted, {
      scrollHeight: { configurable: true, value: 420 },
      clientHeight: { configurable: true, value: 320 },
    });
    fireEvent(window, new Event("resize"));
    await waitFor(() => {
      expect(remounted).not.toHaveAttribute("data-collapsed");
    });
    expect(screen.getByRole("button", { name: "See less" })).toBeInTheDocument();
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
      clientHeight: { configurable: true, value: 320 },
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(body).not.toHaveAttribute("data-collapsed");
    });
    expect(screen.queryByRole("button", { name: "See more" })).not.toBeInTheDocument();
  });
});
