import type { MetadataRoute } from "next";
import { PUBLIC_APP_ORIGIN } from "@/config/public-origin";

export default function robots(): MetadataRoute.Robots {
  const baseUrl = PUBLIC_APP_ORIGIN;

  return {
    rules: [
      {
        userAgent: "*",
        allow: ["/", "/about", "/changelog"],
        disallow: [
          "/api/",
          "/ws",
          "/auth/",
          "/issues",
          "/board",
          "/inbox",
          "/agents",
          "/settings",
          "/my-issues",
          "/computers",
          "/skills",
        ],
      },
    ],
    sitemap: [`${baseUrl}/sitemap.xml`, `${baseUrl}/docs/sitemap.xml`],
  };
}
