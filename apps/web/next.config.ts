import type { NextConfig } from "next";
import { config } from "dotenv";
import { resolve } from "path";
import { resolveRemoteApiUrl } from "./config/runtime-urls";
import { createMDX } from "fumadocs-mdx/next";

// Load root .env so REMOTE_API_URL is available to next.config.ts
config({ path: resolve(__dirname, "../../.env") });

const remoteApiUrl = resolveRemoteApiUrl(process.env);
const docsUrl = process.env.DOCS_URL || "http://localhost:4000";

// Parse hostnames from CORS_ALLOWED_ORIGINS so that Next.js dev server
// allows cross-origin HMR / webpack requests (e.g. from Tailscale IPs).
const allowedDevOrigins = process.env.CORS_ALLOWED_ORIGINS
  ? Array.from(
      new Set(
        process.env.CORS_ALLOWED_ORIGINS.split(",")
          .flatMap((origin) => {
            const value = origin.trim();
            if (!value) return [];
            try {
              const url = new URL(value);
              return [url.hostname, url.host];
            } catch {
              return [value];
            }
          })
          .filter(Boolean),
      ),
    )
  : undefined;

const nextConfig: NextConfig = {
  ...(process.env.STANDALONE === "true" ? { output: "standalone" as const } : {}),
  transpilePackages: ["@multica/core", "@multica/ui", "@multica/views"],
  // The repo's own `typecheck` turbo task (`tsc --noEmit`, run as a separate
  // CI step) is the single source of truth for type errors. Without this,
  // `next build` ALSO runs its own full TS check internally — same errors,
  // twice, every CI run, and turbo can't dedupe it (two distinct tasks).
  typescript: { ignoreBuildErrors: true },
  ...(allowedDevOrigins && allowedDevOrigins.length > 0
    ? { allowedDevOrigins }
    : {}),
  images: {
    formats: ["image/avif", "image/webp"],
    qualities: [75, 80, 85],
  },
  async redirects() {
    return [
      // The "Runtimes" page was renamed to "Computers" (task #18) — keep old
      // links/bookmarks working instead of 404ing. Not `permanent` (307, not
      // 308): a 308 is aggressively cached by browsers, and we may still
      // adjust this rename.
      //
      // `:workspaceSlug` is a wildcard single-segment match — without the
      // `(?!api)` exclusion it also matches `/api/runtimes`, redirecting the
      // real API request to a nonexistent `/api/computers` before it ever
      // reaches the `/api/:path*` rewrite below. That broke every caller of
      // the runtimes API in production (task #18 hotfix).
      {
        source: "/:workspaceSlug((?!api)[^/]+)/runtimes",
        destination: "/:workspaceSlug/computers",
        permanent: false,
      },
      {
        source: "/:workspaceSlug((?!api)[^/]+)/runtimes/:id",
        destination: "/:workspaceSlug/computers/:id",
        permanent: false,
      },
    ];
  },
  async rewrites() {
    return {
      // Run before file-system routes so /docs isn't shadowed by the
      // [workspaceSlug] dynamic segment.
      beforeFiles: [
        {
          source: "/docs",
          destination: `${docsUrl}/docs`,
        },
        {
          source: "/docs/:path*",
          destination: `${docsUrl}/docs/:path*`,
        },
      ],
      afterFiles: [
        {
          source: "/api/:path*",
          destination: `${remoteApiUrl}/api/:path*`,
        },
        {
          source: "/ws",
          destination: `${remoteApiUrl}/ws`,
        },
        {
          source: "/auth/:path*",
          destination: `${remoteApiUrl}/auth/:path*`,
        },
        {
          source: "/uploads/:path*",
          destination: `${remoteApiUrl}/uploads/:path*`,
        },
      ],
      fallback: [],
    };
  },
};

// fumadocs-mdx@12 is incompatible with Next 16's Turbopack: its loader fails to
// dynamic-import `.source/source.config.mjs` under the Turbopack Node evaluator
// (see fumadocs#2658). `dev`/`build` scripts pass `--webpack` to opt out.
// Drop the flag once fumadocs-mdx ships a Turbopack-compatible loader.
const withMDX = createMDX() as (config: NextConfig) => NextConfig;

export default withMDX(nextConfig);
