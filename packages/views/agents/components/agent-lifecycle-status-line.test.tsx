import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentLifecycleStatusLine } from "./agent-lifecycle-status-line";

vi.mock("../resolve-agent-lifecycle-status", () => ({
  resolveAgentLifecycleStatus: (status: string | null | undefined) => {
    if (status === "idle" || status === "working") return null;
    if (status === "stopped") {
      return {
        label: "Stopped",
        shape: "square",
        toneClass: "text-muted-foreground",
        dotClass: "bg-muted-foreground/40",
      };
    }
    if (status === "starting") {
      return {
        label: "Starting",
        shape: "dot",
        toneClass: "text-brand",
        dotClass: "bg-brand",
      };
    }
    if (status === "crashed") {
      return {
        label: "Crashed",
        shape: "dot",
        toneClass: "text-muted-foreground",
        dotClass: "bg-muted-foreground/40",
      };
    }
    return {
      label: "Offline",
      shape: "dot",
      toneClass: "text-muted-foreground",
      dotClass: "bg-muted-foreground/40",
    };
  },
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({ t: () => "" }),
}));

describe("AgentLifecycleStatusLine", () => {
  it("renders nothing for idle/working/offline/legacy disconnected/missing", () => {
    for (const status of ["idle", "working", "offline", "disconnected", null, undefined] as const) {
      const { container } = render(
        <AgentLifecycleStatusLine status={status} />,
      );
      expect(container).toBeEmptyDOMElement();
    }
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

  it("renders crashed as a gray dot", () => {
    render(<AgentLifecycleStatusLine status="crashed" />);
    expect(screen.getByTestId("agent-lifecycle-status")).toHaveTextContent(
      "Crashed",
    );
    expect(screen.getByTestId("agent-lifecycle-status")).toHaveAttribute(
      "data-shape",
      "dot",
    );
  });
});
