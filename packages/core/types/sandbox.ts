export type SandboxNodeStatus = "online" | "offline";
export type SandboxInstanceStatus =
  | "pending"
  | "creating"
  | "running"
  | "failed"
  | "stopping"
  | "stopped"
  | "resuming";
export type SandboxJobStatus =
  | "queued"
  | "dispatched"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface SandboxNode {
  id: string;
  node_key: string;
  owner_user_id: string;
  name: string;
  status: SandboxNodeStatus;
  capabilities: unknown[];
  max_concurrency: number;
  metadata: Record<string, unknown>;
  last_seen_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface SandboxBinding {
  id: string;
  workspace_id: string;
  node_id: string;
  node_key: string;
  node_owner_user_id: string;
  node_name: string;
  node_status: SandboxNodeStatus;
  enabled: boolean;
  policy: Record<string, unknown>;
  created_at: string;
}

export interface SandboxInstance {
  id: string;
  workspace_id: string;
  creator_user_id: string;
  node_id: string;
  node_key?: string;
  node_name?: string;
  node_status?: SandboxNodeStatus;
  status: SandboxInstanceStatus;
  template: string;
  local_ref: string | null;
  endpoint_info: Record<string, unknown>;
  limits: Record<string, unknown>;
  metadata: Record<string, unknown>;
  error: string | null;
  created_at: string;
  updated_at: string;
}

export interface SandboxJob {
  id: string;
  workspace_id: string;
  initiator_user_id: string;
  node_id: string;
  instance_id: string;
  type: "create" | "stop" | "resume" | "delete" | "exec" | "message";
  status: SandboxJobStatus;
  payload: Record<string, unknown>;
  result: Record<string, unknown>;
  error: string | null;
  task_token?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateSandboxRequest {
  node_id?: string;
  template?: string;
  name?: string;
  limits?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  runtime?: Record<string, string>;
}

export interface UpdateSandboxRequest {
  name: string;
  runtime?: Record<string, string>;
}
