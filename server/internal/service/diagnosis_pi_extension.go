// SPDX-License-Identifier: Apache-2.0

package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// GenerateDiagnosisPiExtension writes a reviewed, fixed TypeScript source to a
// file under the supplied root directory. The file registers exactly six tools
// that call the diagnosis API — the local loopback tool server (deprecated
// server mode) or the remote network diagnosis-run API (sandbox mode,
// https). The caller supplies the root (must be a directory) and the file
// permissions to apply. The API URL and bearer token are read from the Pi
// process environment at runtime; they are never embedded in the generated
// source.
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

// DiagnosisPiExtensionSource returns the reviewed extension TypeScript source
// without writing it to the local filesystem. The sandbox orchestrator uses it
// to push the extension into the sandbox workdir via the daemonws file-ops
// channel instead of a host temp file.
func DiagnosisPiExtensionSource() string {
	return diagnosisExtensionSource
}

// diagnosisExtensionSource is the reviewed, fixed TypeScript for the diagnosis
// Pi extension. It registers six capability-scoped tools that communicate
// exclusively with the diagnosis API (loopback tool server or remote https
// run API). The extension never exposes generic HTTP, file-system, or shell
// access. Credentials are read from the process environment and never appear
// in source, prompts, or tool results.
const diagnosisExtensionSource = `// Multica Diagnosis Agent Tools — trusted Pi extension (generated).
// Registers six capability-scoped tools that call the diagnosis API: the
// local loopback tool server (deprecated server mode) or the remote network
// diagnosis-run API (sandbox mode, https). Credentials come from the process
// environment.
//
// TODO(agent): KNOWN GAP (spec 005) — the sandboxed pi process currently runs
// with FULL built-in tool access. The daemon task path (agent-inbox enqueue)
// has no DisableTools/TrustedExtensionPaths plumbing, so unlike the
// server-mode loopback session the sandbox agent is restricted to these tools
// by system-prompt instruction only. Do NOT attempt daemon-side ExecOptions
// plumbing from here; the fix belongs in the daemon task protocol.

const API_URL = process.env.MULTICA_DIAGNOSIS_API_URL;
const CAPABILITY_TOKEN = process.env.MULTICA_DIAGNOSIS_CAPABILITY_TOKEN;

if (!API_URL) {
  throw new Error("multica diagnosis extension: MULTICA_DIAGNOSIS_API_URL is not set");
}
if (!CAPABILITY_TOKEN) {
  throw new Error("multica diagnosis extension: MULTICA_DIAGNOSIS_CAPABILITY_TOKEN is not set");
}

// The network run API mounts the tool endpoints directly under the run base
// (https://<host>/api/v1/diagnosis-runs/{runID}/<endpoint>); the deprecated
// loopback tool server mounts the same endpoints under /v1.
const PATH_PREFIX = API_URL.indexOf("/diagnosis-runs/") >= 0 ? "" : "/v1";

function redact(text: string): string {
  // Never surface the capability token, even if a server error echoes it.
  return text.split(CAPABILITY_TOKEN).join("[redacted]");
}

function authHeaders(): Record<string, string> {
  return {
    Authorization: "Bearer " + CAPABILITY_TOKEN,
    "Content-Type": "application/json",
  };
}

async function apiPost(path: string, body?: unknown): Promise<unknown> {
  const url = API_URL + PATH_PREFIX + path;
  const init: RequestInit = {
    method: body !== undefined ? "POST" : "GET",
    headers: authHeaders(),
  };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
  }
  let resp: Response;
  try {
    resp = await fetch(url, init);
  } catch (err) {
    // Network/DNS/TLS failures (unreachable https host, certificate errors)
    // reject before any response exists. Surface the underlying cause —
    // undici carries TLS details on err.cause — never the token or full URL.
    const detail = err instanceof Error ? err.message : String(err);
    const causeObj = err instanceof Error ? (err as { cause?: { message?: string; code?: string } }).cause : undefined;
    const cause = causeObj ? "; cause: " + (causeObj.code || causeObj.message || String(causeObj)) : "";
    throw new Error(redact("diagnosis API request failed for " + path + ": " + detail + cause));
  }
  // Do NOT surface the raw response or token in tool results.
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    // Cap and redact any error the server echoes.
    const capped = text.length > 512 ? text.slice(0, 512) + "..." : text;
    throw new Error(redact("diagnosis API " + resp.status + ": " + capped));
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
      return apiPost("/get-segment-messages", {
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
      return apiPost("/record-step-rewards", params);
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
      return apiPost("/diagnosis-progress");
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
      return apiPost("/finish-segment", { segment_id: params.segment_id });
    },
  });

  pi.registerTool({
    name: "multica_get_task_context",
    description:
      "Fetch the root task context (goal and gold/acceptance criteria) for this diagnosis run. Call this once at the start: the bootstrap prompt deliberately omits goal/gold, so scoring must be grounded in the context returned here. Only served by the network diagnosis-run API (sandbox mode).",
    parameters: {
      type: "object",
      properties: {},
      additionalProperties: false,
    },
    async handler() {
      return apiPost("/task-context");
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
      return apiPost("/complete-diagnosis");
    },
  });
}
`
