import type { MetadataRoute } from "next";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Multica",
    short_name: "Multica",
    description: "Project management for human and agent teams.",
    start_url: "/",
    scope: "/",
    display: "standalone",
    background_color: "#05070b",
    theme_color: "#05070b",
    // iOS Home Screen / Web Push (16.4+) expects PNG icons — SVG-only
    // manifests can leave installs looking like Safari bookmarks and block
    // standalone display-mode, which gates PushManager on iOS (LRM-684).
    icons: [
      {
        src: "/icon-192.png",
        sizes: "192x192",
        type: "image/png",
        purpose: "any",
      },
      {
        src: "/icon-512.png",
        sizes: "512x512",
        type: "image/png",
        purpose: "any",
      },
      {
        src: "/favicon.svg",
        sizes: "any",
        type: "image/svg+xml",
        purpose: "any",
      },
    ],
  };
}
