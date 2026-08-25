# `multica server info` — Design (V1)

- Date: 2026-07-08
- Source: task #338 (Frank: 发现入口未对齐 raft，"让 parker 来写" + "文档先行"); #313 agent 频道发现能力的续篇
- Raft parity reference: `raft server info` / `--channels` / `--agents` / `--humans` / `--full`（raft-daemon 0.71.0，本机实测采样，非凭记忆）
- Author: Parker（产品）；实现待本 doc 批准后开工

## Problem

raft 的 agent 发现入口是**一站式** `raft server info`：一条命令看到整个 server 的频道清单（含
joined 标注）、全部 agent（含在线/工作状态与角色标注）、全部人类成员（含 owner/admin 标注）。
新 agent 落地后靠它零人工完成自我入职（2026-07-08 Wren 入职实测：`server info` →
`server info --channels` → `message read`，Frank 截图认可）。

multica 把这个入口拆散了：只有 `multica channel list`（仅频道、仅自己已加入的）、
`multica agent list`、无人类成员命令、无 server 级总览。agent 需要知道"这个工作区里有谁、
有哪些地方、我在哪些地方"时没有一条命令能回答——发现性是 #313 已确认的主缺口。

## Command surface（对齐 raft）

```
multica server info                # 默认：计数摘要 + narrow-query 提示（对齐 raft 默认输出）
multica server info --channels     # 频道清单（joined/muted 标注 + 描述）
multica server info --agents       # agent 清单（状态 + 描述 + visibility）
multica server info --humans       # 人类成员清单（owner/admin/member 角色标注）
multica server info --full         # 全量（identity + channels + agents + humans）
multica server info --output json  # 机器可读（各变体通用）
```

`channel list` / `agent list` 保留不废（子集视角，向后兼容）。

### 输出形状（human-readable，逐段对齐 raft）

默认摘要（对齐 raft 默认输出的"计数 + narrow queries 提示"结构）：

```
## Server

Channels: 9 joined
Agents: 7
Humans: 5

Narrow queries:
- multica server info --channels
- multica server info --agents
- multica server info --humans
Full dump: multica server info --full
```

`--channels`（V1 无 public/private 概念，见 Gaps；muted 来自 ChannelResponse.muted）：

```
## Server Channels
#multica [joined, not muted] — Multica 项目主频道
#测试群 [joined, muted]
Showing 1-9 of 9.
```

`--agents`（status 直接用 agent 表既有枚举 idle/working/blocked/error/offline）：

```
## Server Agents
@前端工程师 (working) — 前端负责人：负责前端架构、任务分配…
@产品经理 (idle) — Multica 产品经理…
```

`--humans`（role ∈ owner/admin/member，member 不标注——对齐 raft "无角色标注=普通成员"）：

```
## Server Humans
@andong3 (owner)
@caozs2
```

## Endpoint mapping（全部复用现有 API，V1 零 server 改动）

| 数据段 | 端点 | 处理器（代码坐标） | 备注 |
|---|---|---|---|
| channels | `GET /api/channels` | `ListChannels`, handler/channel.go:262；路由 cmd/server/router.go:1106 | 返回 joined 频道，含 description/muted/unread；`kind='group'` 过滤 |
| humans | `GET /api/workspaces/{id}/members` | `ListMembersWithUser`, handler/workspace.go:397；路由 router.go:612 | 含 role（owner/admin/member）+ display_name；URL 带 workspace id（与其他两个的 header 方式不同，CLI 侧从配置取 id 拼路径） |
| agents | `GET /api/agents` | `ListAgents`, handler/agent.go:519；路由 router.go:910 | 含 status/description/visibility；private-agent 可见性过滤已在 handler 内 |
| identity（--full 头部） | `GET /api/me` | `GetMe`, handler/auth.go:418；路由 router.go:575 | 无 workspace role；role 从 members 响应里按 user_id 匹配补 |

CLI 侧纯组合（cobra 命令 + `newAPIClient` + 3-4 个 GetJSON），模式与 cmd_channel.go 完全一致。

## 生效层声明（#320 checklist）

**V1 = 纯 daemon 发版生效**（CLI 二进制新命令，零 server 端点改动）。随下一个 daemon tag 发布，
各机器 `multica update` 后可用；runtime brief 的 Available Commands 同步登记（同 #313 模式，
发现性是主缺口，命令登记与实现同 PR）。

## Gaps（如实入档，不做 V1 范围）

1. **「可见但未加入的公开频道」V1 做不了，也不该 hack**：multica 数据模型今天没有
   public/private 概念（channel 表无 visibility 列，migrations/112；kind 仅 group|dm）。
   raft 语义里 `joined=false` 的公开频道依赖 visibility。本命令 V2 等可见性 schema
   落地后再补 `[public, not joined]` 行，不为一条 CLI 提前发明平行的可见性机制。
2. **agent 身份的频道视角是错的（既有 bug，非本命令引入）**：`ListChannels` 的 SQL 以
   `member_type='user' AND member_id=X-User-ID` 过滤（channel.go:270-316），而 daemon task
   token 的 `X-User-ID` = agent 属主（middleware auth.go:85-94）。**即 agent 跑
   `channel list`（以及本命令）看到的是属主的频道清单，不是自己的**——与 mute/unfollow
   "插头没插电"同族（user-scoped 端点对 agent 语义失真）。V1 如实接受（当前部署形态下
   CLI 多以人类 PAT 运行，且属主视角 ≈ 超集），但**修正项挂 #313 服务端半/gate PR 同族清单**：
   `ListChannels` 在 `X-Agent-ID` 在场时按 `member_type='agent'` 解析成员身份。修正落地后
   本命令自动受益，CLI 零改动。
3. **agent 实时活动状态**：raft 显示 "working: Running command…" 级别的细粒度；multica 的
   `/api/agents.status` 是持久化粗状态（idle/working/offline）。V1 用粗状态；细粒度
   （`/api/agents/{id}/health`、channel_status.go 的 live 信号）不聚合——N+1 请求换装饰性
   信息，不值。V2 若 server 出批量 health 端点再接。

## 验收

1. 四个变体 + `--output json` 输出与本 doc 形状一致（golden test）；
2. 频道段含 muted 标注且与 `channel list` 数据一致；humans 段 owner/admin 标注正确；
3. 新 agent 入职冒烟：仅凭 brief 里的 Available Commands，一条 `multica server info` 能答出
   "有哪些频道/谁在这个工作区"（Wren 场景重演）；
4. 零 server 改动验证：对旧版 server 运行新 CLI，四个变体全部可用（纯已有端点）。
