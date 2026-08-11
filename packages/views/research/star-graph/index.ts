/**
 * LRM-1496 — 调研星图 D5：五级节点视觉系统与 Map Key.
 *
 * Data contract + adapter live in `lib` (this package); the presentation
 * components and design tokens live in `@multica/ui/components/star-graph`,
 * shared across Web and Desktop. Business logic never enters `packages/ui`.
 */
export * from "./lib";
export * from "./components";
