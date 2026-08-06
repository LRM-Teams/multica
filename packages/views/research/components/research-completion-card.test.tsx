import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { ResearchCompletionCard } from "./research-completion-card";

const here = path.dirname(fileURLToPath(import.meta.url));
const SRC = "research-completion-card.tsx";

function readSrc() {
  return fs.readFileSync(path.join(here, SRC), "utf8");
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        completion_guide: {
          done_title: "Research complete",
          done_body: "Report is ready.",
          failed_title: "Research unfinished",
          failed_body: "This run did not finish.",
          view_report: "View report",
          new_research: "New research",
          home: "Home",
          dismiss: "Dismiss",
          done_badge: "Done",
          failed_badge: "Failed",
        },
      }),
  }),
}));

describe("ResearchCompletionCard (LRM-832)", () => {
  it("renders done copy and wires primary actions", () => {
    const onViewReport = vi.fn();
    const onNewResearch = vi.fn();
    const onHome = vi.fn();
    const onDismiss = vi.fn();
    render(
      <ResearchCompletionCard
        kind="done"
        onViewReport={onViewReport}
        onNewResearch={onNewResearch}
        onHome={onHome}
        onDismiss={onDismiss}
      />,
    );
    const card = screen.getByTestId("research-completion-card");
    expect(card).toHaveAttribute("data-completion-kind", "done");
    expect(screen.getByText("Research complete")).toBeTruthy();
    fireEvent.click(screen.getByText("View report"));
    fireEvent.click(screen.getByText("New research"));
    fireEvent.click(screen.getByText("Home"));
    expect(onViewReport).toHaveBeenCalledTimes(1);
    expect(onNewResearch).toHaveBeenCalledTimes(1);
    expect(onHome).toHaveBeenCalledTimes(1);
  });

  it("uses distinct failed copy", () => {
    render(
      <ResearchCompletionCard
        kind="failed"
        onViewReport={() => {}}
        onNewResearch={() => {}}
        onHome={() => {}}
        onDismiss={() => {}}
      />,
    );
    expect(screen.getByTestId("research-completion-card")).toHaveAttribute(
      "data-completion-kind",
      "failed",
    );
    expect(screen.getByText("Research unfinished")).toBeTruthy();
    expect(screen.getByText("This run did not finish.")).toBeTruthy();
    expect(screen.queryByText("Research complete")).toBeNull();
  });

  // LRM-1244 — no fullscreen dismiss scrim (same root cause as LRM-1243 / #2082).
  // aria-hidden + tabIndex=-1 is still focusable; native dialog focusing steps
  // parked initial focus on the invisible layer. Delete the node; gutter click
  // on the dialog box itself dismisses.
  it("exposes no dismiss scrim; gutter click on dialog dismisses", () => {
    const onDismiss = vi.fn();
    render(
      <ResearchCompletionCard
        kind="done"
        onViewReport={() => {}}
        onNewResearch={() => {}}
        onHome={() => {}}
        onDismiss={onDismiss}
      />,
    );
    const dialog = screen.getByTestId("research-completion-card");
    expect(dialog.tagName).toBe("DIALOG");
    expect(dialog.querySelector("button.absolute.inset-0")).toBeNull();
    expect(
      dialog.querySelector('[aria-hidden="true"][tabindex="-1"]'),
    ).toBeNull();

    const namedDismiss = screen.getAllByRole("button", { name: "Dismiss" });
    expect(namedDismiss).toHaveLength(1);

    fireEvent.click(dialog);
    expect(onDismiss).toHaveBeenCalledTimes(1);

    onDismiss.mockClear();
    const card = dialog.querySelector("[role='document']");
    expect(card).toBeTruthy();
    fireEvent.click(card!);
    expect(onDismiss).not.toHaveBeenCalled();
  });

  // LRM-1301 — labeled close + action icons must not dual-announce; status glyphs too.
  it("source: dismiss/action/status lucide icons declare aria-hidden", () => {
    const src = readSrc();
    expect(src).toMatch(/<X\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<FileText\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Plus\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Home\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<CheckCircle2\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<AlertCircle\b[\s\S]{0,60}aria-hidden/);
  });

  it("render: named buttons keep accessible names; icons are aria-hidden", () => {
    render(
      <ResearchCompletionCard
        kind="done"
        onViewReport={() => {}}
        onNewResearch={() => {}}
        onHome={() => {}}
        onDismiss={() => {}}
      />,
    );
    const dialog = screen.getByTestId("research-completion-card");

    const dismiss = within(dialog).getByRole("button", { name: "Dismiss" });
    expect(dismiss.querySelector("svg")).toHaveAttribute("aria-hidden", "true");

    const viewReport = within(dialog).getByRole("button", {
      name: "View report",
    });
    expect(viewReport.querySelector("svg")).toHaveAttribute(
      "aria-hidden",
      "true",
    );

    const newResearch = within(dialog).getByRole("button", {
      name: "New research",
    });
    expect(newResearch.querySelector("svg")).toHaveAttribute(
      "aria-hidden",
      "true",
    );

    const home = within(dialog).getByRole("button", { name: "Home" });
    expect(home.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });
});
