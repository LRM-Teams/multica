# Duplex 产品化会话网关（LRM-949）

## Goal

把 LRM-945 Spike 的豆包 Duplex 骨架接到生产 `voice-calls` 路径：服务端维持会话、
音频代理、Multica 工具桥回灌，并对 FE（LRM-950）给出稳定事件契约。不替换现网
RTC VoiceChat。

## FE / BE contract

### Lifecycle

1. `POST /api/workspaces/{id}/voice-calls` — 现有创建（拿到 call id；RTC media 可忽略）
2. `POST /api/workspaces/{id}/voice-calls/{callId}/duplex` — 激活 Duplex（不 StartVoiceChat）
3. `GET  /api/workspaces/{id}/voice-calls/{callId}/duplex/ws` — 双向 JSON WebSocket
4. `POST /api/workspaces/{id}/voice-calls/{callId}/stop` — 挂断（Duplex 活跃时跳过 RTC Stop）

### Client → server

| type | payload |
| --- | --- |
| `client.audio.append` | `audio` base64 PCM s16le **16 kHz** mono |
| `client.audio.commit` | end of user utterance |
| `client.interrupt` | barge-in (`response.cancel`) |
| `client.close` | end duplex media |

### Server → client

| type | notes |
| --- | --- |
| `duplex.ready` | `session_id`, `sample_rate=24000`, `audio_format=pcm_s16le` |
| `duplex.asr` | `phase=started\|completed`, `transcript` |
| `duplex.audio.delta` | TTS PCM base64 (24 kHz) |
| `duplex.text.delta` | optional captions |
| `duplex.tool` | `name`, `status=started\|done\|error` |
| `duplex.error` | `code`, `message` |
| `duplex.closed` | media finished |

### Env

- `DOUBAO_DIALOG_API_KEY`（必需）— 与 Spike / 部署 ambient secret 同名
- 可选：`DOUBAO_DIALOG_VOICE` / `DOUBAO_DIALOG_MODEL` / `DOUBAO_DIALOG_ENDPOINT`

## Notes

- Multica 派活复用 `VoiceCallAgentBridge`（与 RTC `delegate_work_to_multica_agent` 同语义）
- 免提 UI：LRM-947；FE 接 Duplex：LRM-950
