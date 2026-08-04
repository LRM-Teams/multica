import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./harness.css";

const params = new URLSearchParams(window.location.search);
document.documentElement.classList.add(params.get("theme") === "dark" ? "dark" : "light");

const rows = [
  { id: "5", wash: "bg-warning/5", label: "warning on bg-warning/5" },
  { id: "10", wash: "bg-warning/10", label: "warning on bg-warning/10" },
  { id: "15", wash: "bg-warning/15", label: "warning on bg-warning/15" },
] as const;

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <div
      className="flex min-h-screen flex-col gap-4 bg-background p-6"
      data-testid="lrm1353-surface"
    >
      {rows.map((row) => (
        <div
          key={row.id}
          data-testid={`wash-${row.id}`}
          className={`rounded-md border border-warning/30 px-3 py-2 ${row.wash}`}
        >
          <span
            data-testid={`label-${row.id}`}
            className="text-sm font-medium text-warning"
          >
            {row.label}
          </span>
        </div>
      ))}
      <div data-testid="wash-on-card" className="rounded-md border bg-card p-3">
        <div className="rounded-md bg-warning/10 px-3 py-2">
          <span
            data-testid="label-card"
            className="text-sm font-medium text-warning"
          >
            warning on card+wash/10
          </span>
        </div>
      </div>
    </div>
  </StrictMode>,
);
