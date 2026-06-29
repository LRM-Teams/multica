import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ActorIdentityRow } from "./actor-identity-row";

describe("ActorIdentityRow", () => {
  it("renders display name with weak @handle when they differ", () => {
    render(
      <ActorIdentityRow identity={{ display_name: "Alice Zhang", name: "alice" }} />,
    );

    expect(screen.getByText("Alice Zhang")).toBeInTheDocument();
    expect(screen.getByText("@alice")).toBeInTheDocument();
  });

  it("hides @handle when display name falls back to handle", () => {
    render(<ActorIdentityRow identity={{ display_name: "", name: "alice" }} />);

    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.queryByText("@alice")).not.toBeInTheDocument();
  });

  it("supports explicit displayName and handle overrides", () => {
    render(<ActorIdentityRow displayName="Aegis" handle="agent_aegis" />);

    expect(screen.getByText("Aegis")).toBeInTheDocument();
    expect(screen.getByText("@agent_aegis")).toBeInTheDocument();
  });
});