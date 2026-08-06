import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ConversationActivityStrip } from "./channels-page";

function renderStrip(ui: React.ReactElement) {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      {ui}
    </I18nProvider>,
  );
}

describe("ConversationActivityStrip", () => {
  it("renders human typing only", () => {
    renderStrip(
      <ConversationActivityStrip
        typingActors={[
          {
            key: "u1",
            channelId: "ch-1",
            actorType: "user",
            actorName: "Lee",
            expiresAt: 1_900_000_000_000,
          },
        ]}
      />,
    );

    expect(screen.getByText("Lee is typing")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /stop/i })).toBeNull();
  });

  it("stays hidden when nothing is typing", () => {
    renderStrip(<ConversationActivityStrip />);

    expect(screen.queryByTestId("conversation-activity-strip")).toBeNull();
  });
});
