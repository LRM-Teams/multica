/**
 * LRM-1264 — one shared lowlight instance for editor + readonly surfaces.
 * Creating `createLowlight(common)` per module duplicated the language pack
 * on the heap without changing highlight behaviour.
 */
import { common, createLowlight } from "lowlight";

export const sharedLowlight = createLowlight(common);
