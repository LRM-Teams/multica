export type SandboxNodeStatus = "online" | "offline";
export type SandboxInstanceStatus =
  | "pending"
  | "creating"
  | "running"
  | "failed"
  | "stopping"
  | "stopped"
  | "resuming"
  | "reconfiguring"
  | "snapshotting";
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
  instance_count?: number;
  created_at: string;
  updated_at: string;
}

export interface SandboxTemplate {
  template_id: string;
  status: string;
  created_at?: string;
  image_info?: string;
  instance_type?: string;
  last_error?: string;
  version?: string;
  job_id?: string;
  is_default?: boolean;
}

export interface SandboxNodeTemplatesResponse {
  templates: SandboxTemplate[];
  default_template_id?: string;
  synced_at?: string;
  node_online?: boolean;
}

export interface DockerImage {
  image_ref: string;
  repository: string;
  tag: string;
  id: string;
  digest?: string;
  created_at?: string;
  created_since?: string;
  size?: string;
}

export interface SandboxNodeDockerImagesResponse {
  images: DockerImage[];
  synced_at?: string;
  node_online?: boolean;
  error?: string;
}

export interface SandboxBinding {
  id: string;
  workspace_id: string;
  node_id: string;
  node_key: string;
  node_owner_user_id: string;
  node_name: string;
  node_status: SandboxNodeStatus;
  node_last_seen_at?: string | null;
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
  type:
    | "create"
    | "stop"
    | "resume"
    | "delete"
    | "reconfigure"
    | "create_template"
    | "delete_template"
    | "exec"
    | "message";
  status: SandboxJobStatus;
  payload: Record<string, unknown>;
  result: Record<string, unknown>;
  error: string | null;
  task_token?: string;
  created_at: string;
  updated_at: string;
}

export type SandboxSnapshotStatus = "creating" | "ready" | "failed" | "deleting";

export interface SandboxSnapshot {
  id: string;
  workspace_id: string;
  node_id: string;
  instance_id?: string;
  creator_user_id?: string;
  cube_snapshot_id: string;
  name: string;
  description: string;
  status: SandboxSnapshotStatus | string;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateSandboxSnapshotRequest {
  name: string;
  description?: string;
}

/** One Pi provider entry stored on a sandbox instance. */
export interface SandboxRuntimeProviderConfig {
  provider: string;
  api_key?: string;
  base_url?: string;
  model?: string;
}

/**
 * Sandbox runtime model configuration.
 *
 * New shape uses `providers` + `default_*`. Legacy flat `api_key` / `base_url` /
 * `model` / `provider` are still accepted and written from the default entry so
 * older Cube templates keep working.
 */
export interface SandboxRuntimeConfig {
  providers?: SandboxRuntimeProviderConfig[];
  default_provider?: string;
  default_model?: string;
  /** @deprecated Prefer providers[].provider / default_provider */
  provider?: string;
  /** @deprecated Prefer providers[].api_key */
  api_key?: string;
  /** @deprecated Prefer providers[].base_url */
  base_url?: string;
  /** @deprecated Prefer providers[].model / default_model */
  model?: string;
}

export interface CreateSandboxRequest {
  node_id?: string;
  template?: string;
  docker_image?: string;
  name?: string;
  limits?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  runtime?: SandboxRuntimeConfig;
}

export interface UpdateSandboxRequest {
  name: string;
  runtime?: SandboxRuntimeConfig;
}
