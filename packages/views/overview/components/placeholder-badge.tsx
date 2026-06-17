"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import { useT } from "../../i18n";

/**
 * Marker shown on widgets backed by mock data (see mock.ts) so demo content is
 * never mistaken for live data.
 */
export function PlaceholderBadge() {
  const { t } = useT("overview");
  return (
    <Badge variant="secondary" className="text-[10px] font-normal text-muted-foreground">
      {t(($) => $.placeholder_badge)}
    </Badge>
  );
}
