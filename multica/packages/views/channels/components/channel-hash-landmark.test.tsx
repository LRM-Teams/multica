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

  it("renders the uploaded channel icon instead of the # glyph when avatarUrl is set", () => {
    const { getByTestId, queryByTestId } = render(
      <ChannelHashLandmark avatarUrl="/uploads/channel-icon.png" />,
    );
    const img = getByTestId("channel-avatar-image");
    expect(img.getAttribute("src")).toContain("/uploads/channel-icon.png");
    expect(img.getAttribute("data-size")).toBe("sm");
    expect(queryByTestId("channel-hash-landmark")).toBeNull();
  });

  it("falls back to the # glyph when the avatar url is null", () => {
    const { getByTestId, queryByTestId } = render(
      <ChannelHashLandmark avatarUrl={null} />,
    );
    expect(getByTestId("channel-hash-landmark")).toBeTruthy();
    expect(queryByTestId("channel-avatar-image")).toBeNull();
  });
});
