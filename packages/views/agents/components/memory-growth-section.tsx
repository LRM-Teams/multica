"use client";

import type { AgentMemoryGrowth } from "@multica/core/types";
import { computeMemoryGrowth } from "@multica/core/agents";
import { MemoryGrowthField } from "./memory-growth-field";
import { useT } from "../../i18n/use-t";

/**
 * Explicit design-preview write count while LRM-303 is not yet on the
 * environment. Only used in non-production builds so zero-write agents never
 * get a silent fake tier in prod (LRM-238). Remove once LRM-303 is live.
 */
const DESIGN_PREVIEW_WRITES =
  process.env.NODE_ENV !== "production" ? 5 : null;

export function resolveMemoryGrowth(
  growth: AgentMemoryGrowth | null | undefined,
): AgentMemoryGrowth | null {
  if (growth) return growth;
  if (DESIGN_PREVIEW_WRITES == null) return null;
  return computeMemoryGrowth(DESIGN_PREVIEW_WRITES);
}

interface MemoryGrowthSectionProps {
  growth: AgentMemoryGrowth | null | undefined;
  align?: "start" | "center";
  className?: string;
}

/** Renders scheme-A Memory growth or nothing (zero XP / loading / error). */
export function MemoryGrowthSection({
  growth,
  align,
  className,
}: MemoryGrowthSectionProps) {
  const { t } = useT("agents");
  const resolved = resolveMemoryGrowth(growth);
  if (!resolved) return null;
  return (
    <MemoryGrowthField
      growth={resolved}
      align={align}
      className={className}
      title={t(($) => $.memory_growth.title)}
      nextLabel={(tierLabel) =>
        t(($) => $.memory_growth.next, { tier: tierLabel })
      }
      writesLabel={(current, required) =>
        t(($) => $.memory_growth.writes, { current, required })
      }
    />
  );
}
