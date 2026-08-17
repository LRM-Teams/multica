# 时段工作介绍 — 实现 Todo（ADR 0019）

## 给实现 AI 的读法（先读这段）

1. **合同以 ADR 0019 为准**：多 runtime **采集员 Agent** → 专用 **周报 Agent** 合成。**废弃 Host Digest 作为 Brief 本机源**（ADR 0018 已 superseded）。
2. **严格按 Slice 顺序做**；一次只做一个 `- [ ]`；开干写「进行中」，做完勾 `- [x]`。
3. **复用**平台 Facts loader、Note Worker、`--note-write`；禁止回顾 API 跑模型；禁止新周报编排引擎。
4. 旧 J1–J3（Host Digest UI）代码可暂留，但新功能不得再依赖 Host 拉 Digest。

术语：`CONTEXT.md` → Period Work（Collector / Period Brief Agent / Synthesis / Brief）。

---

## 目标（一句话）

人勾选若干 **已连接 runtime 上的 Agent**，各自在所在 OS 搜集最近工作（含短 diff / 摘要 / 关键片段），再交给 **专用周报 Agent** 整理成 Notes 里的 Period Work Brief。

---

## 核心模型

```text
人：时间窗 + 勾选采集员 runtime/Agent +（默认）周报 Agent
  ├─ 平台 Facts（Issue / 笔记 / run / PR）          ← 服务端确定性
  └─ 各采集员 Agent → 所在 OS 工作痕迹包              ← Agent 工具采集
        ↓ 汇合
  周报 Agent Period Work Synthesis
        ↓
  Notes：Period Work Brief（--note-write，人确认）
```

---

## 禁止做（永久非目标）

- 键鼠、截屏、剪贴板、浏览器历史
- 全仓 / 无界正文灌进模型；密钥与 `.env` / `.ssh` 等 denylist 路径
- Agent Daily 当采集源；回顾 API 跑模型；导出 `.pptx`
- 用 Host Digest 作为 Brief 的本机必需输入
- 静默 `replace_page`；合成须人可见 `--note-write` 确认

---

## 已定决策（硬约束）

| ID | 决定 |
|----|------|
| D1 | 采集者 = **勾选的 runtime 上的 Agent**（含云端）；范围 = 该 runtime **所在 OS**（本机即整机 HOME） |
| D2 | 允许 **短 diff / 文件摘要 / 关键片段**；仍 denylist + 禁键鼠截屏剪贴板 |
| D3 | 平台 Facts 与各采集包汇合后，由 **专用周报 Agent** 合成（可改选） |
| D4 | 废弃 Host Digest 作为 Brief 本机源（ADR 0019） |
| D5 | 产物只在 Notes；不写 PPT |
| D6 | 权威：`docs/adr/0019-runtime-agent-collectors-period-brief.md`；§4.24 |

---

## Slice K0 — 合同与供给（先做）

- [x] **K0-T1 专用周报 Agent 供给**
  - **目标**：Workspace 有一个默认可选的「周报」Agent（Period Brief Agent）。
  - **要做**：创建/确保内置或 onboarding 供给模板（名称/说明/instructions 锁定采集汇总与 Brief 文风）；Notes「本期工作介绍」默认选中它。
  - **不要做**：强制用户不能改选其他 Agent。
  - **完成标准**：新/现有 Workspace 能解析到默认周报 Agent id；locale 中英说明齐全。

- [ ] **K0-T2 UI：勾选采集员 + 整理员**
  - **目标**：人选时间窗、勾选若干在线 runtime/Agent 作采集员、确认整理员（默认周报 Agent）。
  - **要做**：改 `NotePeriodBriefDialog`；云端与本地均可勾；至少选一个采集员。
  - **不要做**：再展示 Host Journal 开关当 Brief 前置条件。
  - **完成标准**：组件测：勾选集合进入 API；默认整理员为周报 Agent。

---

## Slice K1 — 采集员派发

- [ ] **K1-T1 采集员 Worker / 剧本**
  - **目标**：每个勾选 Agent 收到「在本机 OS 搜集最近工作」的指令（窗口、denylist、允许短 diff/摘要/片段、产出结构化采集包）。
  - **要做**：playbook 或专用 instruction；结果经 `--note-write` 或约定消息格式回到可汇合的载体（私有笔记子页 / 频道附件约定 — 实现时选一种并写进完成标准）。
  - **不要做**：让采集员直接写最终 Brief；Host `RequestComputerWorkDigest`。
  - **完成标准**：单采集员测：派发成功；回复含结构化工作痕迹而非空话。

- [ ] **K1-T2 编排：多采集 → 再唤醒周报 Agent**
  - **目标**：采集员完成后（或超时降级），把平台 Facts + 各采集包交给周报 Agent 合成。
  - **要做**：扩展/替换 `POST /api/notes/period-briefs`：不再拉 Host Digest；写底稿或分区 prompt；`period_brief` 合成。
  - **不要做**：回顾 API 跑模型；采集失败整单失败（应降级标明 empty）。
  - **完成标准**：Handler 测：无 Digest 调用；多 collector id 入参；合成 job 指向周报 Agent。

---

## Slice K2 — 合成与落笔

- [ ] **K2-T1 周报 Agent 合成 + `--note-write`**
  - **目标**：整理员写出 Brief；人点确认落入 `工作介绍/`。
  - **依赖**：K1-T2、现有 note_write UI
  - **完成标准**：fixture 采集包 → Brief 含主线结构；sticky 仍指向文件夹而非底稿。

- [ ] **K2-T2 拆除 Brief 对 Host Digest 的依赖**
  - **目标**：period-brief 路径不再 `fetchComputerWorkDigest`；UI/文案不再要求 Journal。
  - **不要做**：无评审大删 Journal 基础设施（可留作遗留）；只保证 Brief 主路径不依赖。
  - **完成标准**：period-brief 测无 Digest；文档/locale 与 ADR 0019 一致。

---

## 进度

| 顺序 | 下一步 |
|------|--------|
| 1 | ~~K0-T1 周报 Agent 供给~~ |
| 2 | **K0-T2** UI 勾选采集员 + 整理员 |
| 3 | K1 采集 → K2 合成 |

**当前焦点：** Slice K0。下一个 checkbox：**K0-T2**。

### 遗留说明（J1–J3）

Host Digest / Journal / 旧 period-brief Digest 汇合已按 ADR 0018 落地过一轮，**产品合同已切到 0019**。实现新切片时以本文件为准，勿再把 Host Digest 当完成标准。
