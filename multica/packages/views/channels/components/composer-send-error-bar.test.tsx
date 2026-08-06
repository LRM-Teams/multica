import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import {
  ComposerSendErrorBar,
  type ComposerSendErrorState,
} from "./composer-send-error-bar";

function renderBar(error: ComposerSendErrorState | null) {
  render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, channels: enChannels } }}
    >
      <ComposerSendErrorBar error={error} onRetry={vi.fn()} onRestore={vi.fn()} />
    </I18nProvider>,
  );
}

describe("ComposerSendErrorBar — #1276 413 too-long", () => {
  it("tooLong: shows a shorten-and-retry message and offers NO Retry (a raw retry just 413s again)", () => {
    renderBar({ conflicted: false, tooLong: true });
    expect(screen.getByText(/too long/i)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("ordinary retryable failure: shows a Retry button", () => {
    renderBar({ conflicted: false });
    expect(screen.getByRole("button")).toHaveTextContent(/retry/i);
  });

  it("conflicted failure: offers Restore previous, not Retry", () => {
    renderBar({ conflicted: true });
    expect(screen.getByRole("button")).toHaveTextContent(/restore/i);
  });
});
