"use client";

import {
  sandboxEndpointLinks,
  type SandboxEndpointLinkKind,
} from "@multica/core/sandboxes/utils";
import type { SandboxInstance } from "@multica/core/types";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "../../i18n/use-t";

/**
 * Terminal / Pi Web / noVNC (and optional code) links from sandboxd endpoint_info.
 * Shared by Sandbox settings and Computers → cloud computer detail.
 */
export function SandboxEndpointLinks({
  instance,
  className,
}: {
  instance: SandboxInstance;
  className?: string;
}) {
  const { t } = useT("layout");
  const links = sandboxEndpointLinks(instance.endpoint_info);
  if (links.length === 0) return null;

  const labelFor = (kind: SandboxEndpointLinkKind) => {
    switch (kind) {
      case "term":
        return t(($) => $.sandboxes_page.endpoint_term_label);
      case "pi_web":
        return t(($) => $.sandboxes_page.endpoint_pi_web_label);
      case "novnc":
        return t(($) => $.sandboxes_page.endpoint_novnc_label);
      case "code":
        return t(($) => $.sandboxes_page.endpoint_code_label);
      default:
        return kind;
    }
  };

  return (
    <div className={className ?? "space-y-2"}>
      <Label>{t(($) => $.sandboxes_page.endpoint_links_label)}</Label>
      <div className="space-y-2 rounded-md border bg-muted/30 px-3 py-3">
        {links.map((link) => (
          <div key={link.kind} className="flex min-w-0 items-center gap-3">
            <span className="w-20 shrink-0 text-xs font-medium text-muted-foreground">
              {labelFor(link.kind)}
            </span>
            <a
              href={link.url}
              target="_blank"
              rel="noopener noreferrer"
              className="min-w-0 flex-1 truncate font-mono text-sm text-primary underline-offset-4 hover:underline"
            >
              {link.url}
            </a>
          </div>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">
        {t(($) => $.sandboxes_page.endpoint_links_hint)}
      </p>
    </div>
  );
}
