import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { ChannelHashLandmark } from "./channel-hash-landmark";

describe("ChannelHashLandmark", () => {
  it("renders a text-level # (no avatar slot / svg tile)", () => {
    const { getByTestId, container } = render(<ChannelHashLandmark />);
    const el = getByTestId("channel-hash-landmark");
    expect(el.textContent).toBe("#");
    expect(el.getAttribute("data-size")).toBe("sm");
    expect(container.querySelector("svg")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
  });

  it("supports the larger header size", () => {
    const { getByTestId } = render(<ChannelHashLandmark size="lg" />);
    expect(getByTestId("channel-hash-landmark").getAttribute("data-size")).toBe("lg");
  });
});
