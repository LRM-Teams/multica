import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { DisplayGroupCard } from "./display-group-card";

const freshGroup = {
  groupId: "group:r2",
  memberIds: ["r2", "i3", "c6"],
  memberCount: 3,
  onStalePath: false,
};

const staleGroup = {
  groupId: "group:m1",
  memberIds: ["m1"],
  memberCount: 5,
  onStalePath: true,
};

describe("DisplayGroupCard（LRM-1476）", () => {
  it("诚实标注为「显示分组」并显示成员节点数", () => {
    render(<DisplayGroupCard group={freshGroup} />);
    const card = screen.getByTestId("display-group-card");
    expect(card.getAttribute("data-group-id")).toBe("group:r2");
    expect(card.getAttribute("data-member-count")).toBe("3");
    expect(screen.getByTestId("display-group-count").textContent).toContain("3 节点");
    expect(screen.getByText(/显示分组/)).toBeTruthy();
    // 不冒充真实 Insight
    expect(screen.getByTestId("display-group-hint").textContent).toContain("非真实 Insight");
  });

  it("位于 stale 路径上的分组显示失效标记", () => {
    render(<DisplayGroupCard group={staleGroup} />);
    expect(screen.getByTestId("display-group-card").getAttribute("data-on-stale-path")).toBe("true");
    expect(screen.getByTestId("display-group-stale")).toBeTruthy();
  });

  it("点击触发展开，且展开态标注 aria-expanded", () => {
    const onToggle = vi.fn();
    render(<DisplayGroupCard group={freshGroup} expanded={false} onToggle={onToggle} />);
    fireEvent.click(screen.getByTestId("display-group-card"));
    expect(onToggle).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("display-group-card").getAttribute("data-expanded")).toBe("false");
  });
});
