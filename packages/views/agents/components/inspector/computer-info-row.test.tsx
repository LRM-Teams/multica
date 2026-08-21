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
  it("shows Connected + machine label + version", () => {
    renderRow(makeComputer());
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("s144")).toBeInTheDocument();
    expect(screen.getByText("v0.3.92")).toBeInTheDocument();
  });

  it("shows Disconnected when the daemon has no live runner socket", () => {
    renderRow(makeComputer({ connected: false }));
    expect(screen.getByText("Disconnected")).toBeInTheDocument();
    expect(screen.getByText("s144")).toBeInTheDocument();
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
    expect(screen.queryByText(/^v/)).not.toBeInTheDocument();
  });

  it("shows a 'no computer' placeholder when the agent has no bound runtime", () => {
    renderRow(null);
    expect(screen.getByText("No computer")).toBeInTheDocument();
  });
});
