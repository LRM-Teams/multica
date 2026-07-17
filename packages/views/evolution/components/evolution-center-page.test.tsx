import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithI18n } from "../../test/i18n";
import { useEvolutionCopy } from "./evolution-center-page";

function FailureLabel() {
  const copy = useEvolutionCopy();
  return <span>{copy("failures").toLowerCase()}</span>;
}

describe("evolution copy", () => {
  it("uses the i18n instance supplied by the application provider", () => {
    renderWithI18n(<FailureLabel />, { locale: "zh-Hans" });

    expect(screen.getByText("失败")).toBeInTheDocument();
  });
});
