import { expect, test } from "@playwright/test";
import { loginAsPreviewQA, openPreviewIssue } from "./preview-auth";

const DEFAULT_VISUAL_ISSUE_ID = "03e5169c-dd1a-46f4-97d6-9184c018f642";

test("preview visual smoke captures an authenticated issue page", async ({ page }, testInfo) => {
  const workspaceSlug = await loginAsPreviewQA(page);
  const issueId = process.env.QA_VISUAL_ISSUE_ID ?? DEFAULT_VISUAL_ISSUE_ID;

  await openPreviewIssue(page, workspaceSlug, issueId);
  await expect(page.locator("body")).toContainText(/LRM-|Issue|问题|视觉/, { timeout: 10000 });

  await page.screenshot({
    path: testInfo.outputPath(`preview-visual-${issueId}.png`),
    fullPage: true,
  });
});
