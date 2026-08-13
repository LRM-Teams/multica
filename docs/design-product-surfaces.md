# 产品表面视觉规范（冻结）

> **状态**：2026-07-22 Frank —「颜色一旦定稿，整体项目就要一致」；设计文档入库作统一规范源。  
> **关联**：LRM-226（本规范）、LRM-227（前端落地 backlog）、LRM-225（手机 Members 可滚）、LRM-223/224（身份优先 Avatar）。  
> **可点对照**：
> - [`assets/design/visual-tokens-spec.html`](./assets/design/visual-tokens-spec.html) — 一页定稿（token + 字号 + 聊天/成员/设置）
> - [`assets/design/visual-tokens-compare.html`](./assets/design/visual-tokens-compare.html) — 色板 + 三表面对照
> - [`assets/design/visual-tokens-members-mobile.html`](./assets/design/visual-tokens-members-mobile.html) — Members 手机滚动壳

本页是 **产品表面** 的冻结色与字号规范。实现时优先映射到 `packages/ui/styles/tokens.css` 的语义 token；**禁止**页面私自另起灰紫渐变、第二套主色或散落互不相同的 hex。

---

## Frank 口径（必须遵守）

1. **定稿后全项目一致**：颜色 token 一旦冻结，全站产品表面吃同一套；新页面不得再发明第三套色。
2. **本期实现可小步**：落地顺序推荐 **聊天 → 成员 → 设置**（或同 PR）；范围不是「整站换肤」。
3. **本仓库文档是规范源**：以本页 + 上述 HTML 为准；冲突时回写 `tokens.css`，消除双轨。

---

## 1. 冻结色板

| 角色 | 参考值 | 设计 token | 应对齐的实现 | 用途 |
|------|--------|------------|--------------|------|
| 主文字 | `#1d1c1d` | `--ink` | `foreground` / `primary`（按场景） | 标题、列表名、主按钮底 |
| 次要文字 | `#616061` | `--ink-2` | `muted-foreground` | 说明、设置右侧值 |
| 辅助文字 | `#868686` | `--ink-3` | `muted-foreground`（更淡） | 占位、时间戳、图标默认 |
| 分割线 | `#e8e8e8` | `--line` | `border` | 列表行、弹窗分隔 |
| 强边框 | `#d1d1d1` | `--line-strong` | `input` | 搜索框 / 输入框描边 |
| 行 hover | `#f8f8f8` | `--hover` | `muted` / `secondary` | 列表行 hover |
| 链接 / 主操作蓝 | `#1264a3` | `--accent` | `brand` 或 `info`（项目内统一选一，禁止混用） | 链接、主强调 |
| 链接浅底 | `#e8f5fa` | `--accent-soft` | brand/info 浅透明 | chips、选中浅底、提及 |
| 表面 | `#ffffff` | `--surface` | `card` / `popover` | 弹窗、卡片 |
| 页面底 | `#f6f6f4` | `--bg` | `background` / `muted` | 列底、页面底 |
| 语义成功 | `#007a5a` | `--ok` | `success` | 成功态文案 / 图标 |
| 危险 | `#e01e5a` | `--danger` | `destructive` | 删除、移除、危险操作 |
| 在线点 | `#2bac76` | `--online` | Avatar 状态点 | **仅**头像在线点 |
| 忙碌点 | `#e8912d` | `--busy` | Avatar 状态点 | **仅**头像忙碌点 |

### 硬规则

1. **`--ok` ≠ `--online`**：语义成功用 `#007a5a`；在线状态点用 `#2bac76`。禁止混用。
2. 危险操作只用 `--danger`（`#e01e5a`）；文档告警红 `#c23b22` 不进产品壳。
3. 代码里避免散落 `text-[#1264a3]` / `bg-[#e8f5fa]`；逐步收到 CSS 变量 / Tailwind token。
4. 大面积着色用语义色的浅透明，不用高饱和铺底。
5. 禁止页面私自另起灰紫渐变、深色顶栏、第二套主色。

---

## 2. 字号 / 字重

