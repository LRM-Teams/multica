# Release Guide

发布版本的成熟度由源分支决定，不由发布者临时选择：

| 源分支 | 允许的版本 | 更新的 Computer 环境 |
|---|---|---|
| `dev` | `vX.Y.Z-alpha.N`、`vX.Y.Z-beta.N` | `test` |
| `main` | `vX.Y.Z` | `production` |

禁止从 feature branch 发布，禁止从 `dev` 发布正式版，也禁止从 `main`
发布预发布版。`.github/workflows/release.yml` 会校验 tag 格式，并确认 tag
指向对应源分支中的提交。

## 选择版本

- 同一阶段继续迭代时递增序号，例如 `alpha.4 → alpha.5`、
  `beta.2 → beta.3`。
- 从 alpha 进入 beta 时从 `beta.1` 开始。
- 开始下一条测试版本线时，在最新正式版的 patch 上加一，并从
  `alpha.1` 开始，例如 `v0.4.24 → v0.4.25-alpha.1`。
- 正式发布默认递增最新正式版的 patch；如发布负责人指定版本，以指定
  版本为准。
- 已存在的 tag 和不可变发布对象不得覆盖或复用。

## 发布步骤

先确认目标 PR 已合入对应分支，CI 已通过。始终对远端分支提交打 tag，
不要对当前本地工作分支打 tag。

测试版本：

```bash
git fetch origin dev --tags
git tag -a vX.Y.Z-alpha.N origin/dev -m "<release summary>"
git push origin refs/tags/vX.Y.Z-alpha.N
```

正式版本：

```bash
git fetch origin main --tags
git tag -a vX.Y.Z origin/main -m "<release summary>"
git push origin refs/tags/vX.Y.Z
```

推送 tag 会触发 GitHub Actions 的 `Release` workflow。该 workflow 运行
Go tests、构建并发布 CLI/daemon archives、backend/web images、Helm chart，
并更新 Computer 的 canonical release feed。预发布只更新 test，正式版只
更新 production。

## 验收与失败处理

发布完成必须同时确认：

1. `Release` workflow 全部成功；
2. GitHub Release 中存在对应 tag 和产物；
3. canonical Computer feed 中对应环境指向新版本。

workflow 失败时先读取失败 job。不要重复推送同一个 tag，也不要覆盖已
发布对象；修复代码或 workflow 后使用新的版本号重新发布。删除远端 tag
或 Release、改写 tag、手工改包源都属于破坏共享发布状态的操作，必须由
发布负责人单独明确批准。
