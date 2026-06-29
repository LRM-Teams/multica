import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MobileListDetailLayout } from "./mobile-list-detail-layout";

describe("MobileListDetailLayout", () => {
  it("keeps the list mounted while detail is visible", () => {
    render(
      <MobileListDetailLayout
        showDetail
        list={<div>List pane</div>}
        detail={<div>Detail pane</div>}
      />,
    );

    expect(screen.getByText("List pane")).toBeInTheDocument();
    expect(screen.getByText("Detail pane")).toBeInTheDocument();
    expect(screen.getByText("List pane").parentElement).toHaveAttribute(
      "aria-hidden",
      "true",
    );
  });

  it("renders only the list when detail is hidden", () => {
    render(
      <MobileListDetailLayout
        showDetail={false}
        list={<div>List pane</div>}
        detail={<div>Detail pane</div>}
      />,
    );

    expect(screen.getByText("List pane")).toBeInTheDocument();
    expect(screen.queryByText("Detail pane")).not.toBeInTheDocument();
  });
});