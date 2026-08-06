"use client";

import type { JSX } from "react";
import type { ResearchV6NodeKind, ResearchV6UnknownKindDiagnostic } from "@multica/core/types/research-v6";
import {
  RESEARCH_V6_NODE_REGISTRY,
  classifyNodeKind,
  classifyEdgeType,
} from "@multica/core/research-v6/registry";

/**
 * V6 node render surface — the frontend node registry in action.
 *
 * This module is the display-side counterpart of the core registry
 * (`packages/core/research-v6/registry.ts`). It turns a canonical V6
 * projection node into a safe render surface:
 *  - known node kinds render via their registry label;
 *  - unknown future kinds ALWAYS degrade to a `<GenericNode>` (page never
 *    crashes) and record a `ResearchV6UnknownKindDiagnostic`.
 *
 * It is intentionally dependency-light and isolated from the canvas renderer
 * so the graph-model team can adopt it without fighting layout internals.
 * Display grouping/labels never write back to canonical state.
 */

export interface V6NodeRenderLabels {
  unknownNode: string;
  unknownEdge: string;
  diagnosticsTitle: string;
  rawKindLabel: string;
  entityIdLabel: string;
}

export const DEFAULT_V6_NODE_RENDER_LABELS: V6NodeRenderLabels = {
  unknownNode: "未知节点",
  unknownEdge: "未知关系",
  diagnosticsTitle: "未能识别的类型",
  rawKindLabel: "原始类型",
  entityIdLabel: "实体",
};

export interface V6UnknownNodeViewModel {
  isGeneric: true;
  nodeId: string;
  title: string;
  summary: string;
  status: string;
  kind: string;
  diagnostic: ResearchV6UnknownKindDiagnostic;
  diagnosticIndex: number;
}

export interface V6KnownNodeViewModel {
  isGeneric: false;
  nodeId: string;
  kind: ResearchV6NodeKind;
  label: string;
  group: string;
  title: string;
  summary: string;
  status: string;
}

export type V6NodeViewModel = V6UnknownNodeViewModel | V6KnownNodeViewModel;

/**
 * Build a safe render view model for one projection node. Unknown kinds become
 * a `V6UnknownNodeViewModel` carrying diagnostics; the function never throws.
 */
export function toV6NodeViewModel(
  node: {
    id: string;
    node_kind: string;
    title: string;
    summary: string;
    status: string;
    run_id: string;
  },
  diagnostics: ResearchV6UnknownKindDiagnostic[],
): V6NodeViewModel {
  const surface = classifyNodeKind(node.node_kind, node.id, node.run_id, diagnostics);
  if (surface.isGeneric) {
    return {
      isGeneric: true,
      nodeId: node.id,
      title: node.title,
      summary: node.summary,
      status: node.status,
      kind: surface.kind,
      diagnostic: surface.diagnostic,
      diagnosticIndex: surface.diagnostic.sequence,
    };
  }
  return {
    isGeneric: false,
    nodeId: node.id,
    kind: surface.kind,
    label: surface.label,
    group: surface.group,
    title: node.title,
    summary: node.summary,
    status: node.status,
  };
}

/** Edge relation metadata for display; unknown edges degrade, never throw. */
export function toV6EdgeViewModel(
  edge: { id: string; edge_type: string; run_id: string },
  diagnostics: ResearchV6UnknownKindDiagnostic[],
): { isGeneric: boolean; label: string; type: string; diagnostic?: ResearchV6UnknownKindDiagnostic } {
  return classifyEdgeType(edge.edge_type, edge.id, edge.run_id, diagnostics);
}

/**
 * GenericNode — safe rendering for any node kind the registry does not know.
 * It renders the bounded display fields plus the recorded diagnostic so the
 * unknown input stays observable without crashing the page.
 */
export function GenericNode(props: {
  viewModel: V6UnknownNodeViewModel;
  labels?: V6NodeRenderLabels;
}): JSX.Element {
  const labels = props.labels ?? DEFAULT_V6_NODE_RENDER_LABELS;
  const { viewModel } = props;
  return (
    <div
      data-testid="v6-generic-node"
      data-kind={viewModel.kind}
      data-diagnostic-seq={viewModel.diagnosticIndex}
      role="group"
      aria-label={`${labels.unknownNode}: ${viewModel.title ?? viewModel.nodeId}`}
      style={{ border: "1px dashed #c0392b", borderRadius: 8, padding: 8, maxWidth: 260 }}
    >
      <div style={{ fontWeight: 700, color: "#c0392b" }}>
        {labels.unknownNode}
        <span style={{ marginLeft: 6, fontFamily: "monospace", fontWeight: 400 }}>{viewModel.kind}</span>
      </div>
      <div>{viewModel.title || viewModel.nodeId}</div>
      <div style={{ color: "#596069", fontSize: 12 }}>{viewModel.summary}</div>
      <details data-testid="v6-generic-diagnostics">
        <summary>{labels.diagnosticsTitle}</summary>
        <dl style={{ margin: 0, fontSize: 12, fontFamily: "monospace" }}>
          <dt>{labels.rawKindLabel}</dt>
          <dd data-testid="v6-diagnostic-raw">{viewModel.diagnostic.raw}</dd>
          <dt>{labels.entityIdLabel}</dt>
          <dd>{viewModel.diagnostic.owner_id}</dd>
        </dl>
      </details>
    </div>
  );
}

/** Registry-driven view: renders GenericNode for unknown kinds, safe card otherwise. */
export function V6NodeView(props: {
  viewModel: V6NodeViewModel;
  labels?: V6NodeRenderLabels;
}): JSX.Element {
  if (props.viewModel.isGeneric) {
    return <GenericNode viewModel={props.viewModel} labels={props.labels} />;
  }
  const known = props.viewModel;
  const meta = RESEARCH_V6_NODE_REGISTRY.get(known.kind);
  return (
    <div
      data-testid="v6-known-node"
      data-kind={known.kind}
      role="group"
      aria-label={`${known.label}: ${known.title ?? known.nodeId}`}
      style={{ border: "1px solid #d0d7de", borderRadius: 8, padding: 8, maxWidth: 260 }}
    >
      <div style={{ fontWeight: 700 }}>
        {known.label}
        <span style={{ marginLeft: 6, fontFamily: "monospace", fontWeight: 400, fontSize: 12, color: "#596069" }}>
          {known.group}
        </span>
      </div>
      <div>{known.title || known.nodeId}</div>
      <div style={{ color: "#596069", fontSize: 12 }}>{known.summary}</div>
      <div style={{ fontSize: 12, color: meta ? "#596069" : undefined }}>{known.status}</div>
    </div>
  );
}
