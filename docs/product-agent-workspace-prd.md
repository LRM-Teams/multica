# Agent Workspace / Memory PRD v0

- Task: #204（Parker）· 独立于 #188 P1，不阻塞 #197/#198
- 作者: Parker - Product Manager · 日期: 2026-07-03
- 参照: Raft agent workspace（本 PRD 作者每天就活在一个里：MEMORY.md/notes/artifacts 跨会话持久，owner 可通过 Workspace tab 浏览）
- 关联: #188 PRD v2.3 §4.1（三存储层/workspace_file_ref/显式 Save）、§15（justification + 代码事实）

## 0. 为什么要做（已核代码）
Multica 今天**没有**持久 per-agent workspace：只有 per-run workdir（`/multica_workspaces/{ws}/{taskShort}/workdir`，GC 不复用）、project 绑定目录、`agent_memory` 表（runtime 同步的记忆记录，非可浏览文件区）。后果：**agent 每次 run 失忆，攒不出专长**；产出文件随 run 消失；用户看不到 agent 的"家"。Raft 证明了反面：agent 有持久 workspace 后能跨会话积累知识/工作副本，owner 可直接浏览（Frank 日常在用）。

## 1. 模型
**Agent Workspace = 每个 agent 一个持久、私有、受管的文件区。**
- **持久**：跨 run/跨唤醒/跨 daemon 重启保留；不进 run GC。
- **私有**：属于该 agent；默认只有 agent 自己 + 有权 human（见 §4）可见。
- **受管**：位于平台管理的根目录下（非任意 host 路径），大小/权限/审计可控。

**目录约定（对齐 Raft，运行时提示词引导，不强制 schema）**：
- `MEMORY.md` — 恢复入口/索引（每次唤醒先读）
- `notes/` — 长期知识/偏好/频道背景
- `artifacts/` — 长期产出（文档/图/数据）
- 其余自由；平台只管根目录、配额与审计，不管内部结构。

**三层边界（复用 #188 v2.3 §4.1，已锁）**：
| 层 | 定位 | 生命周期 |
|---|---|---|
| Product Attachment Store | 会话文件 source of truth | 跟随消息 |
| Run Workspace | 每 run 临时工作区（附件 materialize、工具中间产物） | TTL/GC |
| **Agent Workspace（本 PRD）** | agent 长期私有区 | 持久，仅显式 Save/agent 主动写入 |

## 2. 物理位置与访问（关键设计点，交 BE 评估）
**推荐：workspace 落在 agent 所在 daemon host 上**，受管根如 `~/.multica/agents/{agent_id}/workspace/`（对齐 Raft 拓扑：我的 workspace 就在 Frank 的机器上，平台中介访问）。
- 服务端不复制全量文件；**浏览/下载经 daemon API 中介**，server 做权限裁决 + 审计。
- **离线态**：daemon 离线 → workspace 显示"不可访问（主机离线）"，不是空目录（边界态显式）。
- 备选（BE 权衡）：server 侧对象存储镜像/同步——成本高，v0 不推荐；`agent_memory` 表保持现状（结构化记忆记录），与文件 workspace 并存，后续再议统一。

## 3. 读写流（动作合约）
- **agent 写**：run 中 agent 可直接读写自己 workspace（运行时挂载/路径注入）；run workdir 的产物要长期保留 = agent 显式移入 workspace（或用户点「Save to agent workspace」）。
- **agent 读附件**：默认 fetch 到 Run Workspace（临时）；**只有显式 Save 才进 Agent Workspace**（#188 已锁，防变相囤积会话文件绕权限）。
- **分享出去**：`Share as attachment`（upload→attachment_id→parts）或 `Share workspace file`（workspace_file_ref → 浏览视图）。裸 host path 不能当聊天链接（#188 已锁）。
- **human 浏览**：agent 档案页 **Workspace tab**（对齐 Raft）：目录树/文件预览/下载/复制 workspace_file_ref。

## 4. 权限
- **agent 本人**：读写自己的 workspace；不能读其他 agent 的。
- **human**：agent owner + workspace admin 可浏览/下载（Raft 同款：Frank 能看我的）；普通成员默认不可见，除非收到 workspace_file_ref 且 ref 权限允许（ref 可撤销）。
- **run/任务**：run 只能访问所属 agent 的 workspace；project 目录/其他 agent workspace 不跨。
- 敏感兜底：workspace 里可能出现凭据类文件——浏览界面对命中 secret 形状的内容打码提示；不阻断 owner/admin。

## 5. 审计
- 读写/Save/Share/删除均记审计（actor、路径、时间、来源 run/context_pack id）；run 中读写自动挂到该 run 的 context_pack（#188 已定 pattern：记录 ref 类型与读取）。
- admin 有 workspace 审计视图（谁的 agent、增长量、异常写入）。

## 6. 容量 / 清理 / 导出 / 删除
- **配额**：per-agent 上限（默认建议 1-5GB，可配）；接近/超限显式提示 agent 与 owner，超限写入失败（显式错误，不静默截断）。
- **清理**：平台不自动删 workspace 内容（它是长期记忆）；提供 owner/admin 手动清理 + 大文件盘点视图；只有 agent 被删除时走保留期→销毁。
- **导出**：owner/admin 可一键导出 zip（迁移/备份）。
- **删除 agent**：workspace 进入保留期（建议 30 天，可配）后销毁；期间可导出/恢复。

## 7. 边界状态
daemon 离线→「主机离线」态｜配额超限→显式失败｜文件被 agent 运行中修改→浏览显示最后修改时间｜workspace_file_ref 指向已删文件→tombstone｜无权限→"无权访问"不泄文件名｜agent 迁移到新机器→workspace 迁移方案（v1 手动导出导入，自动迁移后续）。

## 8. 分期
- **v1**：受管根目录 + agent 读写 + Workspace tab 浏览/下载 + workspace_file_ref 分享 + 显式 Save + 基础审计 + 配额上限。
- **v2**：导出 zip / 删除保留期 / admin 盘点视图 / secret 打码 / 迁移工具。
- **后续**：`agent_memory` 表与文件 workspace 的关系统一；MEMORY.md 约定进运行时提示词（让 Multica agent 像 Raft agent 一样"醒来先读索引"）。

## 9. 验收（v1）
1. 同一 agent 跨 run/重启读到自己上次写的文件；run GC 不触碰 workspace。
2. Workspace tab：owner/admin 可浏览/下载；普通成员不可见；ref 分享可用且可撤销。
3. 附件 fetch 默认进 Run Workspace；只有显式 Save 进 Agent Workspace。
4. 跨 agent 隔离：A 读不到 B 的 workspace（含 run 内路径逃逸防护）。
5. daemon 离线显示离线态；配额超限显式失败；审计可查每次 Save/Share。
6. naming：用户见「Workspace」；不出现 storage/durable 等工程词。

## 10. 待拍 / 开放点
1. 物理位置（§2 推荐 daemon host + 中介访问）——BE 评估后定。
2. 配额默认值、删除保留期时长——产品参数，实现时定。
3. 普通成员经 ref 访问的粒度（单文件 vs 目录）——v1 建议仅单文件。
