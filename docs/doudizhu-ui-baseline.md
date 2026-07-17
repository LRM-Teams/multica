# 斗地主 UI 视觉资产基线

## 视觉方向

- 方向：原创「青金牌桌 + 手绘徽章」；对标欢乐斗地主的清晰层级、反馈密度和牌桌手感，但不复制竞品原图。
- 色彩：深青绿牌桌 `#082d27` / `#116344`，暖金强调 `#d9a54b`，牌面米白 `#fff8ea`，红花色 `#b42028`，黑花色 `#1d2730`。
- 字体建议：牌面使用 Georgia/Times 类衬线字形占位，前端可替换为可商用扑克牌字体；界面中文用项目现有字体栈。
- 版权：本目录 SVG 均为本次原创矢量规范，未直接抄竞品素材；可在项目内商用/改作。若后续替换开源字体或图标，需在本表补 license。

## 资产清单

| 路径 | 尺寸 | 用途 | 覆盖状态 | 来源 / license |
| --- | --- | --- | --- | --- |
| `docs/assets/doudizhu/cards/card-face-template.svg` | 180x252 | 普通牌牌面模板，前端按点数/花色替换角标和中心符号 | 默认普通牌 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/cards/card-back-dragon.svg` | 180x252 | 牌背，发牌、未揭示手牌、牌堆 | 背面 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/cards/joker-black.svg` | 180x252 | 小王牌面 | 大小王 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/cards/joker-red.svg` | 180x252 | 大王牌面 | 大小王 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/cards/card-states.svg` | 780x260 | 手牌状态规范图 | 默认、选中、可出、不可出/已出 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/identity/landlord-badge.svg` | 160x160 | 地主身份徽章 | 桌面座位、结算、身份揭示 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/identity/farmer-badge.svg` | 160x160 | 农民身份徽章 | 桌面座位、结算、身份揭示 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/identity/role-reveal-panel.svg` | 720x280 | 身份揭示视觉规范 | 翻身份/座位徽章飞入 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/feedback/bid-landlord.svg` | 160x160 | 叫地主/抢地主按钮与结果反馈 | 叫抢地主 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/feedback/double-super.svg` | 160x160 | 加倍/超级加倍反馈 | 加倍、超级加倍 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/feedback/bomb-burst.svg` | 160x160 | 炸弹爆点反馈 | 炸弹、王炸 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/feedback/spring-ribbon.svg` | 160x160 | 春天奖励反馈 | 春天、反春天 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/feedback/result-panel.svg` | 720x360 | 胜负结算面板 | 胜利/失败/再来一局 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/previews/lobby-preview.svg` | 1200x720 | 大厅状态预览 | 大厅/开始游戏 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/previews/table-playing-preview.svg` | 1200x720 | 牌桌出牌中预览 | 出牌中/中央出牌区/手牌状态 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/previews/bidding-double-preview.svg` | 1200x720 | 叫抢/加倍预览 | 叫地主、抢地主、加倍、超级加倍 | 原创 SVG，项目内可用 |
| `docs/assets/doudizhu/previews/settlement-preview.svg` | 1200x720 | 结算预览 | 胜负结算、炸弹/春天倍率、再来一局 | 原创 SVG，项目内可用 |

## 状态与动效规范

- 手牌默认态：牌面保持米白底、暖金细边和柔和阴影，避免高饱和荧光；排列改为正排，选中时整体上浮 `20px`。
- 可出态：保留原牌色，增加绿色外描边和底部光条；仅用于系统提示，不强行放大，避免抢过当前选牌焦点。
- 不可出/已出态：降低饱和度并叠加暗青遮罩；已出牌在中央出牌区缩小为 `0.72` 倍，透明度 `0.92`。
- 叫抢地主：按钮使用 `bid-landlord.svg` 星冠符号；点击后按钮弹起 `scale(1 -> 1.08 -> 1)`，桌面中心显示 500ms 金色脉冲。
- 加倍/超级加倍：使用 `double-super.svg` 六边徽章；超级加倍在徽章后叠加 2 圈扩散描边，时长 680ms。
- 炸弹/王炸：使用 `bomb-burst.svg`；中央出牌区先震屏 `6px` 两次，再出现爆点 SVG 和粒子散开，整体 900ms 内结束。
- 春天/反春天：使用 `spring-ribbon.svg`；结算前从顶部飘落，伴随倍率条点亮。
- 胜负结算：使用 `result-panel.svg` 结构；胜利为暖金标题，失败可沿用结构但标题改冷蓝灰，主按钮始终为“再来一局”。

## 四类预览图

- 大厅：`docs/assets/doudizhu/previews/lobby-preview.svg`
- 牌桌出牌中：`docs/assets/doudizhu/previews/table-playing-preview.svg`
- 叫抢/加倍：`docs/assets/doudizhu/previews/bidding-double-preview.svg`
- 结算：`docs/assets/doudizhu/previews/settlement-preview.svg`

## 交给小林接入的组件清单

- `PokerCard`：接入 `card-face-template.svg` 的牌框/质感规则，并为点数、花色、大小王、背面提供 `variant`；状态 props 覆盖 `selected/playable/disabled/played/back`。
- `RoleBadge`：接入 `landlord-badge.svg`、`farmer-badge.svg`，用于座位角标、身份揭示动画、结算列表。
- `ActionFeedbackBadge`：接入 `bid-landlord.svg`、`double-super.svg`、`bomb-burst.svg`、`spring-ribbon.svg`，按关键事件播放一次性动效。
- `RoleRevealOverlay`：按 `role-reveal-panel.svg` 做身份揭示层，徽章从座位飞入桌面中心。
- `SettlementPanel`：按 `result-panel.svg` 做胜负结算，展示身份、分数、炸弹/春天/加倍倍率和“再来一局”。
- `GamePreviewAssets`：开发调试页可直接展示四张 preview SVG，方便后续评审视觉方向。
