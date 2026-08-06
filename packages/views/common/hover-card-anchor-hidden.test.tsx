// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@multica/ui/components/ui/hover-card";

// #344: the profile hover card can render far from its trigger. Base UI sets
// `data-anchor-hidden` on the Positioner whenever ITS OWN collision
// detection doesn't trust the trigger's current position (e.g. mid-reflow,
// while message content above the trigger is still settling layout) — but
// that position never self-corrects once the trigger is actually visible
// again, so the popup can render stuck at a stale/fallback spot. Rather
// than trust a possibly-stale computed position, the popup hides itself
// whenever Base UI flags the anchor as untrusted, and reappears the instant
// a later, accurate recompute clears the flag.
//
// Base UI's real collision detection needs a real layout engine (not
// jsdom) to actually flip `data-anchor-hidden`, so this locks the CSS hook
// staying wired rather than exercising the real trigger — real-device
// verification (Iris) covers the visual behavior end to end.
describe("HoverCardContent — #344 anchor-hidden hook", () => {
  it("wires a visibility rule to Base UI's data-anchor-hidden state on the positioner", () => {
    render(
      <HoverCard open>
        <HoverCardTrigger render={<button type="button" />}>
          trigger
        </HoverCardTrigger>
        <HoverCardContent>popup content</HoverCardContent>
      </HoverCard>,
    );

    const popup = screen.getByText("popup content");
    // The Positioner is the Popup's parent inside the portal.
    const positioner = popup.parentElement;
    expect(positioner).not.toBeNull();
    expect(positioner?.className).toContain("data-anchor-hidden:invisible");
  });
});
