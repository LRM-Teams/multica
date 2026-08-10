import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { configStore } from "@multica/core/config";
import enCommon from "../locales/en/common.json";
import enWorkspace from "../locales/en/workspace.json";
import { CreateWorkspaceForm } from "./create-workspace-form";

vi.mock("@multica/core/workspace/mutations", () => ({
  useCreateWorkspace: () => ({ mutate: vi.fn(), isPending: false }),
}));

const TEST_RESOURCES = { en: { common: enCommon, workspace: enWorkspace } };

describe("CreateWorkspaceForm", () => {
  beforeEach(() => {
    configStore.getState().setDaemonConfig({
      environment: "production",
      daemonAppUrl: "https://www.leagent.me",
    });
  });

  it("shows the configured test app host instead of the production domain", () => {
    configStore.getState().setDaemonConfig({
      environment: "test",
      daemonAppUrl: "https://82.157.184.89/",
    });

    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CreateWorkspaceForm onSuccess={vi.fn()} />
      </I18nProvider>,
    );

    expect(screen.getByText("82.157.184.89/")).toBeInTheDocument();
    expect(screen.queryByText("leagent.me/")).not.toBeInTheDocument();
  });
});
