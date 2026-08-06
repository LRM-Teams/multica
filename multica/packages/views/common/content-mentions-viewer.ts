// Re-export from core so channel UI and realtime notification gating share one
// mention detector (WeChat-style: group @me/@all, DM all messages).
export {
  contentMentionsViewer,
  messageMentionsViewer,
} from "@multica/core/channels";
