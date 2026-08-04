# Agent Fleet Honor Card Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the Agents overview fleet-strength panel into a visually distinctive, fully clickable card that opens the selected agent's Honor tab.

**Architecture:** Keep `AgentDetailOverview` as the owner of navigation and fleet data. Add a focused internal `FleetHonorCard` that renders the existing `AgentFleetRank`, uses the existing `onHonor` callback, and relies only on shared fleet icons and semantic UI tokens. No API, state, or route changes are required.

**Tech Stack:** React 19, TypeScript, Tailwind CSS, Vitest, Testing Library, `@multica/ui` fleet components.

---

### Task 1: Lock the whole-card Honor navigation contract

**Files:**
- Modify: `packages/views/agents/components/agent-detail-overview.test.tsx`
- Test: `packages/views/agents/components/agent-detail-overview.test.tsx`

- [ ] **Step 1: Add a realistic fleet fixture and pass it into the overview**

Import `AgentFleetRank`, define a fixture with all four pillar values, and extend `renderOverview` with an optional third `fleet` argument:

```tsx
const fleet: AgentFleetRank = {
  agent_id: agent.id,
  fleet_score: 68.4,
  class_id: "cruiser",
  class_label: "Cruiser",
  fleet_rank: 3,
  fleet_size: 12,
  sample_tasks: 24,
  sample_sufficient: true,
  frozen: false,
  pillars: {
    delivery: 0.82,
    evolution: 0.48,
    growth: 0.61,
    efficiency: 0.73,
  },
};

function renderOverview(task: AgentTask, onHonor = vi.fn(), fleetRank?: AgentFleetRank) {
  // Existing provider setup remains unchanged.
  return render(
    <AgentDetailOverview
      // Existing props remain unchanged.
      fleet={fleetRank}
      onHonor={onHonor}
    />,
  );
}
```

- [ ] **Step 2: Add the failing whole-card interaction test**

```tsx
it("focuses and opens Honor from the fleet card surface", () => {
  const onHonor = vi.fn();
  renderOverview(makeTask("queued"), onHonor, fleet);

  const fleetCard = screen.getByRole("button", { name: "Fleet rank · Honor" });
  fleetCard.focus();
  expect(fleetCard).toHaveFocus();

  fireEvent.click(fleetCard);
  expect(onHonor).toHaveBeenCalledOnce();
});
```

- [ ] **Step 3: Record the RED expectation without local execution**

Local tests are unavailable by explicit user constraint. The pre-implementation expectation is `FAIL`: the current fleet panel is a non-interactive `SectionCard`, so no named button `Fleet rank · Honor` exists. GitHub CI is the executable gate after the implementation is pushed.

### Task 2: Implement the command-deck fleet card

**Files:**
- Modify: `packages/views/agents/components/agent-detail-overview.tsx`

- [ ] **Step 1: Import the existing shared visual primitives**

Add `ChevronRight` to the Lucide imports, import `FleetClassIcon` beside `FleetRankBadge`, and import `fleetClassTone` from `@multica/ui/lib/fleet-class`.

- [ ] **Step 2: Add a focused internal `FleetHonorCard` component**

The component accepts only the existing data and callback:

```tsx
function FleetHonorCard({
  fleet,
  isArchived,
  classLabel,
  onHonor,
}: {
  fleet: AgentFleetRank;
  isArchived: boolean;
  classLabel: string;
  onHonor: () => void;
}) {
  const { t } = useT("agents");
  const pillars = [
    ["delivery", fleet.pillars.delivery],
    ["evolution", fleet.pillars.evolution],
    ["growth", fleet.pillars.growth],
    ["efficiency", fleet.pillars.efficiency],
  ] as const;

  return (
    <section className="group relative isolate w-full overflow-hidden rounded-2xl border border-primary/20 bg-gradient-to-br from-primary/[0.08] via-card to-chart-2/[0.08] p-4 text-left shadow-sm transition hover:-translate-y-0.5 hover:border-primary/40 hover:shadow-lg motion-reduce:transform-none motion-reduce:transition-none">
      <button
        type="button"
        data-testid="agent-fleet-honor-card"
        onClick={onHonor}
        className="absolute inset-0 z-20 cursor-pointer rounded-2xl bg-transparent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      >
        <span className="sr-only">
          {t(($) => $.fleet.title)} · {t(($) => $.tabs.honor)}
        </span>
      </button>
      {/* Semantic-token atmosphere, fleet emblem, score/rank hierarchy,
          localized Honor affordance, and four compact energy meters remain
          outside the button so assistive technology can read the data. */}
    </section>
  );
}
```

The final JSX must include:

- decorative primary/chart glow layers with `aria-hidden` and `pointer-events-none`;
- `FleetClassIcon` inside a command-emblem frame;
- the existing localized `FleetRankBadge`;
- large rounded fleet score and localized rank/frozen text;
- `Honor` plus `ChevronRight`, with a restrained group-hover translation;
- all four localized pillars with numeric values and clamped meter widths;
- the existing warming-up message when `sample_sufficient` is false.

- [ ] **Step 3: Replace only the old fleet `SectionCard` block**

```tsx
{fleet ? (
  <FleetHonorCard
    fleet={fleet}
    isArchived={isArchived}
    classLabel={fleetClassName(fleet.class_id, fleet.class_label)}
    onHonor={onHonor}
  />
) : null}
```

Keep the header Honor button and every non-fleet overview section unchanged.

- [ ] **Step 4: Perform static review**

Run only non-executing inspections allowed by the local constraint:

```bash
git diff --check
git diff --stat
git diff -- packages/views/agents/components/agent-detail-overview.tsx packages/views/agents/components/agent-detail-overview.test.tsx
```

Expected: no whitespace errors, no unrelated file changes, and the test asserts the user-visible navigation contract rather than CSS implementation details.

- [ ] **Step 5: Commit the implementation**

```bash
git add packages/views/agents/components/agent-detail-overview.tsx \
  packages/views/agents/components/agent-detail-overview.test.tsx \
  docs/superpowers/plans/2026-08-04-agent-fleet-honor-card.md
git commit -m "feat(agents): upgrade fleet honor card"
```

### Task 3: Push, open the small PR, and use CI as the test gate

**Files:**
- No code changes expected.

- [ ] **Step 1: Push the isolated branch**

```bash
git push -u origin feat/agent-fleet-honor-card
```

- [ ] **Step 2: Open a ready-for-review PR**

Create a PR with base `dev`, describe the whole-card navigation, semantic visual upgrade, regression coverage, and the explicit local-test limitation.

- [ ] **Step 3: Follow GitHub checks to completion**

```bash
gh pr checks <PR-number> --repo LRM-Teams/multica --watch
```

Expected: frontend tests, typecheck, lint, React Doctor, build, backend, and installer checks are green. If a deterministic check fails because of this branch, inspect the first failing log, fix the root cause, push a focused follow-up commit, and watch the replacement run.
