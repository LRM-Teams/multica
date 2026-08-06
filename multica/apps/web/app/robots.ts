import type { MetadataRoute } from "next";

export default function robots(): MetadataRoute.Robots {
  const baseUrl = "https://www.leagent.me";

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
