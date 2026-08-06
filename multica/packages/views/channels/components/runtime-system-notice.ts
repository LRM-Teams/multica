import type { ChannelMessage } from "@multica/core/types";

const LEGACY_RUNTIME_SYSTEM_MESSAGE_KINDS = new Set([
  "runtime_outdated",
  "runtime_missing",
  "runtime_disconnected",
]);

const LEGACY_RUNTIME_NOTICE_CONTENT = new Set([
  "daemon_outdated",
  "daemon_missing",
  "daemon_disconnected",
  "Local daemon is outdated.",
  "Install the Multica CLI and start the daemon.",
  "Local daemon is disconnected.",
  "本地守护进程已过期，需要更新。",
  "需要安装 Multica CLI 并启动守护进程。",
  "本地守护进程未连接。",
]);

export function isLegacyRuntimeSystemNotice(message: ChannelMessage): boolean {
  if (message.type !== "system") return false;

  const kind = (message as { system_message_kind?: unknown }).system_message_kind;
  if (
    typeof kind === "string" &&
    LEGACY_RUNTIME_SYSTEM_MESSAGE_KINDS.has(kind)
  ) {
    return true;
  }

  return LEGACY_RUNTIME_NOTICE_CONTENT.has(message.content.trim());
}
