import { expect, type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";

const DEFAULT_QA_EMAIL = "qa-preview@multica.ai";
const DEFAULT_QA_NAME = "QA Preview";
const DEFAULT_QA_WORKSPACE_SLUG = "multica-qa";

interface PreviewLoginOptions {
  email?: string;
  name?: string;
  workspaceSlug?: string;
  workspaceName?: string;
  token?: string;
}

function required(value: string | undefined, name: string) {
  if (!value) throw new Error(`${name} is required for preview visual login`);
  return value;
}

function pathForWorkspace(workspaceSlug: string, path = "/issues") {
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  return `/${workspaceSlug}${normalizedPath}`;
}

/**
 * Log in with the shared QA human account for preview visual checks.
 *
 * The preferred preview path uses a short-lived QA_PREVIEW_TOKEN supplied by the
 * agent secret store. The DB fallback mirrors loginAsDefault: send-code, read the
 * OTP from verification_code, verify-code, then inject the returned token.
 */
export async function loginAsPreviewQA(page: Page, opts: PreviewLoginOptions = {}) {
  const workspaceSlug = opts.workspaceSlug ?? process.env.QA_PREVIEW_WORKSPACE_SLUG ?? DEFAULT_QA_WORKSPACE_SLUG;
  const token = opts.token ?? process.env.QA_PREVIEW_TOKEN;

  if (token) {
    // Seed token mode before the first app document evaluates. The web shell
    // decides cookie-vs-token auth during initial render, so setting localStorage
    // after /login loads can race and leave the session in cookie mode.
    await page.addInitScript((t) => localStorage.setItem("multica_token", t), token);
    await page.goto(pathForWorkspace(workspaceSlug));
    await page.waitForURL("**/issues", { timeout: 10000 });
    return workspaceSlug;
  }

  const api = new TestApiClient();
  await api.login(
    opts.email ?? process.env.QA_PREVIEW_EMAIL ?? DEFAULT_QA_EMAIL,
    opts.name ?? process.env.QA_PREVIEW_NAME ?? DEFAULT_QA_NAME,
  );
  const workspace = await api.ensureWorkspace(
    opts.workspaceName ?? process.env.QA_PREVIEW_WORKSPACE_NAME ?? "Multica QA",
    workspaceSlug,
  );

  const verifiedToken = required(api.getToken() ?? undefined, "verified QA token");
  await page.goto("/login");
  await page.evaluate((t) => localStorage.setItem("multica_token", t), verifiedToken);
  await page.goto(pathForWorkspace(workspace.slug));
  await page.waitForURL("**/issues", { timeout: 10000 });
  return workspace.slug;
}

export async function openPreviewIssue(page: Page, workspaceSlug: string, issueId: string) {
  await page.goto(pathForWorkspace(workspaceSlug, `/issues/${issueId}`));
  await expect(page.locator("body")).not.toContainText(/login|sign in/i, { timeout: 10000 });
}
