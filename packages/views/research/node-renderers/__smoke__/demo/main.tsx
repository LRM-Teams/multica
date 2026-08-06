import { createRoot } from "react-dom/client";
import type { ReactNode } from "react";
import type { ResearchV6UnknownKindDiagnostic } from "@multica/core/types/research-v6";
import { NodeRenderer } from "../../node-renderer";
import { GenericNodeCard } from "../../generic-node-card";
import { classifyNodeFamily, NODE_KIND_FAMILY_LABELS } from "../../node-kind-registry";
import {
  UI01_FIXTURE_NODES,
  UI01_STATE_NODES,
} from "../../__fixtures__/ui01-contract-fixture";
import "./demo.css";

const zooms = [0.4, 1, 1.6];

function KindGrid({ zoom }: { zoom: number }) {
  const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
  return (
    <div
      style={{
        transform: `scale(${zoom})`,
        transformOrigin: "top left",
        width: zoom <= 0.5 ? 1000 : 1400,
      }}
    >
      <div style={{ display: "flex", flexWrap: "wrap", gap: 10 }}>
        {UI01_FIXTURE_NODES.map((node) => (
          <NodeRenderer key={node.id} node={node} diagnostics={diagnostics} zoom={zoom as 0.4} />
        ))}
      </div>
    </div>
  );
}

function StateMatrix({ zoom }: { zoom: number }) {
  const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
  return (
    <div
      style={{
        transform: `scale(${zoom})`,
        transformOrigin: "top left",
        width: 1200,
      }}
    >
      <div style={{ display: "flex", flexWrap: "wrap", gap: 10 }}>
        {UI01_STATE_NODES.map((node) => (
          <NodeRenderer key={node.id} node={node} diagnostics={diagnostics} zoom={zoom as 0.4} />
        ))}
        {/* explicit generic degradation card */}
        <GenericNodeCard
          nodeId="run:f:1"
          kind="totally_future_type"
          title="未来的未知类型"
          summary="这个 kind 未注册，必须降级为 generic 且页面不崩溃。"
          status="pending"
          zoom={zoom as 0.4}
        />
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section style={{ marginBottom: 28 }}>
      <h2 style={{ fontSize: 16, margin: "12px 14px", color: "#333" }}>{title}</h2>
      {children}
    </section>
  );
}

function App() {
  const params = new URLSearchParams(window.location.search);
  const zoomParam = params.get("zoom");
  const shownZooms = zoomParam ? [Number(zoomParam)] : zooms;
  return (
    <main style={{ padding: 16 }}>
      <h1 style={{ fontSize: 22, margin: "0 0 4px 14px", color: "#111" }}>
        Research V6 · 30 类节点卡片渲染器 — 40% / 100% / 160%
      </h1>
      <p style={{ margin: "0 14px 16px", color: "#666", fontSize: 13 }}>
        六族 mapping → NodeRenderer；未知 kind → GenericNodeCard。全部为语义 token，无 hex/palette。
      </p>

      {shownZooms.map((z) => (
        <section key={z} data-zoom-section={z} style={{ marginBottom: 28 }}>
          <h2 style={{ fontSize: 16, margin: "12px 14px", color: "#333" }}>{`缩放 ${Math.round(z * 100)}%`}</h2>
          <KindGrid zoom={z} />
        </section>
      ))}

      <h2 style={{ fontSize: 18, margin: "28px 14px 0", color: "#111" }}>八态矩阵</h2>
      <p style={{ margin: "4px 14px 12px", color: "#666", fontSize: 13 }}>
        default / selected / loading / running / failed / stale / terminal / unknown（generic）
      </p>
      <Section title="">
        <StateMatrix zoom={1} />
      </Section>

      <p style={{ fontWeight: 600, marginTop: 8 }}>
        已注册族：{Object.entries(NODE_KIND_FAMILY_LABELS).map(([k, v]) => `${k}=${v}`).join(" · ")}
      </p>
      <p style={{ color: "#888", fontSize: 12 }}>
        共 {UI01_FIXTURE_NODES.length - 1} 个已知 kind + 1 个未知 kind；射线 {classifyNodeFamily({ id: "x", node_kind: "task", run_id: "y" }, []).family}
      </p>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<App />);
