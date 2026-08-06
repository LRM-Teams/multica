import type { DockerImage, SandboxBinding } from "@multica/core/types";

export function dockerImageLabel(image: DockerImage): string {
  return image.image_ref || [image.repository, image.tag].filter(Boolean).join(":");
}

/** Closed trigger and open list must use the same node label (not node_id). */
export function dockerNodeSelectLabel(
  binding: Pick<SandboxBinding, "node_name" | "node_status">,
): string {
  return `${binding.node_name} (${binding.node_status})`;
}

export function defaultDockerContainerName(): string {
  return `docker-${Math.random().toString(36).slice(2, 8)}`;
}
