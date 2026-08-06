import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { PlanBillingPage } from "./plan-billing-page";

describe("PlanBillingPage", () => {
  it("defaults to yearly billing and shows Pro annual price", () => {
    renderWithI18n(<PlanBillingPage />);

    expect(screen.getByRole("heading", { name: "Workspace plans" })).toBeTruthy();
    expect(screen.getByText("$8.80")).toBeTruthy();
    expect(screen.getByText(/Billed yearly/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Yearly/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("switches to monthly Pro price", async () => {
    const user = userEvent.setup();
    renderWithI18n(<PlanBillingPage />);

    await user.click(screen.getByRole("button", { name: "Monthly" }));
    expect(screen.getByText("$10")).toBeTruthy();
    expect(screen.getByText("Billed monthly")).toBeTruthy();
  });

  it("renders Chinese copy", () => {
    renderWithI18n(<PlanBillingPage />, { locale: "zh-Hans" });

    expect(screen.getByRole("heading", { name: "工作区方案" })).toBeTruthy();
    expect(screen.getByText("方案与账单")).toBeTruthy();
    expect(screen.getByRole("button", { name: /年付/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });
});
