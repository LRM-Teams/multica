# PR CI 按影响面跑测试

日期：2026-08-20  
状态：已拍板

## 问题

PR 合进 `dev` 会部署 s89。当时每次 CI 全量跑约 1.2 万条测试（Go ~7013 + web 图 Vitest ~5223），一条红就不能 merge。改文案或单包也会拉完整仓。

## 口径

- **只改 CI**。测试文件不删。本地 `make check` / `pnpm test` / `go test ./...` 仍是全量。
- **开发阶段 = PR → `dev` → s89**。不在这次加夜间全量。
- **测这次 PR 影响到的部分**，不是固定抽 200 条冒烟，也不是只按文件名对 `*_test.go`。

## 影响面怎么算

相对 base（PR 用 `origin/dev`，push 到 `dev` 用 `github.event.before`）：

- **前端**：Turbo `...[base]`（变更包 + 依赖它们的包），再排除 `@multica/desktop` / `@multica/mobile` / `@multica/docs`。Web-only 产品门不变。
- **后端**：变更文件映射到 Go 包，再用 `go list` 的 `Deps` 做反向闭包（谁依赖这些包，谁就测）。
- **脚本门**（selfhost / web-workflows / baked-origins / reserved-slugs / migration-numbers）：只在对应文件变更时跑。
- Job 始终启动；没有影响面就空过成功。Required check 不能因为 skip job 变黄。

## 必须放大的例外

下列任一变更 → 按现网 web 范围跑完整前端门 + 完整 `go test ./...`：

- `.github/workflows/ci.yml`
- `pnpm-lock.yaml` / `pnpm-workspace.yaml` / `turbo.json`
- `server/go.mod` / `server/go.sum`
- `scripts/ci-pr-scope.sh` / `scripts/ci-expand-go-packages.sh` / `scripts/ci-turbo-web.sh`

`github.event.before` 为全零（新建分支）也走全量。

## 明确不做

- 不删测试文件。
- 不加 nightly workflow。
- 不把 E2E / desktop / mobile / docs 拉进 PR 门。
- 不再每次单独跑 `make test-agent-delivery-route`（与 `go test ./internal/daemon` 重复）。daemon 被影响到时，该包测试会跑。

## 代价

改 `packages/core`、`packages/ui`、`server/internal/handler` 这类共享包，仍会带上一大片。这是「影响到」的本意。只改单页/单包时，CI 应从一万多降到该包及其依赖方。
