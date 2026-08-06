// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Skill } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSkills from "../../locales/en/skills.json";
import { SkillGrantSection } from "./skill-grant-section";

const promoteSkill = vi.fn();
const listSkillPromotions = vi.fn();
const listChannels = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    promoteSkill: (...args: unknown[]) => promoteSkill(...args),
    listSkillPromotions: (...args: unknown[]) => listSkillPromotions(...args),
    listChannels: (...args: unknown[]) => listChannels(...args),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const TEST_RESOURCES = {
  en: { common: enCommon, skills: enSkills },
};

function makeSkill(partial: Partial<Skill> = {}): Skill {
  return {
    id: "skill-1",
    workspace_id: "ws-1",
    name: "demo-skill",
    description: "demo",
    content: "# demo",
    files: [],
    config: {},
    created_by: null,
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    grant_level: "agent",
    channel_id: null,
    capabilities: {
      can_promote_to_channel: true,
      can_promote_to_workspace: true,
    },
    ...partial,
  };
}

function renderGrant(skill: Skill) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <SkillGrantSection skill={skill} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("SkillGrantSection", () => {
  beforeEach(() => {
    promoteSkill.mockReset();
    listSkillPromotions.mockReset();
    listChannels.mockReset();
    listChannels.mockResolvedValue([
      {
        id: "ch-1",
        workspace_id: "ws-1",
        name: "dev-group",
        kind: "group",
        description: null,
        lark_chat_id: null,
        created_by: "u1",
        created_at: "2026-08-01T00:00:00Z",
        updated_at: "2026-08-01T00:00:00Z",
      },
    ]);
    listSkillPromotions.mockResolvedValue({ items: [] });
  });



  it("hides promote buttons when capabilities are false/missing", () => {
    renderGrant(
      makeSkill({
        capabilities: {
          can_promote_to_channel: false,
          can_promote_to_workspace: false,
        },
      }),
    );
    expect(
      screen.queryByRole("button", { name: "Authorize to channel" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Promote to workspace default" }),
    ).not.toBeInTheDocument();
  });
});
