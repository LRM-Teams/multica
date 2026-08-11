import { test, expect } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

test.describe("Comments", () => {
  let api: TestApiClient;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();
    await api.createIssue("E2E Comment Test " + Date.now());
    await loginAsDefault(page);
  });

  test.afterEach(async () => {
    await api.cleanup();
  });

  test("can add a comment on an issue", async ({ page }) => {
    // Wait for issues to load and click first one. `*=` matches both legacy
    // `/issues/{id}` and URL-refactored `/{slug}/issues/{id}` hrefs.
    const issueLink = page.locator('a[href*="/issues/"]').first();
    await expect(issueLink).toBeVisible({ timeout: 5000 });
    await issueLink.click();
    await page.waitForURL(/\/issues\/[\w-]+/);

    // Wait for issue detail to load
    await expect(page.locator("text=Properties")).toBeVisible();

    // Type a comment
    const commentText = "E2E comment " + Date.now();
    const composer = page.getByTestId("issue-comment-composer");
    const deferredEditor = composer.getByTestId("content-editor-deferred");
    if (await deferredEditor.isVisible()) await deferredEditor.click();
    await composer.locator('[contenteditable="true"]').fill(commentText);

    await composer.locator("button").last().click();

    // Comment should appear in the activity section
    await expect(page.getByTestId("virtuoso-item-list").getByText(commentText)).toBeVisible({
      timeout: 5000,
    });
  });

  test("comment submit button is disabled when empty", async ({ page }) => {
    const issueLink = page.locator('a[href*="/issues/"]').first();
    await expect(issueLink).toBeVisible({ timeout: 5000 });
    await issueLink.click();
    await page.waitForURL(/\/issues\/[\w-]+/);

    await expect(page.locator("text=Properties")).toBeVisible();

    const submitBtn = page.getByTestId("issue-comment-composer").locator("button").last();
    await expect(submitBtn).toBeDisabled();
  });
});
