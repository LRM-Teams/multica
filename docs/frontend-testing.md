# Frontend Testing — 重置与重建规范

**Date:** 2026-08-06 · **拍板:** Frank · **Status:** 现行规范(engineering-principles §8 指向本文)

> **一句话现状:前端测试体系已于 2026-08-06 整体清零** —— 单测套件
> (`packages/core` / `packages/views` / `apps/web`,529 文件 / ~3,700 用例)、
> Playwright `e2e/` 套件、vitest 配置、setup 文件、测量脚本、测试依赖,全部删除。
> 前端正确性门禁 = **build + typecheck + lint + React Doctor**(ci.yml)。
> 测试从零重建,**加任何 `*.test.*` / `*.spec.*` 文件前必读本文**。
> `apps/desktop` / `apps/mobile` 各自的测试设置不在本次重置范围内。

---

## 1. 为什么清零(决策链,全部有实测数据)

目标是 frontend CI job < 10 分钟。优化路径全部走完之后,结论是"不删到伤覆盖就到不了":

| 阶段 | frontend job | Test step | views 套件 |
|---|---|---|---|
| 基线(2026-08-05) | 19m44s | 13m28s | 12m17s |
| #2376 删 90 个低密度文件 | 15m58s | 10m23s | 9m14s (554s) |
| #2394 happy-dom + threads(零删除) | — | 7m45s | 403s |
| #2401 worker 调优 | 14m04s | 7m13s | 376s |

10 分钟内需要 Test ≤ ~3m35s(build ~3m30-4m + 固定开销 ~2m30s 不属于测试、动不了),
即 views ≤ ~180s——按实测换算要再删掉**一半套件成本**,贪心清单直接吃进
`issue-detail` / `login-page` / `create-issue` 这类核心页面测试。删一半留一半 =
花钱养一个不再构成安全网的套件。Frank 拍板:**整体清零,门禁收敛到静态检查,
测试按本文规矩从头重建。**

被否决的其他路线(全部实测过,**不要重走**,原始测量在
`docs/superpowers/specs/2026-07-29-frontend-test-cost-design.md` 的 2026-08-06 addendum):

- `pool: vmThreads` — 最快(83.6s),但外部模块跨 VM realm 共享 → 650+ 载入序相关失败,不确定性红线。
- `isolate: false` — vi.mock 工厂在共享 registry 竞争,同样的不确定性(2026-07-29 已测)。
- `deps.optimizer` esbuild 预打包 — 冷启动(CI 唯一形态)净变慢 + 模块双实例破坏 11 个文件。
- 合并测试文件(方案 E)— mock 工厂逐文件定制,拼接后静默互踩(2026-07-29 pilot 已证伪)。
- 并行 job 拆分 / 加 runner 分片 — Frank 否决(拓扑不动)。

## 2. 现行门禁(`可执行`,已在 CI)

1. **`turbo build typecheck lint --filter='@multica/web...'`** — TS strict 编译 + eslint + Next 生产构建。
2. **React Doctor**(changed-scope vs origin/dev,warning 即红)— React 正确性静态检查。
3. **后端 Go 测试** — 不在本文范围,照旧全量跑(`make check` = typecheck + Go tests)。

## 3. 重建规范(新增测试的门槛)

**总预算:前端 CI 测试步 wall ≤ 2 分钟。** 预算是一等约束,和正确性同级。
超预算的 PR 不能合并——预算内怎么分,按下面的层级优先级。

### 层级与优先级(先低层后高层,低层能测的禁止上高层)

