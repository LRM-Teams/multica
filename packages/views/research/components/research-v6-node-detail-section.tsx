"use client";

import type {
  ResearchV6DirectorEntityRef,
  ResearchV6DirectorNodeDetail,
} from "@multica/core/types/research-v6-director";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { AlertCircle, LoaderCircle, RotateCcw } from "lucide-react";
import { useT } from "../../i18n/use-t";

function ReferenceGroup({
  label,
  references,
}: {
  label: string;
  references: readonly ResearchV6DirectorEntityRef[];
}) {
  if (references.length === 0) return null;
  return (
    <div>
      <dt className="text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
        {label}
      </dt>
      <dd className="mt-1 flex flex-wrap gap-1">
        {references.map((reference) => (
          <Badge
            key={`${reference.kind}:${reference.id}:${reference.revision ?? 0}`}
            variant="outline"
            className="max-w-full gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium"
            title={`${reference.kind}:${reference.id}`}
          >
            <span>{reference.kind}</span>
            {reference.revision ? (
              <span className="text-muted-foreground">v{reference.revision}</span>
            ) : null}
          </Badge>
        ))}
      </dd>
    </div>
  );
}

export function ResearchV6NodeDetailSection({
  detail,
  loading,
  error,
  onRetry,
  onFocusNode,
}: {
  detail?: ResearchV6DirectorNodeDetail;
  loading: boolean;
  error: Error | null;
  onRetry: () => void;
  onFocusNode?: (nodeId: string) => void;
}) {
  const { t } = useT("research");

  if (loading && !detail) {
    return (
      <section
        className="flex items-center gap-2 rounded-lg border border-primary/15 bg-primary/5 p-3 text-xs text-muted-foreground"
        aria-live="polite"
      >
        <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" />
        {t(($) => $.v6_detail.loading)}
      </section>
    );
  }

  if (error && !detail) {
    return (
      <section className="rounded-lg border border-destructive/25 bg-destructive/5 p-3">
        <div className="flex items-start gap-2 text-xs text-foreground">
          <AlertCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
          <p>{t(($) => $.v6_detail.load_failed)}</p>
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="mt-2 h-8 gap-1.5"
          onClick={onRetry}
        >
          <RotateCcw className="size-3.5" />
          {t(($) => $.session_page.retry)}
        </Button>
      </section>
    );
  }

  if (!detail) return null;
  const termination = detail.node.state.termination;
  const relations = [...detail.incoming, ...detail.outgoing];

  return (
    <section
      data-testid="research-v6-node-detail-section"
      className="rounded-lg border border-primary/15 bg-primary/5 p-3"
    >
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge variant="outline">{detail.node.tier}</Badge>
        <Badge variant="secondary">{detail.node.state.execution}</Badge>
        <Badge variant="secondary">{detail.node.state.conclusion}</Badge>
        <Badge variant="secondary">{detail.node.state.integration}</Badge>
      </div>

      {termination ? (
        <div className="mt-3 rounded-md border border-warning/25 bg-warning/8 p-2.5">
          <p className="text-[10px] font-semibold tracking-wide text-warning uppercase">
            {termination.reason_code}
          </p>
          <p className="mt-1 text-xs leading-relaxed text-foreground">
            {termination.reason_detail}
          </p>
        </div>
      ) : null}

      {relations.length > 0 ? (
        <div className="mt-3">
          <h3 className="text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
            {t(($) => $.v6_detail.relations)}
          </h3>
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {relations.map((edge) => {
              const neighborId =
                edge.from_node_id === detail.node.id
                  ? edge.to_node_id
                  : edge.from_node_id;
              return (
                <Button
                  key={edge.id}
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-7 max-w-full gap-1 px-2 text-[10px]"
                  aria-label={`${edge.kind}: ${neighborId}`}
                  title={neighborId}
                  onClick={() => onFocusNode?.(neighborId)}
                >
                  <span>{edge.kind}</span>
                  {edge.hidden_count > 0 ? (
                    <span className="text-muted-foreground">
                      +{edge.hidden_count}
                    </span>
                  ) : null}
                </Button>
              );
            })}
          </div>
        </div>
      ) : null}

      <dl className="mt-3 grid gap-3 sm:grid-cols-2">
        <ReferenceGroup label={t(($) => $.v6_detail.agents)} references={detail.agent_refs} />
        <ReferenceGroup label={t(($) => $.v6_detail.work_items)} references={detail.work_item_refs} />
        <ReferenceGroup label={t(($) => $.v6_detail.attempts)} references={detail.attempt_refs} />
        <ReferenceGroup label={t(($) => $.v6_detail.evidence)} references={detail.evidence_refs} />
        <ReferenceGroup label={t(($) => $.v6_detail.discussions)} references={detail.discussion_refs} />
        <ReferenceGroup label={t(($) => $.v6_detail.reports)} references={detail.report_refs} />
        <ReferenceGroup label={t(($) => $.v6_detail.history)} references={detail.history_refs} />
      </dl>
    </section>
  );
}
