// SPDX-License-Identifier: Apache-2.0

package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// GenerateDiagnosisPiExtension writes a reviewed, fixed TypeScript source to a
// file under the supplied root directory. The file registers exactly five tools
// that call the local loopback diagnosis API. The caller supplies the root
// (must be a directory) and the file permissions to apply. The API URL and
// bearer token are read from the Pi process environment at runtime; they are
// never embedded in the generated source.
func GenerateDiagnosisPiExtension(root string, perm os.FileMode) (string, error) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("diagnosis extension: root dir %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("diagnosis extension: %q is not a directory", root)
	}
	path := filepath.Join(root, "multica-diagnosis-tools.ts")
	if err := os.WriteFile(path, []byte(diagnosisExtensionSource), perm); err != nil {
		return "", fmt.Errorf("diagnosis extension: write %q: %w", path, err)
	}
	return path, nil
}

// diagnosisExtensionSource is the reviewed, fixed TypeScript for the diagnosis
// Pi extension. It registers five capability-scoped tools that communicate
// exclusively with the loopback diagnosis API. The extension never exposes
// generic HTTP, file-system, or shell access. Credentials are read from the
// process environment and never appear in source, prompts, or tool results.
const diagnosisExtensionSource = `// Multica Diagnosis Agent Tools — trusted Pi extension (generated).
// Registers five capability-scoped tools that call the local loopback
// diagnosis API. Credentials come from the process environment.

const API_URL = process.env.MULTICA_DIAGNOSIS_API_URL;
const CAPABILITY_TOKEN = process.env.MULTICA_DIAGNOSIS_CAPABILITY_TOKEN;

function authHeaders(): Record<string, string> {
  return {
    Authorization: "Bearer " + CAPABILITY_TOKEN,
    "Content-Type": "application/json",
  };
}

async function apiPost(path: string, body?: unknown): Promise<unknown> {
  const url = API_URL + path;
  const init: RequestInit = {
    method: body !== undefined ? "POST" : "GET",
    headers: authHeaders(),
  };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(url, init);
  // Do NOT surface the raw response or token in tool results.
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    // Redact the token from any error the server echoes.
    const safe = text.length > 512 ? text.slice(0, 512) + "..." : text;
    throw new Error("diagnosis API " + resp.status + ": " + safe);
  }
  return resp.json();
}

// ── Tool definitions ──

export default function (pi: any) {
  pi.registerTool({
    name: "multica_get_segment_messages",
    description:
      "Fetch one page of messages for a diagnosis segment using an opaque cursor. The first call uses an empty cursor; subsequent calls pass the next_cursor from the prior response.",
    parameters: {
      type: "object",
      properties: {
        segment_id: { type: "string", description: "The segment ID to fetch messages for." },
        cursor: { type: "string", description: "Opaque cursor from the prior response; omit or use empty string for the first page." },
      },
      required: ["segment_id"],
      additionalProperties: false,
    },
    async handler(params: { segment_id: string; cursor?: string }) {
      return apiPost("/v1/get-segment-messages", {
        segment_id: params.segment_id,
        cursor: params.cursor || "",
      });
    },
  });

  pi.registerTool({
    name: "multica_record_step_rewards",
    description:
      "Persist step rewards for a segment. Rewards are idempotent by (segment_id, seq). Scores must be between 0 and the configured score_max. Returns persisted, missing, and rejected seqs.",
    parameters: {
      type: "object",
      properties: {
        segment_id: { type: "string" },
        rewards: {
          type: "array",
          items: {
            type: "object",
            properties: {
              seq: { type: "integer", minimum: 1 },
              score: { type: "integer", minimum: 0 },
              rationale: { type: "string" },
            },
            required: ["seq", "score", "rationale"],
            additionalProperties: false,
          },
        },
      },
      required: ["segment_id", "rewards"],
      additionalProperties: false,
    },
    async handler(params: { segment_id: string; rewards: Array<{ seq: number; score: number; rationale: string }> }) {
      return apiPost("/v1/record-step-rewards", params);
    },
  });

  pi.registerTool({
    name: "multica_get_diagnosis_progress",
    description:
      "Return the authoritative diagnosis progress from the server: completed and remaining segment IDs, current segment with cursor/reward coverage. Call this after every compaction resume to reconcile the compacted memory with server state.",
    parameters: {
      type: "object",
      properties: {},
      additionalProperties: false,
    },
    async handler() {
      return apiPost("/v1/diagnosis-progress");
    },
  });

  pi.registerTool({
    name: "multica_finish_segment",
    description:
      "Mark a segment complete. The server validates that all expected messages have been fetched and all expected step rewards have been persisted. Returns 'completed' or 'incomplete' with missing seqs.",
    parameters: {
      type: "object",
      properties: {
        segment_id: { type: "string" },
      },
      required: ["segment_id"],
      additionalProperties: false,
    },
    async handler(params: { segment_id: string }) {
      return apiPost("/v1/finish-segment", { segment_id: params.segment_id });
    },
  });

  pi.registerTool({
    name: "multica_complete_diagnosis",
    description:
      "Finalize the diagnosis run after all segments are complete. The server validates full DAG coverage and transitions the run to completed. Returns missing segments if any remain.",
    parameters: {
      type: "object",
      properties: {},
      additionalProperties: false,
    },
    async handler() {
      return apiPost("/v1/complete-diagnosis");
    },
  });
}
`
