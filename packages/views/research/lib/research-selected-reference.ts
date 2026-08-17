import type {
  ResearchGraphNode,
  ResearchSelectedReference,
} from "@multica/core/types";
import type { ResearchV6DirectorEntityKind } from "@multica/core/types/research-v6-director";

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const SHA256_PATTERN = /^sha256:[0-9a-f]{64}$/i;
const RESEARCH_REF_KINDS = new Set<string>([
  "goal",
  "branch",
  "task",
  "attempt",
  "work_item",
  "agent",
  "result",
  "insight",
  "discussion",
  "dispute",
  "integration",
  "report",
  "source_snapshot",
  "observation",
  "claim",
  "evidence_link",
] satisfies readonly ResearchV6DirectorEntityKind[]);

/**
 * Build a message reference only from the immutable canonical ref projected by
 * the server. Missing revision/hash means the node remains selectable on the
 * canvas but cannot be attached to Director context; the UI never invents an
 * identity from the display node id.
 */
export function researchSelectedReferenceFromNode(
  node: ResearchGraphNode,
): ResearchSelectedReference | null {
  if (!node.payload || typeof node.payload !== "object") return null;
  const canonicalRef = (node.payload as Record<string, unknown>).canonical_ref;
  if (!canonicalRef || typeof canonicalRef !== "object") return null;
  const ref = canonicalRef as Record<string, unknown>;
  if (
    typeof ref.kind !== "string" ||
    typeof ref.id !== "string" ||
    typeof ref.revision !== "number" ||
    !Number.isInteger(ref.revision) ||
    ref.revision < 1 ||
    typeof ref.content_hash !== "string" ||
    !RESEARCH_REF_KINDS.has(ref.kind) ||
    !UUID_PATTERN.test(ref.id) ||
    !SHA256_PATTERN.test(ref.content_hash)
  ) {
    return null;
  }

  return {
    stable_id: `${ref.kind}:${ref.id}`,
    kind: ref.kind,
    entity_id: ref.id,
    revision: ref.revision,
    content_hash: ref.content_hash,
    display_summary: (node.title || node.summary || ref.kind).slice(0, 4_096),
  };
}
