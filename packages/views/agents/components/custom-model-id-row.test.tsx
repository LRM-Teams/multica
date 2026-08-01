// @vitest-environment jsdom

import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import { CustomModelIdRow } from "./custom-model-id-row";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

function renderRow(onSubmit = vi.fn()) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <CustomModelIdRow onSubmit={onSubmit} />
    </I18nProvider>,
  );
  return onSubmit;
}

describe("CustomModelIdRow", () => {
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  it("is always visible as a Custom model ID row", () => {
    renderRow();
    expect(screen.getByText("Custom model ID…")).toBeInTheDocument();
  });

  it("opens an independent input and submits on Enter", () => {
    const onSubmit = renderRow();
    fireEvent.click(screen.getByText("Custom model ID…"));
    const input = screen.getByPlaceholderText("Enter model ID");
    fireEvent.change(input, { target: { value: "my-custom-model" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onSubmit).toHaveBeenCalledWith("my-custom-model");
  });

  it("cancels on Escape without submitting", () => {
    const onSubmit = renderRow();
    fireEvent.click(screen.getByText("Custom model ID…"));
    const input = screen.getByPlaceholderText("Enter model ID");
    fireEvent.change(input, { target: { value: "abort-me" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText("Custom model ID…")).toBeInTheDocument();
  });
});
