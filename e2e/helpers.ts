import { type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";

const DEFAULT_E2E_NAME = "E2E User";
const DEFAULT_E2E_EMAIL = `e2e-${process.pid}@multica.ai`;
const DEFAULT_E2E_WORKSPACE = `e2e-workspace-${process.pid}`;

/**
 * Log in as the default E2E user and ensure the workspace exists first.
 * Authenticates via API (send-code → DB read → verify-code), then injects
 * the token into localStorage so the browser session is authenticated.
 *
 * Returns the E2E workspace slug so callers can build workspace-scoped URLs.
 */
export async function loginAsDefault(page: Page): Promise<string> {
  const api = new TestApiClient();
  await api.login(DEFAULT_E2E_EMAIL, DEFAULT_E2E_NAME);
  const workspace = await api.ensureWorkspace(
    "E2E Workspace",
    DEFAULT_E2E_WORKSPACE,
  );
  await api.ensureWorkspaceReady(workspace);

  const token = api.getToken();
  if (!token) throw new Error("default E2E client is not authenticated");
  await page.addInitScript((t) => {
    localStorage.setItem("multica_token", t);
  }, token);
  await page.goto(`/${workspace.slug}/issues`);
  await page.waitForURL("**/issues", { timeout: 30000 });
  await page.waitForLoadState("networkidle", { timeout: 30000 });
  await page.getByRole("heading", { name: "Issues" }).waitFor({
    state: "visible",
    timeout: 30000,
  });
  return workspace.slug;
}

/**
 * Create a TestApiClient logged in as the default E2E user.
 * Call api.cleanup() in afterEach to remove test data created during the test.
 */
export async function createTestApi(): Promise<TestApiClient> {
  const api = new TestApiClient();
  await api.login(DEFAULT_E2E_EMAIL, DEFAULT_E2E_NAME);
  const workspace = await api.ensureWorkspace("E2E Workspace", DEFAULT_E2E_WORKSPACE);
  await api.ensureWorkspaceReady(workspace);
  return api;
}

export async function openWorkspaceMenu(page: Page) {
  await page.locator('[data-slot="sidebar-header"] button').click();
  await page.getByRole("menu").waitFor({ state: "visible" });
}
