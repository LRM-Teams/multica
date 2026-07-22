# 产品表面视觉规范（冻结）

> **状态**：2026-07-22 Frank 确认 —「颜色一旦定稿，整体项目就要一致」；设计文档入库作为统一规范。  
> **关联**：LRM-226（小步统一 token）、LRM-225（手机 Members 可滚）、LRM-223/224（身份优先 Avatar）。  
> **可点对照**：[`assets/design/visual-tokens-members-mobile.html`](./assets/design/visual-tokens-members-mobile.html)

本页是 **聊天 / 成员 / 设置** 等产品表面的冻结色与交互壳规范。实现时优先映射到 `packages/ui/styles/tokens.css` 的语义 token；**禁止**页面私自另起灰紫渐变、第二套主色或硬编码一堆互不相同的 hex。

---

## 1. 冻结色板（产品表面）

| 角色 | 参考值 | 应对齐的实现 token | 用途 |
|------|--------|-------------------|------|
| 主文字 | `#1d1c1d` | `foreground` / `primary`（按场景） | 标题、列表名 |
| 次要文字 | `#616061` | `muted-foreground` | 说明、副文案 |
| 辅助文字 | `#868686` | `muted-foreground`（更淡场景） | 占位、元信息 |
| 分割线 | `#e8e8e8` | `border` | 列表行、弹窗分隔 |
| 强边框 | `#d1d1d1` | `input` | 搜索框描边 |
| 链接 / 主操作蓝 | `#1264a3` | `brand` 或 `info`（项目内统一选一，禁止混用） | 链接、主按钮、文件名强调 |
| 链接浅底 | `#e8f5fa` | `brand`/`info` 的浅透明变体 | chips、选中浅底 |
| 表面 | `#ffffff` | `card` / `popover` / `background` | 弹窗、卡片 |
| 页面底 | `#f6f6f4` | `background` / `muted` | 聊天外层浅底 |
| 成功 | `#007a5a` | `success` | 成功态 |
| 危险 | `#e01e5a` | `destructive` | 删除、危险 |

**硬规则**

1. 定稿后全站产品表面吃同一套；新页面不得再发明第三套色。
2. 代码里避免散落 `text-[#1264a3]` / `bg-[#e8f5fa]`；逐步收到 CSS 变量 / Tailwind token。
3. 大面积着色用语义色的浅透明，不用高饱和铺底。

---

## 2. 字号（产品表面）

| 角色 | 建议 | Tailwind 对齐 |
|------|------|---------------|
| 弹窗/页标题 | 15–16px | `text-base` / 略强调 |
| 正文 / 列表主行 | 13–14px | `text-sm` |
| 辅助 / handle | 11–12px | `text-xs` |

字重：正文 `font-normal`～`font-medium`；标题可用 `font-semibold`，避免满屏 `font-bold`。

---

## 3. 手机 Members 滚动壳（与 LRM-225）

- 弹窗 / Sheet **定高**，列 flex，`min-height: 0`
- 顶栏、搜索、底栏：`flex: 0 0 auto`
- **仅成员列表** `flex: 1; overflow: auto; -webkit-overflow-scrolling: touch`
- 色与字号用 §1 / §2，与桌面 Members 大弹窗一致

验收：窄屏成员超过一屏时可指滑列表；顶栏与搜索不跟着跑。

---

## 4. 头像（与 LRM-223 / 224）

对照：[`assets/design/avatar-identity-first.html`](./assets/design/avatar-identity-first.html)

- 身份优先：`actorType + actorId`；消息 URL 仅缓存加速，缺了不清空
- 占位：色圆 + 1～2 字母；禁止整词文字（如「前端」）
- Agent 状态点并进同一 Avatar 壳

---

## 5. 与 `design.md` 的关系

- [`design.md`](./design.md) 仍是全局设计哲学与 shadcn token 总册。
- **本页**冻结近期产品拍板的表面色与结构；冲突时以本页「产品表面」为准，并应回写到 `tokens.css` 消除双轨。
- 不做整站换肤；按聊天 → 成员 → 设置逐步收敛硬编码。
