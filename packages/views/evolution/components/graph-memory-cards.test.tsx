// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enEvolution from "../../locales/en/evolution.json";
import { LegacyCurationNotApplicableCard } from "./graph-memory-cards";

const TEST_RESOURCES = { en: { evolution: enEvolution } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("LegacyCurationNotApplicableCard", () => {
  it("states that legacy curation is not applicable in graph mode", () => {
    render(<LegacyCurationNotApplicableCard />, { wrapper: I18nWrapper });
    expect(screen.getByText(enEvolution.legacyCurationNotApplicable)).toBeTruthy();
    expect(screen.getByText(enEvolution.legacyCurationNotApplicableHint)).toBeTruthy();
  });
});
