import { queryOptions, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import type { ServiceEnvironment } from "../config";
import { MULTICA_RELEASE_METAINFO_URL } from "../constants/repository";

const PreviewTagSchema = z.string().regex(
  /^v\d+\.\d+\.\d+-(?:alpha|beta|rc)\.\d+$/,
);

const ComputerMetainfoSchema = z.object({
  schema_version: z.literal(1),
  environments: z.object({
    test: z.object({
      tag: PreviewTagSchema,
    }),
  }),
});

export interface TestComputerRelease {
  tag: string;
}

export async function fetchTestComputerRelease(
  signal?: AbortSignal,
): Promise<TestComputerRelease> {
  const response = await fetch(MULTICA_RELEASE_METAINFO_URL, {
    signal,
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(`Computer metainfo request failed (${response.status})`);
  }

  const parsed = ComputerMetainfoSchema.safeParse(await response.json());
  if (!parsed.success) {
    throw new Error("Computer metainfo contains an invalid Test release");
  }

  return { tag: parsed.data.environments.test.tag };
}

export function testComputerReleaseOptions(enabled = true) {
  return queryOptions({
    queryKey: ["computer-release", "test"] as const,
    queryFn: ({ signal }) => fetchTestComputerRelease(signal),
    enabled,
    staleTime: 0,
    refetchInterval: 60_000,
    refetchOnMount: "always",
    refetchOnWindowFocus: true,
  });
}

/**
 * Resolves the Computer release shown by every install-command surface.
 * Test prefers canonical metainfo's exact preview tag and falls back to the
 * valid moving `test` selector while it is loading or temporarily unavailable.
 * Production keeps the host-provided stable version.
 */
export function useComputerReleaseVersion(
  environment: ServiceEnvironment,
  configuredVersion = "",
): string {
  const { data } = useQuery(
    testComputerReleaseOptions(environment === "test"),
  );

  if (environment !== "test") {
    return configuredVersion.trim();
  }
  return data?.tag ?? "test";
}
