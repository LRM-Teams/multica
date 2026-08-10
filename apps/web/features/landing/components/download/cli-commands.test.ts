import { describe, expect, it } from "vitest";
import { landingCLICommands } from "./cli-commands";

describe("landingCLICommands", () => {
  it("uses stable and a workspace-scoped setup command in production", () => {
    expect(
      landingCLICommands({
        environment: "production",
        appUrl: "https://www.leagent.me",
        apiUrl: "https://api.leagent.me",
      }),
    ).toEqual({
      installCmd:
        "curl -fsSL https://cdn.leagent.me/computer/install.sh | bash",
      setupCmd: "multica setup /<workspace-slug>",
    });
  });

  it("uses the deployment-pinned Computer release and explicit endpoints in test", () => {
    expect(
      landingCLICommands({
        environment: "test",
        appUrl: "https://82.157.184.89/",
        apiUrl: "https://82.157.184.89/",
        computerVersion: "v0.4.24-alpha.2",
      }),
    ).toEqual({
      installCmd:
        "curl -fsSL https://cdn.leagent.me/computer/install.sh | bash -s -- --version v0.4.24-alpha.2",
      setupCmd:
        "multica setup --environment test --server-url https://82.157.184.89 --app-url https://82.157.184.89 /<workspace-slug>",
    });
  });
});
