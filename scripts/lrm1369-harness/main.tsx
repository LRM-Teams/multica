import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./harness.css";

const params = new URLSearchParams(window.location.search);
document.documentElement.classList.add(
  params.get("theme") === "dark" ? "dark" : "light",
);

// `before=1` re-pins the label colour to the pre-LRM-1369 token so the same
// harness reproduces the failing baseline instead of relying on a stale build.
const before = params.get("before") === "1";
const label = before ? "text-success" : "text-success-strong";

// Real call-site class strings copied from the shipped components so the gate
// measures what users see, not a synthetic swatch.
const rows = [
  { id: "5", wash: "bg-success/5", note: "research-product-round-card" },
  { id: "10", wash: "bg-success/10", note: "agents/health · runtimes/shared" },
  { id: "15", wash: "bg-success/15", note: "git-list · graph-node · report" },
] as const;

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <div
      className="flex min-h-screen flex-col gap-4 bg-background p-6"
      data-testid="lrm1369-surface"
    >
      {rows.map((row) => (
        <div
          key={row.id}
          data-testid={`wash-${row.id}`}
          className={`rounded-md border border-success/35 px-3 py-2 ${row.wash}`}
        >
          <span
            data-testid={`label-${row.id}`}
            className={`text-sm font-medium ${label}`}
          >
            success on {row.wash} — {row.note}
          </span>
        </div>
      ))}

      {/* Card base (chips inside dialogs/cards). */}
      <div data-testid="wash-on-card" className="rounded-md border bg-card p-3">
        <div className="rounded-full bg-success/15 px-2 py-0.5">
          <span
            data-testid="label-card"
            className={`text-xs font-medium ${label}`}
          >
            success chip on card + wash/15
          </span>
        </div>
      </div>

      {/* Muted base — the worst measured stack (list rows / panel bodies). */}
      <div data-testid="wash-on-muted" className="rounded-md bg-muted/50 p-3">
        <div className="rounded-full bg-success/15 px-2 py-0.5">
          <span
            data-testid="label-muted"
            className={`text-xs font-medium ${label}`}
          >
            success chip on muted + wash/15
          </span>
        </div>
      </div>

      {/* Zero-regression probes: --success itself must not move. */}
      <div className="flex items-center gap-3">
        <span
          data-testid="dot-success"
          className="inline-block size-2 rounded-full bg-success"
        />
        <span
          data-testid="divider-success"
          className="inline-block h-1 w-16 bg-success"
        />
        <span
          data-testid="solid-success"
          className="rounded-lg bg-success px-2 py-1 font-bold text-white"
        >
          solid
        </span>
        <span data-testid="text-success-plain" className="text-sm text-success">
          plain text-success (no wash)
        </span>
      </div>
    </div>
  </StrictMode>,
);
