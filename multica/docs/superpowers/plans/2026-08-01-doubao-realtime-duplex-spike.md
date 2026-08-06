# Spike: 豆包端到端实时语音全双工 + Multica 工具桥

Issue: LRM-945

## Goal

打通「边说边聊像真人」的全双工对讲脑，并把「边打电话边干活」接到 Multica
真实工具（开 issue / delegate），结果可语音回灌。本 Spike 只交付可打断 Demo
骨架与契约文档，不替换现网 RTC VoiceChat 产品路径。

## Decision (Spike 结论)

| 选项 | 形态 | 打断 / 双工 | Multica 工具 | 结论 |
| --- | --- | --- | --- | --- |
| A. 沿用 RTC VoiceChat + ArkV3 | `StartVoiceChat` 级联 ASR→Ark→TTS；`InterruptMode=0` | 轮次级联，可打断但不是真人全双工；延迟 ~1–2s 属管线固有 | `delegate_work_to_multica_agent` + HTTP callback 已通 | **保留现网打电话** |
| B. 换豆包 S2S / Realtime Duplex | `wss://.../api/v3/duplex/realtime/dialogue`（JSON）或经典 `/api/v3/realtime/dialogue`（二进制） | 端到端语音大模型，原生 `response.cancel` / ClientInterrupt | `session.tools` + `response.function_call_arguments.done` → Multica → `conversation.item.create` 回灌 | **实现「像真人 + 边聊边干活」跟这条** |

**取舍：不在本 Spike 硬拧 ArkV3 VoiceChat 冒充全双工。** 现网 VoiceChat 继续服务可打电话；新能力跟 S2S/Duplex 开实现单。两条产品线共用「delegate 到 Multica agent」语义，但传输与回灌协议不同，不可混用 RTC `UpdateVoiceChat` 回灌 Duplex 会话。

## Why Duplex first

Issue 原文指向经典 Realtime Dialogue
(`wss://openspeech.bytedance.com/api/v3/realtime/dialogue`)。等价全双工产品
**Realtime Duplex / Seeduplex**
(`.../api/v3/duplex/realtime/dialogue`) 使用 WebSocket 文本 JSON，原生
function-call 事件更干净，Spike 代码骨架以 Duplex 为默认；经典二进制协议保留
endpoint 常量，供控制台仅开通旧产品时切换。

## Required console / env (no secrets in repo)

火山控制台需单独开通 **端到端实时语音 / 实时对话（Dialog）** 产品，与现网
`VOLCENGINE_RTC_*`（RTC + VoiceChat）以及 `DOUBAO_SPEECH_API_KEY`（流式 ASR/TTS
2.0）**不是同一产品**。能否复用同一火山账号开通，需运维在控制台确认；Key
未写入部署前通联验收保持 blocked。

| Env | 用途 | 备注 |
| --- | --- | --- |
| `DOUBAO_DIALOG_API_KEY` | Duplex 鉴权 `X-Api-Key` | **主 Key**；勿复用 RTC Secret |
| `DOUBAO_DIALOG_APP_ID` | 可选 `X-Api-App-Id` | 应用配置，非鉴权因子 |
| `DOUBAO_DIALOG_ENDPOINT` | WebSocket URL | 默认 Duplex；可改经典 dialogue |
| `DOUBAO_DIALOG_RESOURCE_ID` | 经典协议资源 | 默认 `volc.speech.dialog`（仅经典路径） |
| `DOUBAO_DIALOG_ACCESS_KEY` | 经典协议 `X-Api-Access-Key` | 仅经典二进制路径需要 |
| `DOUBAO_DIALOG_VOICE` | 输出音色 | 默认 `zh_female_vv_jupiter_bigtts` |
| `DOUBAO_DIALOG_MODEL` | Duplex model | 默认 `1.2.6.0`（以上游文档为准） |

禁止：把 Key 写进 issue / 评论 / 提交明文。Agent `custom_env` 建议名与上表一致；
`multica agent env` 仅 owner/admin 可写。

## Architecture (Spike)

```text
Mic / PCM16k ──► Duplex WS (session.create + tools)
                      │
                      ├─ ASR / TTS 事件（本地播放；ASR started → response.cancel）
                      │
                      └─ response.function_call_arguments.done
                              │
                              ▼
                     MulticaToolBridge
                              │
                              ├─ delegate_work_to_multica_agent(request)
                              │     → 现有 VoiceCallAgentBridge 语义
                              │       （开 issue / 真实 agent wake）
                              │
                              └─ conversation.item.create (role=tool)
                                      → 模型继续 TTS 语音回灌
```

代码位置：

- `server/internal/integrations/doubaodialog` — 协议客户端、工具 schema、桥接
- `server/cmd/doubao-dialog-spike` — 可打断对讲 + 假/真 Multica 执行的 CLI Demo

## Out of scope

- 完整产品 UI / 替换 FE RTC 进房
- 硬拧现有 ArkV3 VoiceChat 管线
- 生产部署接线（等 Key + 实现单）

## Acceptance mapping

1. **可打断对讲 Demo** — CLI + 协议单测；live 联调依赖 Key（缺则 blocked）
2. **Function Call → Multica** — `MulticaToolBridge` 单测证明 FC→执行→回灌；live 同 blocked
3. **env/控制台文档** — 本文件 + issue 评论
4. **取舍结论** — 上文 Decision；跟进实现单应新建，不自批本 Spike done

## Verification

```bash
cd server
go test ./internal/integrations/doubaodialog/...
# live（有 Key 时）:
# DOUBAO_DIALOG_API_KEY=... go test ./internal/integrations/doubaodialog -run Live -count=1
# DOUBAO_DIALOG_API_KEY=... go run ./cmd/doubao-dialog-spike --text '帮我开一个 issue 修登录'
```

## Live verification notes (2026-08-01)

- Duplex ingress for model turns is **audio** (`input_audio_buffer.*`); plain text user items do not start a chat turn.
- `session.tools` must be **flat** `{type,name,description,parameters}` (not nested OpenAI `function`).
- `response.output_audio.delta` carries PCM in `delta` (base64).
- Keep sending silence frames after `commit` while waiting for FC (matches upstream demo).
- Live evidence: `DOUBAO_DIALOG_LIVE=1 go test ./internal/integrations/doubaodialog -run TestLiveFunctionCallToMultica` created child issue LRM-946.
