// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentRuntimeConfig } from "@multica/core/types";
import enAgents from "../../../locales/en/agents.json";
import { ComputerInfoRow } from "./computer-info-row";

function makeComputer(
  overrides: Partial<NonNullable<AgentRuntimeConfig["computer"]>> = {},
): AgentRuntimeConfig["computer"] {
  return {
    daemon_id: "daemon-1",
    name: "s144",
    connected: true,
    cli_version: "0.3.92",
    ...overrides,
  };
}

function renderRow(computer: AgentRuntimeConfig["computer"]) {
  render(
    <I18nProvider locale="en" resources={{ en: { agents: enAgents } }}>
      <ComputerInfoRow computer={computer} />
    </I18nProvider>,
  );
}

// Frank, 2026-08-01: standalone info row, deliberately independent of the
// Runtime/code-agent picker row — it must not disappear or get relabeled
// when that picker's own vocabulary changes.
//
// 2026-08-21: it also stopped resolving anything. The server assembles this
// row now (GET /api/agents/{id}/runtime-config), because resolving it here
// meant joining a runtime id against a list that omits another member's
// private runtime — which silently missed and claimed "No computer".
describe("ComputerInfoRow (2026-08-01)", () => {
  // Online, the dot carries the state and the version gets the space —
  // spelling out "Connected" was pushing the machine name aside. The state is
  // still announced, so nothing depends on colour alone.
  it("shows the machine and version, with the state on the dot", () => {
    renderRow(makeComputer());
    expect(screen.getByText("s144")).toBeInTheDocument();
    expect(screen.getByText("v0.3.92")).toBeInTheDocument();
    // Announced to a screen reader, invisible on screen — the dot carries it.
    expect(screen.getByText("Connected")).toHaveClass("sr-only");
  });

  // Offline differs by the dot's colour and nothing else: grey reads as
  // offline without spending a word on it. The state is still announced.
  it("announces Disconnected on the dot without spelling it out", () => {
    renderRow(makeComputer({ connected: false }));
    expect(screen.getByText("Disconnected")).toHaveClass("sr-only");
    expect(screen.getByText("s144")).toBeInTheDocument();
    expect(screen.getByText("v0.3.92")).toBeInTheDocument();
  });

  it("renders the name the server resolved, never re-deriving one", () => {
    // Frank 2026-08-02: never the "Provider (host)" code-agent string. The
    // fallback chain now lives on the server (resolveComputerName), so this
    // component must render whatever it is handed.
    renderRow(makeComputer({ name: "someone-elses-box" }));
    expect(screen.getByText("someone-elses-box")).toBeInTheDocument();
  });

  it("omits the version when the Computer has not reported one", () => {
    renderRow(makeComputer({ cli_version: undefined }));
    expect(screen.getByText("s144")).toBeInTheDocument();
    expect(screen.queryByText(/^v\d/)).not.toBeInTheDocument();
  });

  it("shows a 'no computer' placeholder when the agent has no bound runtime", () => {
    renderRow(null);
    expect(screen.getByText("No computer")).toBeInTheDocument();
  });
});
