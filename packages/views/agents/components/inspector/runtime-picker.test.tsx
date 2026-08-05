// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentRuntime, MemberWithUser } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";
import enIssues from "../../../locales/en/issues.json";
import { RuntimePicker } from "./runtime-picker";

vi.mock("../../../runtimes/components/provider-logo", () => ({
  ProviderLogo: () => <span data-testid="provider-logo" />,
  knownProviderLabel: (provider: string) => {
    const labels: Record<string, string> = {
      cursor: "Cursor",
      kiro: "Kiro",
      codex: "Codex CLI",
      pi: "Pi",
    };
    return labels[provider];
  },
}));

vi.mock("../../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents, issues: enIssues },
};

const ME = "user-me";
const members: MemberWithUser[] = [];

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Cursor (ubuntu)",
    display_name: null,
    runtime_mode: "local",
    provider: "cursor",
    status: "online",
    device_info: "ubuntu",
    device_name: null,
    metadata: {},
    last_seen_at: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    owner_id: ME,
    computer_connected: true,
    ...overrides,
  } as AgentRuntime;
}

function renderPicker(
  props: Partial<React.ComponentProps<typeof RuntimePicker>> & {
    value: string;
    runtimes: AgentRuntime[];
  },
) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <RuntimePicker
        members={members}
        currentUserId={ME}
        canEdit
        onChange={() => {}}
        {...props}
      />
    </I18nProvider>,
  );
}

describe("RuntimePicker cross-computer moves", () => {
  beforeEach(() => {
    cleanup();
  });
  afterEach(() => {
    cleanup();
  });

  it("includes eligible runtimes from other computers even when hostname text overlaps", () => {
    const onUbuntu = makeRuntime({
      id: "rt-ubuntu-kiro",
      daemon_id: "daemon-ubuntu",
      name: "Kiro (ubuntu)",
      provider: "kiro",
      display_name: "ubuntu",
    });
    const onKiroBox = makeRuntime({
      id: "rt-kiro-codex",
      daemon_id: "daemon-kiro",
      name: "Codex CLI (kiro)",
      provider: "codex",
      display_name: "kiro",
      device_info: "ubuntu · codex",
    });
    const peerOnUbuntu = makeRuntime({
      id: "rt-ubuntu-cursor",
      daemon_id: "daemon-ubuntu",
      name: "Cursor (ubuntu)",
      provider: "cursor",
      display_name: "ubuntu",
    });

    renderPicker({
      value: onUbuntu.id,
      boundRuntimeId: onUbuntu.id,
      runtimes: [onUbuntu, onKiroBox, peerOnUbuntu],
    });

    fireEvent.click(screen.getByRole("button"));
    // Trigger + list row both show the selected brand.
    expect(screen.getAllByText("Kiro").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Cursor")).toBeInTheDocument();
    expect(screen.getByText("Codex CLI")).toBeInTheDocument();
  });

  it("does not let boundRuntimeId hide a cross-computer draft selection", () => {
    const onUbuntu = makeRuntime({
      id: "rt-ubuntu",
      daemon_id: "daemon-ubuntu",
      provider: "cursor",
      name: "Cursor (ubuntu)",
    });
    const onKiro = makeRuntime({
      id: "rt-kiro",
      daemon_id: "daemon-kiro",
      provider: "kiro",
      name: "Kiro (kiro)",
      display_name: "kiro",
    });

    renderPicker({
      value: onKiro.id,
      boundRuntimeId: onUbuntu.id,
      runtimes: [onUbuntu, onKiro],
    });

    fireEvent.click(screen.getByRole("button"));
    // Trigger renders the draft brand and both computers remain selectable.
    const optionLabels = screen
      .getAllByText(/^(Cursor|Kiro)$/)
      .filter((el) => el.className.includes("text-sm"));
    expect(optionLabels.map((el) => el.textContent)).toEqual([
      "Cursor",
      "Kiro",
    ]);
  });
});
