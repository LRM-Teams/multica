import type { ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enOnboarding from "../../locales/en/onboarding.json";
import { StepHeader } from "./step-header";

const resources = { en: { common: enCommon, onboarding: enOnboarding } };

function renderHeader(
  props: Partial<ComponentProps<typeof StepHeader>> = {},
) {
  return render(
    <I18nProvider locale="en" resources={resources}>
      <StepHeader currentStep="workspace" {...props} />
    </I18nProvider>,
  );
}

describe("StepHeader", () => {
  it("renders canonical progress and invokes back navigation", async () => {
    const user = userEvent.setup();
    const onBack = vi.fn();
    renderHeader({ onBack });

    expect(screen.getByRole("progressbar", { name: "Step 4 of 5" })).toHaveAttribute(
      "aria-valuenow",
      "4",
    );
    await user.click(screen.getByRole("button", { name: "Back" }));
    expect(onBack).toHaveBeenCalledOnce();
  });

  it("omits the back control when navigation is unavailable", () => {
    renderHeader();
    expect(screen.queryByRole("button", { name: "Back" })).not.toBeInTheDocument();
  });

  it("disables back navigation while the current step is pending", () => {
    renderHeader({ onBack: vi.fn(), backDisabled: true });
    expect(screen.getByRole("button", { name: "Back" })).toBeDisabled();
  });
});
