# 荣誉系统残留文案汉化记录

日期：2026-08-04

目标：移除中文荣誉页面、身份徽章和通知中写死的 `XP`、`LV.`、`Lv.` 与英文匿名名称，同时保持其他语言显示不变。

## 执行记录

- [x] 阅读 `apps/docs/content/docs/developers/conventions.zh.mdx`，确认 Agent 译为“智能体”，通用概念完整翻译，插值文案由语言包控制。
- [x] 审计智能体荣誉页、个人荣誉页、智能体侧栏、成员侧栏、智能体列表、频道身份名称和解锁通知。
- [x] 确认根因：多个 React 组件直接拼接 `XP` / `LV.`，因此中文语言包即使已翻译也无法覆盖。
- [x] 为四种语言增加经验数值、经验单位、匿名开发者和通用荣誉等级文案，保持语言包键结构一致。
- [x] 中文荣誉等级统一为“第 N 级”，经验单位统一为“经验”；个人与智能体荣誉页面、侧栏、频道身份徽章和通知均改为读取语言包。
- [x] 修正经验变动展示的符号拼接，负数不再显示为 `+-N`。
- [x] 新增中文荣誉文案回归测试，并把智能体成就卡、荣誉侧栏、解锁通知和共享身份徽章测试切到中文断言。
- [x] `pnpm --filter @multica/views typecheck` 通过。
- [x] 相关测试 8 个文件、296 条通过，2 条既有跳过；语言包 parity 通过。
- [x] 变更文件 ESLint 与 `git diff --check` 通过。
- [x] React Doctor 以 `origin/dev` 为基线扫描 12 个变更源文件，未报告本次改动的具体问题。

## 完整测试中的独立问题

- `packages/views/channels/components/dm-conversation-message-actions.test.tsx` 的 4 条失败来自测试替身缺少 `getAgentHonorLevel`；修复已单独提交 PR #2131，本分支不重复修改。
- `packages/views/editor/readonly-content.test.tsx` 的 KaTeX 测试独立运行仍失败；本分支未修改编辑器或数学公式代码。该问题可能影响真实用户，汉化 PR 提交后切独立分支复现页面行为。

## 边界

- `memory_growth` 中的 `Next` / `writes` 属于记忆成长模块，不混入荣誉系统 PR；后续作为独立汉化项处理。
- 不修改服务端字段名、API 的 `xp` 标识或代码变量名；只处理用户可见文案。

## 提交

- [x] 非草稿 PR：[#2148](https://github.com/LRM-Teams/multica/pull/2148)，目标分支 `dev`。
- [x] PR 提交后不代替用户合并，继续处理下一项问题。
