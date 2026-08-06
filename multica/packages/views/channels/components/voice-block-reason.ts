/**
 * Why the voice recording button is unavailable (#858).
 *
 * The mic is disabled by several independent conditions ORed together, and
 * before this every one of them produced the SAME sentence — "Clear the current
 * text and attachments before recording" — which is a dead end for a user whose
 * composer is empty but who has an unsent recording waiting. A control that is
 * merely disabled says nothing; one that explains itself wrongly sends people
 * somewhere that does not help, which is worse.
 *
 * So each cause resolves to exactly one reason with a sentence that is true for
 * that state, and the caller renders it in a visible status line.
 */
export type VoiceBlockReason =
  | "starting"
  | "uploading"
  | "pending_voice"
  | "sending"
  | "text_draft"
  | "attachment_draft"
  | "text_and_attachment_draft";

/**
 * The mic's OWN capture phase (#858).
 *
 * Typed rather than a `busy: boolean`, because "acquiring the microphone" and
 * "uploading the finished recording" are different states and the boolean
 * collapsed them — which made the shell announce "uploading your voice message"
 * while `getUserMedia` was still pending and nothing had been uploaded. That is
 * the same defect this feature exists to remove, one state earlier.
 *
 * `recording` is deliberately absent: while recording the control is the STOP
 * button and is not blocked at all.
 */
export type VoiceCapturePhase = "idle" | "starting" | "uploading";

export interface VoiceBlockInputs {
  /**
   * Deliberately NOT the attachment tray's `hasUploading` (Iris, #858): an
   * uploading PDF must never be described as "uploading your voice message".
   * Attachment uploads reach this resolver through `hasAttachmentDraft`, and
   * get the attachment sentence.
   */
  capturePhase: VoiceCapturePhase;
  /** An unsent recording is waiting for THIS target (channel or thread). */
  pendingVoice: boolean;
  /** A message send is in flight. */
  sending: boolean;
  /** The composer holds text. */
  hasTextDraft: boolean;
  /**
   * The attachment tray holds at least one item, in ANY status.
   *
   * `pending.length > 0`, not `hasUploading` — the tray disables the mic while
   * anything sits in it, uploaded or not (channels-page voiceDisabled). Note
   * `hasUploading` is a strict subset of this, so "uploading but nothing
   * pending" is not a reachable state.
   */
  hasAttachmentDraft: boolean;
}

/**
 * First matching cause wins. Order is a product decision (Iris, #858):
 * operations already in flight outrank recoverable work, and "you typed
 * something" comes last because it is the one cause a user can see for
 * themselves.
 *
 * `text_and_attachment_draft` is its own result rather than falling back to
 * either single cause. Telling someone with BOTH to "send or clear the text"
 * leaves the mic disabled after they comply — the same defect this type exists
 * to remove, one step further along.
 */
export function resolveVoiceBlockReason(inputs: VoiceBlockInputs): VoiceBlockReason | null {
  if (inputs.capturePhase === "starting") return "starting";
  if (inputs.capturePhase === "uploading") return "uploading";
  if (inputs.pendingVoice) return "pending_voice";
  if (inputs.sending) return "sending";
  if (inputs.hasTextDraft && inputs.hasAttachmentDraft) return "text_and_attachment_draft";
  if (inputs.hasTextDraft) return "text_draft";
  if (inputs.hasAttachmentDraft) return "attachment_draft";
  return null;
}
