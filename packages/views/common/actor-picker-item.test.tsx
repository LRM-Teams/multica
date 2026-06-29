import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ActorPickerItem } from "./actor-picker-item";

vi.mock("./actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

describe("ActorPickerItem", () => {
  it("renders identity stack and handles click", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();

    render(
      <ActorPickerItem
        actorType="agent"
        actorId="a1"
        identity={{ display_name: "Aegis", name: "agent_aegis" }}
        fallback="a1"
        selected={false}
        onClick={onClick}
      />,
    );

    expect(screen.getByText("Aegis")).toBeInTheDocument();
    expect(screen.getByText("@agent_aegis")).toBeInTheDocument();
    expect(screen.getByTestId("actor-avatar")).toBeInTheDocument();

    await user.click(screen.getByRole("button"));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});