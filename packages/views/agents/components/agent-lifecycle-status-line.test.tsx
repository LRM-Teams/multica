import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentLifecycleStatusLine } from "./agent-lifecycle-status-line";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: {
      lifecycle: Record<string, string>;
    }) => string) =>
      selector({
        lifecycle: {
          starting: "Starting",
          stopped: "Stopped",
          crashed: "Crashed",
          disconnected: "Disconnected",
          offline: "Offline",
        },
      }),
  }),
}));

describe("AgentLifecycleStatusLine", () => {
  it("renders nothing for idle/working", () => {
    const { container } = render(<AgentLifecycleStatusLine status="idle" />);
    expect(container).toBeEmptyDOMElement();
    const { container: c2 } = render(
      <AgentLifecycleStatusLine status="working" />,
    );
    expect(c2).toBeEmptyDOMElement();
  });

  it("renders stopped with square marker", () => {
    render(<AgentLifecycleStatusLine status="stopped" />);
    const mark = screen.getByTestId("agent-lifecycle-status");
    expect(mark).toHaveTextContent("Stopped");
    expect(mark).toHaveAttribute("data-shape", "square");
    const marker = mark.querySelector("[aria-hidden]");
    expect(marker?.className).toContain("rounded-[1px]");
  });

  it("renders starting with brand tone", () => {
    render(<AgentLifecycleStatusLine status="starting" />);
    const mark = screen.getByTestId("agent-lifecycle-status");
    expect(mark).toHaveTextContent("Starting");
    expect(mark).toHaveAttribute("data-shape", "dot");
    expect(mark.className).toContain("text-brand");
  });
});
