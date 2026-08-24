import { test, expect } from "@playwright/test";
import { loginAsDefault } from "./helpers";

test.describe("Settings", () => {
  test("updating workspace name reflects in sidebar immediately", async ({
    page,
  }) => {
    await loginAsDefault(page);

    // Read the current workspace name from the sidebar
    const sidebarName = page.getByTestId("workspace-switcher-name");
    const originalName = await sidebarName.innerText();

    await page.getByRole("link", { name: "Settings" }).click();
    await page.waitForURL("**/settings");
    await page.getByRole("tab", { name: "General" }).click();

    const generalPanel = page.getByRole("tabpanel", { name: "General" });
    const nameInput = generalPanel.locator('input[type="text"]').first();
    await nameInput.clear();
    const newName = "Renamed WS " + Date.now();
    await nameInput.fill(newName);

    // Save
    await generalPanel.getByRole("button", { name: "Save" }).click();

    await expect(page.getByText("Workspace settings saved").last()).toBeVisible({ timeout: 5000 });

    // Sidebar should reflect the new name WITHOUT page refresh
    await expect(sidebarName).toContainText(newName);

    // Restore original name so other tests aren't affected
    await nameInput.clear();
    await nameInput.fill(originalName.trim());
    await generalPanel.getByRole("button", { name: "Save" }).click();
    // The first success toast can still be exiting when the restore toast appears.
    await expect(page.getByText("Workspace settings saved").last()).toBeVisible({ timeout: 5000 });
  });
});
