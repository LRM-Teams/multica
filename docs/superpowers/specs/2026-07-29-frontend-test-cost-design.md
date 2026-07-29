# Frontend test cost — long-term design

**Date:** 2026-07-29
**Owner:** Felix (FE lead)
**Status:** proposal — no implementation authorised. Sequencing and go/no-go are Parker's; Frank set the direction ("前端的测试可以改为长期方案").
**Related:** task #855 (archived conclusion in its task thread) · PR #1356 (merged) · task #853

> **Picking this up cold?** Read §2 (where the time goes) and §3 (why the obvious lever doesn't work) before anything else. Everything here was measured, not estimated; every number states the machine and mode it came from. **Nothing in §5 is authorised** — it is a menu with prices, not a plan of record. The three things most likely to mislead you are called out in §2 (a red run's wall time is not comparable), §3 (the cheap-looking fix is a prerequisite, not a win), and §4a (this mode's failure count is nondeterministic — do not quote a single run).

---

## 1. Why this exists

PR #1356 split the frontend CI job into `build/typecheck/lint` and `test`, and runs the test phase one outer Turbo task at a time. It bought a real thing — **zero timeouts, trustworthy test results** — at a real price:

| | before | after (#1356, live today) |
|---|---|---|
| frontend CI job | **~477s** | **~1066s** |

The cost is already being paid on every PR. This document is about **whether it can be reduced, and at what cost** — not about reverting #1356. Reverting returns the flake, which is the more expensive failure (a day was lost to "does this green count?").

## 2. Where the time actually goes

Measured locally on `packages/views` (10 cores, warm cache — **these numbers rank options; they do not predict CI wall time**):

| | baseline (all green) | `--no-isolate` (red) | `--no-isolate` + RTL cleanup |
|---|---|---|---|
| Test Files failed | 0 | 168 | **86** |
| Tests reported | 2851 | **2626** | 2851 |
| wall | 88.0s | 22.5s | 38.2s |
| **import** | **349.67s** | **44.77s** | 69.75s |
| **environment** | **240.76s** | 176.71s | 160.55s |
| tests | 120.31s | 109.12s | 116.05s |

**Reading these correctly matters more than the values:**

- `import` and `environment` are paid **per file, before any assertion runs**. All 297 files were still collected in every column, so **these two are not distorted by the failures.**
- `tests` and `wall` **are** distorted — a file that throws early skips its remaining cases. In the middle column 225 cases were never reported at all. **22.5s is a lower bound, not a measurement.**
- Wren independently reproduced the import collapse on a different machine with a different method (CLI flag vs config change): 350.27s → 74.94s. **Two people, two methods, same structure.** The magnitudes differ; the structure is the finding.
- Whether truncation occurs at all is **environment-dependent** (his run: 2924 → 2924, no truncation; mine: 2851 → 2626). Neither run disproves the other.

**Conclusion:** the dominant cost is not running tests. It is **re-importing the same module graph and rebuilding the same jsdom environment, once per test file, 297 times.**

## 3. What we know about `isolate: false`

Vitest's own guidance (`vitest.dev/guide/improving-performance`) scopes it to:

> "projects that don't rely on side effects and properly cleanup their state (**which is usually true for projects with `node` environment**)"

`packages/views` is jsdom — the case the parenthetical excludes. Turning isolation off surfaced two distinct root causes:

### Root cause 1 — missing per-file RTL cleanup. **Confirmed. One line.**

`packages/views/test/setup.ts` never registers React Testing Library's cleanup. Normally RTL self-registers `afterEach(cleanup)` when imported. Under `isolate: false` the module registry is **worker-scoped**, so that registration happens **only for the first file on each worker**; every later file leaves its DOM mounted.

```ts
// packages/views/test/setup.ts — setupFiles DO run per test file
import { cleanup } from "@testing-library/react";
afterEach(() => cleanup());
```

Effect, flip-verified against a **true control on the same base** (the file reverted with `git checkout origin/dev -- packages/views/test/setup.ts`, same command):

| `vitest run --no-isolate` | `Found multiple elements` | failing files |
|---|---|---|
| control (line absent) | **412** | 168 / 304 |
| with the line | **0** | 112 / 304 |

On an earlier base the same change also brought back **225 cases that were never reported at all** — which independently confirmed both that the truncation was real and that the un-patched run's wall time was meaningless.

> ⚠️ Method note, because it nearly produced a fake result: the first attempt at this control used `git stash push` on a file that was **already committed** — a no-op. "Control" and "treatment" were the same code, and the comparison looked entirely reasonable. Use `git checkout <base> -- <file>`, and sanity-check that the control file really lacks the thing you removed.

> ⚠️ **This line is a no-op under today's `isolate: true` config** — per-file registries already make RTL's own cleanup work, which is why the baseline is green. It is a **prerequisite for step 2, not a standalone win.** It must not be recorded as "168 → 86 fixed" while isolation is on.

### Root cause 2 — per-file module mocks. **Diffuse. No common fix.**

I first suspected the known automock/factory-mixing bug. **It is not that:** 1061 `vi.mock` calls in `packages/views`, exactly **1** automock. The real shape:

| module | files mocking it separately |
|---|---|
| `@multica/core/api` | 64 |
| `@multica/core/hooks` | 61 |
| `@multica/core/paths` | 58 |
| `@multica/core/auth` | 50 |
| `sonner` | 36 |
| `@tanstack/react-query` | 36 |

Under a shared registry, **which factory wins depends on file load order.** That is the `Unable to find an element` class (63 occurrences) — the mirror image of root cause 1: not leftover DOM, but *the wrong module*.

There is no one-line fix. Collapsing hundreds of per-file mocks into shared fixtures is real engineering.

## 4a. ⚠️ `--no-isolate` failure counts are nondeterministic — never quote one run

Two runs of **identical code and an identical command** on the same machine gave:

| run | failing files (of 304) |
|---|---|
| 1 | **112** |
| 2 | **81** |

A 31-file swing with nothing changed. This follows directly from root cause 2: which mock factory wins depends on **file load order**, and load order varies between runs. A single run's number is a sample from a distribution, not a measurement of the code.

**Consequence for anyone continuing this work:** if you compare "before" and "after" using one run each, you can manufacture any conclusion you like, in either direction. Ranking the options in §4 on single-run counts would be **wrong regardless of which option came out ahead**. Use repeated runs and report the spread, or compare something that doesn't vary — the per-file `import`/`environment` totals are stable across runs and are what the options actually target.

The one measurement here that *is* clean is the flip-verify in §3 (412 → 0): it isolates a single failure signature with a true control on the same base, and the effect size dwarfs the noise.

## 4. Options, with honest costs

> ### ⛔ SUPERSEDED — do not implement §4's option E or §5's step 2 as written
>
> **A pilot on 2026-07-29 measured this and disproved the argument for E.** The
> claim below — that merging fixes mock competition "by construction, since
> mocks within one file are already consistent" — **does not hold for the merge
> operation itself.** The seven `channels-page-*` tests mock an identical set of
> 18 modules, but their factory bodies differ by 68/106/127 lines out of blocks
> that are only 111/127/186 lines long: they are bespoke per file. Concatenating
> two of them puts two `vi.mock` calls for the same module in one file, the
> second silently wins, and the first file's tests then run against a stand-in
> they were never written for.
>
> **The duplication is in the factories, not the files** ⇒ shared mock fixtures
> (option D) moves ahead of merging (option E), and attacks the same duplication
> without giving up file-level isolation. **D's saving is not yet measured** and
> is deliberately not asserted here.
>
> Full revision follows once the shared-fixture measurement is in — taken on CI,
> not locally (local variance ~25%, dedicated runner ~0.4%).
>
> ### ⚠️ §1's goal and option D are not the same thing
>
> This document sets out to **reduce CI time**, and D was accepted as a means to
> that end. Building it showed the two come apart:
>
> - **D buys a single source of truth.** A new test file should not hand-copy a
>   160-line mock block, and changing the auth mock should be one edit, not seven.
> - **D is NOT shown to buy time, and there is reason to doubt it does.** Every
>   file still calls `vi.mock` fifteen times, still resolves the same modules, and
>   now imports one more. **Sharing removes duplicated source, not duplicated
>   module resolution** — and `import`/`environment` is the latter.
>
> A conversion of one file changed it from 304 to **302** lines: those "identical"
> factories are 3–7 lines each while the shared call site costs 4, so it is close
> to one-for-one. **"182 duplicated lines" was true; "extracting saves 182 lines"
> was not.**
>
> **Do not present D as a performance measure.** Whether the time goal is
> reachable at all is decided by one CI measurement over the seven converted
> `channels-page-*` files; if `import`/`environment` do not move, that path stops
> there rather than expanding to 297 files.


| # | option | attacks | cost | verdict |
|---|---|---|---|---|
| A | keep #1356 as-is | nothing | 1066s/PR forever | **status quo — acceptable, not free** |
| B | revert #1356 | 1066s → 477s | returns flake | **no** — the flake is more expensive |
| C | `isolate: false` alone | import (−87%) | 86 files red, load-order dependent | **no** — trades a known cost for a nondeterministic one |
| D | shared mock fixtures, then `isolate: false` | import + env | hundreds of files touched | large, sequenceable |
| E | **merge test files** | import + env + **mock competition** | large, mechanical, reviewable | **recommended direction** |

**Why E over D:** merging files fixes mock competition *by construction* — mocks within one file are already consistent — while also cutting per-file import and jsdom setup. `isolate: false` (C/D) fixes only the import half **and creates** the mock-competition problem that then has to be paid down separately.

## 5. Proposed sequencing — each step independently valuable and reversible

Nothing below is authorised. Each step ends in a decision point, not an automatic next step.

**Step 0 — instrument (no behaviour change).** Record per-file import and environment cost so later steps are measured, not asserted. *Acceptance:* a committed baseline artifact from a runner-shaped, cache-miss run. *Gain:* none directly — it makes every later claim checkable.

**Step 1 — land the RTL cleanup line.** *Acceptance:* suite green, no behaviour change. *Gain:* **zero today** — explicitly a prerequisite. Must be recorded as such.

**Step 2 — pilot merge on one directory.** Pick the largest cluster: **`channels/components`, 60 of the package's 297 test files (20%)** — the next largest are `common` (25) and `agents/components` (19). Merge its test files, keep every assertion. *Acceptance:* identical test count, identical pass set, **each merged file's guards flip-verified red**; measured import/env delta. *Gain:* measured, not predicted. *Decision point:* extrapolate only if the pilot's ratio holds.

**Step 3 — extend, directory by directory.** Only if step 2's measured ratio justifies it. *Acceptance:* per-directory, same as step 2.

**Step 4 — re-evaluate `isolate: false`.** Only after mock competition is structurally gone. *Acceptance:* full suite green **with** isolation off, 3 runner-shaped cache-miss runs.

## 6. Risks

- **Merged test files lose per-file isolation by definition** — a merged file's tests share one module registry. That is the point (it removes competition) but it means **a bad merge silently couples tests**. Mitigation: identical test count + flip-verify each guard, per file, no exceptions.
- **Extrapolating the pilot.** The heaviest directory is not representative of the light ones. State the sample.
- **Local numbers are not CI numbers.** Every acceptance above is runner-shaped and cache-miss for this reason.
- **`channels-page-system-channel` and the C/D full-page wiring tests are protected** (Barry, #853) — they are not candidates for "optimization" and must not be thinned during a merge.

## 7. What this document does not claim

- No estimate of the CI seconds this recovers. **Unknown until step 2 measures it.** The one honest bound: `import` is 349.67s of accounted work locally and most of it is duplication — but how much survives on a cold runner is not measured.
- No claim that step 1 helps today. It does not.
- No claim that #1356 should be reverted.
