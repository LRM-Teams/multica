// Next.js bundles path-to-regexp internally without shipping its types.
// This is the exact matching engine `redirects()`/`rewrites()` run
// through, used in route-redirects.test.ts to test our redirect
// patterns against the real thing rather than an approximation.
declare module "next/dist/compiled/path-to-regexp" {
  export function pathToRegexp(source: string): RegExp;
}
