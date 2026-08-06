export interface WebPushPublicKeyResponse {
  public_key: string;
  enabled: boolean;
}

export interface WebPushSubscriptionKeysPayload {
  p256dh: string;
  auth: string;
}

export interface WebPushSubscriptionPayload {
  endpoint: string;
  keys: WebPushSubscriptionKeysPayload;
  expiration_time?: number | null;
  device_id?: string;
  user_agent?: string;
}

export interface WebPushSubscriptionResponse {
  id: string;
  workspace_id: string;
  user_id: string;
  endpoint: string;
  expiration_time?: string;
  device_id?: string;
  user_agent?: string;
  last_active_at: string;
}

export interface WebPushTestResponse {
  ok: boolean;
  delivered: number;
  failed: number;
  gone: number;
  attempted: number;
}
