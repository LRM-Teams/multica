import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { ChannelHashMark } from "./channel-hash-mark";

describe("ChannelHashMark", () => {
  it("renders a text-level hash without an avatar tile", () => {
    const { container, getByText } = render(<ChannelHashMark size="sidebar" />);
    expect(getByText("#")).toBeTruthy();
    expect(container.querySelector("svg")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
  });

  it("scales the glyph for header vs sidebar", () => {
    const { rerender, container } = render(<ChannelHashMark size="sidebar" />);
    expect(container.firstElementChild?.className).toContain("text-[15px]");
    rerender(<ChannelHashMark size="header" />);
    expect(container.firstElementChild?.className).toContain("text-lg");
  });
});
