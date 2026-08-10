// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import type { ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enChannels from "../../locales/en/channels.json";
import { ConversationActivityStrip } from "./conversation-activity-strip";

const TEST_RESOURCES = { en: { channels: enChannels } };

function renderStrip(props: ComponentProps<typeof ConversationActivityStrip>) {
  return render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <ConversationActivityStrip {...props} />
    </I18nProvider>,
  );
}

describe("ConversationActivityStrip", () => {
  it("renders conversation-scoped human typing", () => {
    renderStrip({ typingActors: [{ actorName: "Alice" }] });

    expect(screen.getByText("Alice is typing")).toBeInTheDocument();
    expect(screen.getByTestId("conversation-typing-row")).toBeInTheDocument();
  });

  it("renders the existing multi-human typing summary", () => {
    renderStrip({
      typingActors: [{ actorName: "Alice" }, { actorName: "Bob" }],
    });

    expect(screen.getByText("Alice and Bob are typing")).toBeInTheDocument();
  });

  it("renders nothing when nobody is typing", () => {
    const { container } = renderStrip({ typingActors: [] });

    expect(container).toBeEmptyDOMElement();
  });
});
