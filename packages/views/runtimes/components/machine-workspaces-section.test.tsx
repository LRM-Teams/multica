// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import type { RuntimeAgentWorkspace } from "@multica/core/types";
import { MachineWorkspacesSection } from "./machine-workspaces-section";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes },
};

function wrap(ui: ReactNode) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {ui}
    </I18nProvider>,
  );
}

function makeWs(
  partial: Partial<RuntimeAgentWorkspace> & { dir_name: string },
): RuntimeAgentWorkspace {
  return {
    rel_path: `ws-id/agents/${partial.dir_name}`,
    orphan: false,
    ...partial,
  };
}

describe("MachineWorkspacesSection (LRM-1148)", () => {
  it("shows idle copy before first scan", () => {
    wrap(
      <MachineWorkspacesSection
        machineOnline
        primaryRuntimeId="rt-1"
        canUpdate
        scanned={false}
        loading={false}
        data={undefined}
        deletePending={false}
        onScan={vi.fn()}
        onDelete={vi.fn()}
      />,
    );
    expect(screen.getByTestId("machine-workspaces-idle")).toHaveTextContent(
      /Scan to list/i,
    );
    expect(screen.getByTestId("machine-scan-workspaces")).toHaveTextContent(
      "Rescan",
    );
  });

  it("renders name, status badge, cleaned path — no metrics placeholders", () => {
    wrap(
      <MachineWorkspacesSection
        machineOnline
        primaryRuntimeId="rt-1"
        canUpdate
        scanned
        loading={false}
        data={{
          runtime_id: "rt-1",
          status: "ok",
          items: [
            makeWs({
              dir_name: "546d9101-bd59-4745-8771-48505c1556bf",
              rel_path:
                "7beafc96-3c51-4fcc-9fe7-8c36ceb482ff/agents/546d9101-bd59-4745-8771-48505c1556bf",
              agent_id: "546d9101-bd59-4745-8771-48505c1556bf",
              agent_name: "Alice",
              orphan: false,
            }),
            makeWs({
              dir_name: "a91f0c22-dead-beef-cafe-000000000001",
              rel_path:
                "7beafc96-3c51-4fcc-9fe7-8c36ceb482ff/agents/a91f0c22-dead-beef-cafe-000000000001",
              orphan: true,
            }),
          ],
        }}
        deletePending={false}
        onScan={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByText("Alice")).toBeTruthy();
    expect(screen.getByTestId("machine-workspace-status-active")).toHaveTextContent(
      "Active",
    );
    expect(
      screen.getByTestId("machine-workspace-status-orphaned"),
    ).toHaveTextContent("Orphaned");
    expect(
      screen.getByText("agents/546d9101-bd59-4745-8771-48505c1556bf"),
    ).toBeTruthy();
    expect(screen.queryByText(/KB/i)).toBeNull();
    expect(screen.queryByText(/—/)).toBeNull();
    expect(screen.queryByText(/4\.0/)).toBeNull();
  });

  it("opens confirm dialog before delete; non-owner delete is disabled", () => {
    const onDelete = vi.fn();
    wrap(
      <MachineWorkspacesSection
        machineOnline
        primaryRuntimeId="rt-1"
        canUpdate={false}
        scanned
        loading={false}
        data={{
          runtime_id: "rt-1",
          status: "ok",
          items: [
            makeWs({
              dir_name: "dir-1",
              agent_name: "Alice",
              agent_id: "a1",
              orphan: false,
            }),
          ],
        }}
        deletePending={false}
        onScan={vi.fn()}
        onDelete={onDelete}
      />,
    );

    const del = screen.getByTestId("machine-workspace-delete-dir-1");
    expect(del).toBeDisabled();
    fireEvent.click(del);
    expect(onDelete).not.toHaveBeenCalled();
    expect(
      screen.queryByTestId("delete-agent-workspace-dialog"),
    ).toBeNull();
  });

  it("owner delete opens confirm then calls onDelete", () => {
    const onDelete = vi.fn();
    wrap(
      <MachineWorkspacesSection
        machineOnline
        primaryRuntimeId="rt-1"
        canUpdate
        scanned
        loading={false}
        data={{
          runtime_id: "rt-1",
          status: "ok",
          items: [
            makeWs({
              dir_name: "dir-1",
              agent_name: "Alice",
              agent_id: "a1",
              orphan: false,
            }),
          ],
        }}
        deletePending={false}
        onScan={vi.fn()}
        onDelete={onDelete}
      />,
    );

    fireEvent.click(screen.getByTestId("machine-workspace-delete-dir-1"));
    expect(
      screen.getByTestId("delete-agent-workspace-dialog"),
    ).toBeTruthy();
    fireEvent.click(screen.getByTestId("delete-agent-workspace-confirm"));
    expect(onDelete).toHaveBeenCalledWith("dir-1");
  });
});
