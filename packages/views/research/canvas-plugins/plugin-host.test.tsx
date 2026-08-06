import { act, cleanup, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { ReactNode } from "react";
import { renderWithI18n } from "../../test/i18n";
import { ResearchCanvasPluginSlot } from "./plugin-host";
import { ResearchCanvasPluginSlots } from "./plugin-slots";
import type {
  PanelPluginProps,
  ResearchCanvasPluginContext,
  ResearchCanvasPluginRegistration,
} from "./types";

afterEach(() => cleanup());

const EMPTY_CONTEXT: ResearchCanvasPluginContext = {
  nodes: [],
  selectedNodeId: null,
  reducedMotion: false,
};

function panelProps(): PanelPluginProps {
  return { context: EMPTY_CONTEXT };
}

function registration(
  id: string,
  slot: "insight" | "dispute" | "motion" | "executionOverlay" | "trajectoryJump",
  loader: () => Promise<{ default: () => ReactNode }>,
): ResearchCanvasPluginRegistration<any> {
  return { id, slot: slot as never, load: loader };
}

function Box({ children }: { children: string }): ReactNode {
  return <div data-testid={`box-${children}`}>{children}</div>;
}

function renderSlot(regs: ResearchCanvasPluginRegistration<any>[], slot: string) {
  return renderWithI18n(
    <ResearchCanvasPluginSlots registrations={regs}>
      <ResearchCanvasPluginSlot slot={slot as never} props={panelProps()} />
    </ResearchCanvasPluginSlots>,
  );
}

describe("ResearchCanvasPluginSlot — FE-10 AC #1/#2 (isolated lazy slots)", () => {
  it("renders the generic absent fallback when a slot has no registration", () => {
    renderSlot([], "insight");
    expect(
      screen.getByTestId("research-canvas-plugin-absent-insight"),
    ).toBeTruthy();
    // No loading frame, no error frame.
    expect(screen.queryByTestId("research-canvas-plugin-loading")).toBeNull();
    expect(screen.queryByTestId("research-canvas-plugin-error")).toBeNull();
  });

  it("shows the loading frame while a registered lazy chunk is pending, then the plugin", async () => {
    let resolve!: (v: { default: () => ReactNode }) => void;
    const gate = new Promise<{ default: () => ReactNode }>((r) => (resolve = r));
    renderSlot(
      [registration("insight-a", "insight", () => gate)],
      "insight",
    );
    // Suspense fallback is shown synchronously while the chunk is pending.
    expect(screen.getByTestId("research-canvas-plugin-loading")).toBeTruthy();
    // Resolve the lazy chunk.
    await act(async () => {
      resolve({ default: function InsightA() {
        return <Box>insight-a-content</Box>;
      } });
      await gate;
    });
    expect(await screen.findByTestId("box-insight-a-content")).toBeTruthy();
    expect(screen.queryByTestId("research-canvas-plugin-loading")).toBeNull();
  });

  it("renders the error fallback reporting the plugin name, and retry recovers", async () => {
    let shouldFail = true;
    const reg = registration("insight-fail", "insight", () => {
      if (shouldFail) {
        return Promise.reject(new Error("chunk exploded"));
      }
      return Promise.resolve({ default: function InsightOk() {
        return <Box>insight-ok</Box>;
      } });
    });
    renderSlot([reg], "insight");
    const chip = await screen.findByTestId("research-canvas-plugin-error");
    expect(chip).toBeTruthy();
    // AC: the failing plugin name is reported, not just a generic message.
    expect(chip.textContent).toContain("insight-fail");
    // Retry: flip the loader to succeed and press retry.
    shouldFail = false;
    const retry = screen.getByRole("button", { name: "Retry" });
    await act(async () => {
      retry.click();
    });
    expect(await screen.findByTestId("box-insight-ok")).toBeTruthy();
    expect(screen.queryByTestId("research-canvas-plugin-error")).toBeNull();
  });

  it("isolates a failed slot — the whole canvas and sibling slots keep rendering", async () => {
    renderWithI18n(
      <ResearchCanvasPluginSlots
        registrations={[
          registration("bad", "dispute", () =>
            Promise.reject(new Error("dispute boom")),
          ),
          registration("good", "insight", () =>
            Promise.resolve({ default: function InsightGood() {
              return <Box>insight-good</Box>;
            } }),
          ),
        ]}
      >
        {/* A host that would blank the canvas if it weren't isolated. */}
        <HostShell label="canvas-shell">
          <ResearchCanvasPluginSlot slot="dispute" props={panelProps()} />
          <ResearchCanvasPluginSlot slot="insight" props={panelProps()} />
        </HostShell>
      </ResearchCanvasPluginSlots>,
    );
    expect(await screen.findByTestId("research-canvas-plugin-error")).toBeTruthy();
    // The sibling insight slot mounted and rendered despite the dispute failure.
    expect(await screen.findByTestId("box-insight-good")).toBeTruthy();
    expect(screen.getByTestId("canvas-shell")).toBeTruthy();
  });
});

function HostShell({ label, children }: { label: string; children: ReactNode }): ReactNode {
  return <div data-testid={label}>{children}</div>;
}
