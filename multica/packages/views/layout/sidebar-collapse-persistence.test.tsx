import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { SidebarProvider, useSidebar } from "@multica/ui/components/ui/sidebar";

// LRM-765: collapsed preference persists across remounts (refresh) via
// localStorage, desktop only — mobile keeps its own openMobile state.
function Probe() {
  const { open, toggleSidebar, isMobile } = useSidebar();
  return (
    <button
      type="button"
      data-testid="toggle"
      data-open={String(open)}
      data-mobile={String(isMobile)}
      onClick={toggleSidebar}
    >
      toggle
    </button>
  );
}

function renderProvider() {
  return render(
    <SidebarProvider>
      <Probe />
    </SidebarProvider>,
  );
}

describe("SidebarProvider collapsed persistence (LRM-765)", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("writes the collapsed state to localStorage on toggle", () => {
    renderProvider();
    expect(screen.getByTestId("toggle").dataset.open).toBe("true");
    fireEvent.click(screen.getByTestId("toggle"));
    expect(screen.getByTestId("toggle").dataset.open).toBe("false");
    expect(localStorage.getItem("sidebar_open")).toBe("false");
  });

  it("restores the persisted collapsed state on remount", () => {
    localStorage.setItem("sidebar_open", "false");
    renderProvider();
    expect(screen.getByTestId("toggle").dataset.open).toBe("false");
  });

  it("restores the persisted expanded state on remount", () => {
    localStorage.setItem("sidebar_open", "true");
    renderProvider();
    expect(screen.getByTestId("toggle").dataset.open).toBe("true");
  });

  it("a controlled provider is not overridden by the stored value", () => {
    localStorage.setItem("sidebar_open", "false");
    render(
      <SidebarProvider open>
        <Probe />
      </SidebarProvider>,
    );
    expect(screen.getByTestId("toggle").dataset.open).toBe("true");
  });
});
