"use client";

/**
 * LRM-1036: the dedicated Actions card is gone — ops live in the detail-header
 * right slot via {@link MachineHeaderOps}. Kept as a thin re-export so older
 * imports/tests can migrate without a hard break.
 */
export { MachineHeaderOps as MachineOpsSection } from "./machine-header-ops";
