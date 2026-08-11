import { useEffect, useState } from "react";
import { MembersDirectoryPage } from "@multica/views/members";
import type { DaemonStatus } from "../../../shared/daemon-types";

/**
 * Desktop wrapper around Members Directory — forwards local daemon
 * identity so computer grouping matches Runtimes (Local section).
 */
export function DesktopMembersPage() {
  const [status, setStatus] = useState<DaemonStatus>({ state: "stopped" });
  const [lastIdentity, setLastIdentity] = useState<{
    daemonId: string | null;
    deviceName: string | null;
  }>({ daemonId: null, deviceName: null });
  const [hostName, setHostName] = useState<string | null>(null);

  useEffect(() => {
    const apply = (s: DaemonStatus) => {
      setStatus(s);
      if (s.daemonId) {
        setLastIdentity({
          daemonId: s.daemonId,
          deviceName: s.deviceName ?? null,
        });
      }
    };
    window.daemonAPI.getStatus().then(apply);
    window.daemonAPI.getHostName().then((name) => setHostName(name || null));
    return window.daemonAPI.onStatusChange(apply);
  }, []);

  const localDaemonId = status.daemonId ?? lastIdentity.daemonId;
  const localMachineName =
    status.deviceName ?? lastIdentity.deviceName ?? hostName;

  return (
    <MembersDirectoryPage
      localDaemonId={localDaemonId}
      localMachineName={localMachineName}
      hasLocalMachine
    />
  );
}