- **L1 · 静态检查(已在位)** — TS strict + eslint + React Doctor,零边际成本,永远是第一道。
- **L2 · API 契约韧性测试(最先重建,唯一"强制"类别)**
  CLAUDE.md《API Response Compatibility》的配套:每个被 UI 消费的 endpoint schema
  必须有"喂畸形响应"的测试(缺字段 / 错类型 / null 数组),fail-closed。
  位置 `packages/core/api/`,node 环境,零 DOM。整层预算 ≤ 10s。
  这是 installed-app 架构唯一的防线(#2143/#2147/#2192 三次事故),清零期间此风险裸奔,
  **重建从这里开始。**
- **L3 · 纯逻辑单测** — reducer / 排序 / 过滤 / 格式化 / view-model。
  node 环境,零 DOM 零 RTL。整层预算 ≤ 30s。
- **L4 · 组件交互测试(清单制,不自由新增)** — 只覆盖清单内的关键路径:
  login、issue 创建/编辑、composer 发送、workspace 切换、频道消息渲染。
  清单外的组件测试一律拒;想加,先进清单(改本文,Frank 签)。整层预算 ≤ 80s。
- **L5 · E2E(重建而非恢复)** — 关键用户旅程的最终安全网,长期方向是它(而非组件测试)
  承接回归类覆盖。前置条件:CI 里跑通 backend+frontend 环境 + 有 owner(task #12),
  在那之前不进门禁。旧 `e2e/` 已删,历史可从 git 考古但不要照搬——旧套件从未进过 CI。

### 硬性规矩(对每个新测试文件)

1. **环境按层级钦定**:L2/L3 = node;L4 = happy-dom + `pool: "threads"` + `isolate: true`
   ——这是实测选出的栈(本地 3x、CI −35%),配置模板见 §4,不要另起炉灶。
2. **密度门槛**:单文件 ≥ 5 个用例,或单用例均摊 ≤ 150ms。
   一个文件一两个用例、却付一整份 environment+import 开销——#2376 删的就是这类,
   这次 117 个高成本文件的贪心清单里 71 个是这样长出来的 LRM 回归测试。
3. **PR 必须附数字**:新增/改动测试的 PR 在描述里贴 vitest `Duration` 行。
   预算超了,先删自己层里最不值的,不是抬预算。
4. **回归测试优先写在 L5/L2,不是 L4**:bug 回归先问:能不能锁在契约层或 E2E 旅程里?

### `可执行`化欠债(按 engineering-principles §0 标签制)

- 预算门禁(CI 里量测试步 wall、超 2min 变红)— **`仅文档`,缺 owner 认领**。
- L4 清单约束(清单外组件测试 lint 拦截)— **`仅文档`**,同上。

## 4. 重建起点:钦定栈配置(实测选型,2026-08-06)

原配置文件已随清零删除;下面的模板就是当时实测通过的形态,重建 L4 时以此为起点。

```ts
// packages/views/vitest.config.ts(重建模板)
import { availableParallelism } from "node:os";
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    pool: "threads",            // forks → threads: 同等隔离,少一层进程开销(实测 -18%)
    maxWorkers: Math.max(2, availableParallelism() - 1), // 2-vCPU runner 上 2 workers(容器实测)
    environment: "happy-dom",   // jsdom → happy-dom: env 构建便宜一个量级(本地 3x)
    environmentOptions: {
      happyDOM: {
        settings: {
          navigator: {
            // happy-dom 默认 UA 带 "Windows NT",会翻转平台检测;钉 Linux UA
            userAgent:
              "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HappyDOM/20.0.0",
          },
        },
      },
    },
    globals: true,
    isolate: true,              // 不可关——关了就是 mock 竞争 flake(已实测)
  },
});
```

已知 happy-dom 迁移坑(都遇过、都有解):
- 平台检测:默认 UA 含 Windows → 钉 UA(上面模板)。
- 可访问名:相邻 inline span 之间会插空格(`"👍1"` → `"👍 1"`)→ 断言用 `/👍\s*1/`。
- `<dialog>`:happy-dom 忠实派发 `close` 事件(jsdom 不派发)——组件若既调 `dialog.close()`
  又在元素上绑 `onClose` 会双触发;这是真 bug,修组件不是修测试(#2394 修过两处)。
- CSSOM 级联断言(getComputedStyle 继承)happy-dom 不支持 → 文件级
  `// @vitest-environment jsdom` + 注释原因,jsdom 临时加回 devDeps。

L2/L3(node 环境)模板:`environment` 留默认,`pool: "threads"` + `passWithNoTests: true` 即可。
测量工具:旧 `import-env-baseline-reporter.mjs`(git 考古 #2401 前的
`packages/views/scripts/`)可原样复活,用于密度门槛测量。
