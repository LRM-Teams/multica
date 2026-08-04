# 只读公式渲染测试修正记录

日期：2026-08-04

目标：判断完整 views 测试中的 KaTeX 失败是否影响真实用户，并修正真实根因。

## 执行记录

- [x] 在最新 `origin/dev` 上独立运行 `readonly-content.test.tsx`，稳定复现公式断言失败。
- [x] 检查生产实现：`ReadonlyContent` 检测到公式后，通过 effect 异步加载 `remark-math`、`rehype-katex` 和样式，再触发第二次渲染。
- [x] 检查失败测试：测试在首次同步渲染后立即查找 `.katex`，仍假设插件同步加载。
- [x] 将公式用例改为等待异步插件完成，再断言行内公式与块公式的 KaTeX 标记。
- [x] 未修改生产组件，避免为了满足陈旧测试破坏按需加载与首屏体积设计。
- [x] 单文件测试 28/28 通过。
- [x] `pnpm --filter @multica/views typecheck` 通过。
- [x] 变更文件 ESLint 与 `git diff --check` 通过。
- [x] 完整 views 测试：456 个文件通过；3969 条通过，2 条预期失败，5 条跳过。

## 结论

真实用户路径会在异步插件加载后正确渲染公式；失败来自测试时序，不需要生产代码修复。

## 提交

- [x] 非草稿 PR：[#2149](https://github.com/LRM-Teams/multica/pull/2149)，目标分支 `dev`。
- [x] PR 提交后不代替用户合并，继续处理下一项问题。
