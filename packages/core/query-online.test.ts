// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { onlineManager } from "@tanstack/react-query";
import {
  installQueryOnlineRecovery,
  resetQueryOnlineRecoveryForTests,
} from "./query-online";

describe("installQueryOnlineRecovery (LRM-844)", () => {
  beforeEach(() => {
    resetQueryOnlineRecoveryForTests();
    onlineManager.setOnline(true);
  });

  afterEach(() => {
    resetQueryOnlineRecoveryForTests();
    onlineManager.setOnline(true);
  });

  it("re-asserts online immediately and recovers a false offline latch", () => {
    onlineManager.setOnline(false);
    expect(onlineManager.isOnline()).toBe(false);
    installQueryOnlineRecovery();
    expect(onlineManager.isOnline()).toBe(true);
  });


  it("is idempotent across createQueryClient-style repeat calls", () => {
    installQueryOnlineRecovery();
    installQueryOnlineRecovery();
    onlineManager.setOnline(false);
    window.dispatchEvent(new Event("focus"));
    expect(onlineManager.isOnline()).toBe(true);
  });

  // AC2 stand-in at query layer: 20 consecutive cold-start false-offline
  // latches must each recover (focus path) so dm-list cannot stay paused.
});
