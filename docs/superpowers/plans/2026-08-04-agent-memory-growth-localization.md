# 智能体记忆成长区汉化记录

日期：2026-08-04

目标：完整汉化智能体资料中的记忆成长区，包含标题、进度、当前阶段、下一阶段和分段提示。

## 执行记录

- [x] 审计四种语言的 `agents.memory_growth`，确认此前均使用英文 `Memory growth / Next / writes`。
- [x] 检查数据来源，确认服务端通过稳定 `tier` ID 与英文 `tier_label` 同时返回青铜、白银、黄金、铂金四个阶段。
- [x] 为四种语言增加阶段名称并翻译标题、下一阶段与记忆更新次数。
- [x] 组件按稳定 `tier` ID 读取本地化名称；未知的新阶段才使用服务端名称，保留前向兼容。
- [x] 当前阶段、四段进度条提示和下一阶段均使用本地化名称，避免只翻标题后仍显示 `Silver / Gold`。
- [x] 新增简体中文回归测试，验证页面不存在 `Memory growth / Next / Silver / Gold / writes`。
- [x] `pnpm --filter @multica/views typecheck` 通过。
- [x] 记忆成长与语言包 parity 测试：2 个文件、192 条通过。
- [x] 变更文件 ESLint 与 `git diff --check` 通过。
- [x] React Doctor 以 `origin/dev` 为基线扫描 2 个变更源文件，未报告本次改动的具体问题。

## 中文文案

- `Memory growth` → `记忆成长`
- `Next · Gold` → `下一阶段 · 黄金`
- `5 / 6 writes` → `5 / 6 次记忆更新`
- `Bronze / Silver / Gold / Platinum` → `青铜 / 白银 / 黄金 / 铂金`

## 提交

- [x] 非草稿 PR：[#2150](https://github.com/LRM-Teams/multica/pull/2150)，目标分支 `dev`。
- [x] PR 提交后不代替用户合并，继续处理下一项问题。