| 角色 | 建议 | 设计名 | Tailwind 对齐 |
|------|------|--------|---------------|
| 弹窗大标题 | 18px / 700 | Title L | `text-lg` + semibold/bold |
| 频道名 / 面板标题 | 15–16px / 700 | Title M | `text-base` 略强调 |
| 成员名 / 行主文 | 13–14px / 650 | Body Strong | `text-sm` + medium |
| 消息正文 / 设置行 | 13–14px / 400 | Body | `text-sm` |
| 说明 / handle / 右侧值 | 12px / 400 | Meta | `text-xs` · ink-2 |
| 时间 / 标签 / 辅助 | 11–12px / 400 | Caption | `text-xs` · ink-3 |

字体栈：`Segoe UI, PingFang SC, Hiragino Sans GB, Noto Sans SC, sans-serif`（与现网一致即可）。  
圆角：面板 / 弹窗 **12** · 控件 **8** · 头像圆。

---

## 3. 三表面对照（本期落地范围）

| 表面 | 必须吃同一 token | 要点 |
|------|------------------|------|
| **Chat** | ink / ink-2 / ink-3 / line / line-strong / accent | 消息名 700 · 正文 13–14 · 时间 Caption · 输入框边 `line-strong` |
| **Members** | 同上 + hover + surface | 大弹窗与设置「成员」Tab **共用列表**；行 hover = `--hover` |
| **Settings** | 同上 + danger | Tab 12/700，激活底边 ink；危险操作只用 `--danger` |

对照图：打开 [`visual-tokens-compare.html`](./assets/design/visual-tokens-compare.html) 或同目录 PNG。

**Messages 两列不得同色**：workspace 导航吃 `--sidebar`（`bg-sidebar` / page-bg）；会话列表吃 `--surface`（`bg-background`）。选中行用 `bg-muted`，hover 用 `bg-accent`（`--hover`）。禁止把列表再锁回 sidebar chrome，否则两列糊成一块。契约在 `conversation-sidebar-styles.ts`。

**本期不做**：整站换肤、暗色模式、换品牌主色、侧栏导航批发重画、营销页。

---

## 4. 手机 Members 滚动壳（与 LRM-225）

- 弹窗 / Sheet **定高**，列 flex，`min-height: 0`
- 顶栏、搜索、底栏：`flex: 0 0 auto`
- **仅成员列表** `flex: 1; overflow: auto; -webkit-overflow-scrolling: touch`
- 色与字号用 §1 / §2，与桌面 Members 大弹窗一致

验收：窄屏成员超过一屏时可指滑列表；顶栏与搜索不跟着跑。可点示意见 [`visual-tokens-members-mobile.html`](./assets/design/visual-tokens-members-mobile.html)。

---

## 5. 头像（与 LRM-223 / 224）

对照：[`assets/design/avatar-identity-first.html`](./assets/design/avatar-identity-first.html)（及 LRM-223 `design-avatar-spec-b`）

- 身份优先：`actorType + actorId`；消息 URL 仅缓存加速，缺了不清空
- 占位：色圆 + 1～2 字母；禁止整词文字（如「前端」）
- Agent 状态点并进同一 Avatar 壳；状态点色用 §1 的 `--online` / `--busy` 等，**不要**用 `--ok`

本规范不重开头像设计审。

---

## 6. 默认 / 加载 / 空 / 错误

各表面沿用既有交互态，**只换色号**，不另起文案体系：

| 态 | 色 |
|----|----|
| 默认 | ink + surface + line |
| 加载 | muted / skeleton（现有） |
| 空 | ink-2 说明文案 |
| 错误 / 危险 | danger |
| 不可用 | ink-3 + 降低对比，不另起灰紫 |

---

## 7. 前端交接

1. 抽共享 CSS 变量（或 theme map），值以上表为准；三表面禁止本地再定义主色。
2. 实现落在 LRM-227；可与 LRM-225 同 PR 或先后合入 `dev`。
3. PR 附运行截图，对照本页 HTML / PNG。
4. 合入后删 feature 分支。

---

## 8. 与 `design.md` 的关系

- [`design.md`](./design.md) 仍是全局设计哲学与 shadcn token 总册。
- **本页**冻结近期产品拍板的表面色与结构；冲突时以本页「产品表面」为准，并应回写到 `tokens.css` 消除双轨。
