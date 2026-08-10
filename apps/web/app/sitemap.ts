import type { MetadataRoute } from "next";
import { PUBLIC_APP_ORIGIN } from "@/config/public-origin";

export default function sitemap(): MetadataRoute.Sitemap {
  const baseUrl = PUBLIC_APP_ORIGIN;

  return [
    {
      url: baseUrl,
      lastModified: new Date("2026-04-01"),
      changeFrequency: "weekly",
      priority: 1.0,
    },
    {
      url: `${baseUrl}/about`,
      lastModified: new Date("2026-04-01"),
      changeFrequency: "monthly",
      priority: 0.7,
    },
    {
      url: `${baseUrl}/changelog`,
      lastModified: new Date("2026-04-01"),
      changeFrequency: "weekly",
      priority: 0.6,
    },
    {
      url: `${baseUrl}/contact-sales`,
      lastModified: new Date("2026-05-21"),
      changeFrequency: "monthly",
      priority: 0.7,
    },
  ];
}
