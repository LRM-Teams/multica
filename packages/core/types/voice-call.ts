export interface VoiceCall {
  id: string;
  channel_id: string;
  agent_id: string;
  status: string;
  started_at: string;
  connected_at: string | null;
  ended_at: string | null;
  end_reason: string;
  error_code: string;
  input_audio_ms: number;
  output_audio_ms: number;
  updated_at: string;
}

export interface VoiceCallMedia {
  app_id: string;
  room_id: string;
  user_id: string;
  token: string;
  expires_at: string;
}

export interface CreateVoiceCallRequest {
  channel_id: string;
  agent_id: string;
}

export interface CreateVoiceCallResponse {
  call: VoiceCall;
  media: VoiceCallMedia;
}

export interface GetVoiceCallResponse {
  call: VoiceCall;
}

export interface VoiceCallDuplexAudioHint {
  input_format: string;
  input_sample_rate: number;
  output_format: string;
  output_sample_rate: number;
}

export interface VoiceCallDuplexEventHint {
  client: string[];
  server: string[];
}

export interface StartVoiceCallDuplexResponse {
  call: VoiceCall;
  mode: string;
  ws_path: string;
  audio: VoiceCallDuplexAudioHint;
  events: VoiceCallDuplexEventHint;
}
