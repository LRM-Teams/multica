import { test, expect } from "@playwright/test";
import { loginAsDefault } from "./helpers";

test.describe("Navigation", () => {
  test.beforeEach(async ({ page }) => {
    await loginAsDefault(page);
  });

  test("sidebar navigation works", async ({ page }) => {
    await page.getByRole("link", { name: "Activity" }).click();
    await page.waitForURL("**/inbox");
    await expect(page).toHaveURL(/\/inbox/);

    await page.getByRole("link", { name: "Members" }).click();
    await page.waitForURL("**/members");
    await expect(page).toHaveURL(/\/members/);

    await page.getByRole("link", { name: "Issues" }).click();
    await page.waitForURL("**/issues");
    await expect(page).toHaveURL(/\/issues/);
  });

  test("settings page loads via sidebar", async ({ page }) => {
    await page.getByRole("link", { name: "Settings" }).click();
    await page.waitForURL("**/settings");

    await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Profile" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "General" })).toBeVisible();
  });

  test("members page shows the workspace directory", async ({ page }) => {
    await page.getByRole("link", { name: "Members" }).click();
    await page.waitForURL("**/members");

    await expect(page.getByRole("heading", { name: "Members" })).toBeVisible();
  });
});
