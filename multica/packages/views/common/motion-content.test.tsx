import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MotionContent } from "./motion-content";

describe("MotionContent (#820 motion contract)", () => {
  it("renders its children", () => {
    render(
      <MotionContent motionKey="a">
        <p>hello</p>
      </MotionContent>,
    );
    expect(screen.getByText("hello")).toBeInTheDocument();
  });

  it("fades the entering content in (motion-safe) and drops it under reduced motion", () => {
    const { container } = render(
      <MotionContent motionKey="a">
        <span>x</span>
      </MotionContent>,
    );
    const wrapper = container.firstElementChild as HTMLElement;
    // Opacity-only enter animation, guarded to motion-safe...
    expect(wrapper.className).toContain(
      "motion-safe:animate-[content-fade-in_var(--motion-duration-moderate)_var(--motion-ease-out)]",
    );
    // ...and dropped entirely under prefers-reduced-motion.
    expect(wrapper.className).toContain("motion-reduce:animate-none");
  });

  it("maps each tier to its duration token; defaults to moderate", () => {
    const { container: fast } = render(
      <MotionContent motionKey="a" tier="fast">
        <span>x</span>
      </MotionContent>,
    );
    expect((fast.firstElementChild as HTMLElement).className).toContain(
      "--motion-duration-fast",
    );

    const { container: slow } = render(
      <MotionContent motionKey="a" tier="slow">
        <span>x</span>
      </MotionContent>,
    );
    expect((slow.firstElementChild as HTMLElement).className).toContain(
      "--motion-duration-slow",
    );
  });

  it("remounts the subtree when motionKey changes (instant swap, only-final on retarget)", () => {
    // A keyed subtree means the DOM node identity changes on motionKey change —
    // the old content is gone at once and the new is in the tree immediately, so
    // semantics/focus never wait on the fade and rapid retarget shows only the
    // latest key.
    const { container, rerender } = render(
      <MotionContent motionKey="a">
        <span>first</span>
      </MotionContent>,
    );
    const firstNode = container.firstElementChild;
    rerender(
      <MotionContent motionKey="b">
        <span>second</span>
      </MotionContent>,
    );
    const secondNode = container.firstElementChild;
    expect(screen.getByText("second")).toBeInTheDocument();
    expect(screen.queryByText("first")).not.toBeInTheDocument();
    expect(secondNode).not.toBe(firstNode);
  });

  it("passes through caller-owned layout className (opacity-only stays the contract)", () => {
    const { container } = render(
      <MotionContent motionKey="a" className="flex min-h-0 flex-1 flex-col">
        <span>x</span>
      </MotionContent>,
    );
    expect((container.firstElementChild as HTMLElement).className).toContain(
      "flex min-h-0 flex-1 flex-col",
    );
  });
});
