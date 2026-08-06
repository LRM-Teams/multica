import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CollapsibleMessageBody } from "./collapsible-message-body";
import { resetCollapsibleMessageExpandedMemoryForTests } from "./collapsible-message-expanded-memory";

afterEach(() => {
  resetCollapsibleMessageExpandedMemoryForTests();
});

describe("CollapsibleMessageBody", () => {

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



});
