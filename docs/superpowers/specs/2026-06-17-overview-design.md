# 总览 (Overview) — Design Spec

Date: 2026-06-17
Status: Approved

## Goal

Add a workspace-scoped admin overview page "总览" (Agent Overview) to Multica,
reproduced from the Figma design (`Agent调研`, node 197:2 "总览-管理员"). Wire real
data where Multica's domain provides it; use clearly-marked mock placeholders
where no backing exists.

> Supersedes the earlier "动态看板 / dynamic-board" page (Figma node 1:3), which
> was built from the wrong design node and has been removed.

## Scope

- One shared view page + nav entry "总览" at `/overview`. No backend changes.
  Default workspace landing route stays `issues`.
- Web + desktop both wired (shared view, per-app route).

## Architecture

- Shared package dir `packages/views/overview/`.
- Renders inside the existing `DashboardLayout` shell. The Figma's own left
  sidebar is dropped (Multica's `AppSidebar` already provides nav + user). Only
  the design's main content area is implemented, in a responsive grid.
- Nav entry at top of `workspaceNav`, lucide icon `LayoutDashboard`.

## Layout (from node 197:2)

- Title "Agent 总览".
- Top row: 5 KPI cards — Active Agents, Tasks Done, Success Rate, Spend,
  Pending Approval. Some carry a "vs yesterday" trend tag.
- Two panels below: "与我相关的任务或消息" (tasks/messages for me) and
  "我的 Agent 工作状态" (my agents' activity).

## Widgets → Data Mapping

| Widget | Source | Kind |
|---|---|---|
| Active Agents (working/total, idle & error counts) | `agentListOptions` — agent list + `status` | REAL |
| Tasks Done (done/total, blocked count) | `issueStatusCountsOptions` — issue status totals | REAL |
| Success Rate | derived: done / (done + blocked) | DERIVED (from real) |
| Spend ($23.24 / budget $200) | no dollar-cost rollup in domain | MOCK |
| Pending Approval (count) | `in_review` issue total (count REAL; "longest wait" MOCK) | REAL + mock detail |
| "vs yesterday" trends (+8 / -4% / +6.23) | no day-over-day series | MOCK |
| Tasks & messages for me (priority 高/中/低/@) | `inboxListOptions` filtered to assign/mention/review/comment; priority from `severity` + `mentioned` | REAL |
| My agents' activity (success/fail dot) | `inboxListOptions` filtered to agent task events | REAL |

Mock data lives in `overview/mock.ts`, marked `// PLACEHOLDER`. The spend card
carries a visible "Demo data" badge.

## Data / Resilience

- Workspace-scoped queries key on `wsId`; `enabled: !!wsId`.
- Real widgets show Skeleton while loading and an empty state when no data.
- Follow API Response Compatibility rules: optional-chain every field, default
  arrays, explicit checks; switch on inbox `type` is membership-set based with a
  default fall-through.

## Styling

- Figma hardcoded colors mapped to semantic tokens: card `bg-card`, brand blue →
  `primary`, grays → `muted-foreground`, trend up → `text-success`, trend down /
  warning → `text-warning`, high priority → `destructive`. Dark-mode safe. No
  hardcoded colors. shadcn Card/Badge/Skeleton.

## Files

New: `packages/views/overview/**`, `packages/views/locales/{en,zh-Hans,ja,ko}/overview.json`,
`apps/web/app/[workspaceSlug]/(dashboard)/overview/page.tsx`.

Modified: `app-sidebar.tsx`, `paths.ts`, desktop `routes.tsx`, `package.json`
exports, `locales/index.ts`, `i18n/resources-types.ts`, `locales/*/layout.json`
(`nav.overview`). Reuses `issueStatusCountsOptions` in `core/issues/queries.ts`.

## Testing

- Typecheck (core/views/web), i18n parity (4 locales), ESLint.

## Out of Scope

- Real dollar-cost computation, day-over-day trend series, approval-wait metric.
- Role switching (普通员工/管理员) shown in the design.
- Changing the default workspace landing route.
