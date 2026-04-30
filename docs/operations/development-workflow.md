# Development Workflow

详细开发、测试与交付流程。

- **CLAUDE.md**：项目宪法，包含不可违反的强制规则。
- **本文档**：操作细节、完整命令、角色定义与背景说明。

---

## Build & Test Commands

### agentd / agentgw (Go)

```bash
cd agentd  # or agentgw
go build ./cmd/agentd/          # build binary
go test ./... -v -timeout 30s   # run all unit tests
go test ./internal/config/... -v  # run single package tests
go test -tags integration ./... -v -timeout 30s  # include integration tests
```

### agentapp (Flutter)

```bash
cd agentapp
flutter pub get                  # install dependencies
flutter test -v                  # run all tests
flutter test test/models_test.dart -v  # run single test file
flutter analyze                  # lint/static analysis
```

---

## Repo Scripts

所有构建、部署、发布、安装操作必须通过仓库脚本执行，禁止手动 `go build` + `scp`。

### `scripts/build.sh` — 增量构建

支持基于内容哈希的增量缓存（Go 二进制）和时间戳缓存（Flutter 产物）。并行构建所有平台。

```bash
./scripts/build.sh              # 构建全部（默认）
./scripts/build.sh go           # 仅 Go 二进制（agentd + agentgw，全平台）
./scripts/build.sh agentd       # macOS agentd
./scripts/build.sh agentd-linux # Linux amd64 agentd
./scripts/build.sh agentgw      # macOS agentgw
./scripts/build.sh agentgw-linux# Linux amd64 agentgw
./scripts/build.sh apk          # Android APK
./scripts/build.sh ipa          # iOS IPA
./scripts/build.sh web          # Flutter Web 静态资源
```

**输出位置**：
- `out/darwin-arm64/agentd` — macOS daemon
- `out/darwin-arm64/agentgw` — macOS gateway
- `out/linux-amd64/agentd` — Linux daemon (amd64)
- `out/linux-amd64/agentgw` — Linux gateway (amd64)
- `out/android/agentapp.apk` — Android APK
- `out/ios/agentapp.ipa` — iOS IPA
- `out/static/` — Web 静态资源

### `scripts/deploy.sh` — 部署

构建 + 部署到本地/远程/移动设备。自动检测连接的手机并安装。

```bash
./scripts/deploy.sh             # 全量：构建全部 + 部署 local + remote + 重启 agentgw
./scripts/deploy.sh build       # 构建全部 + 自动安装到已连接设备
./scripts/deploy.sh server      # 仅服务端：构建 Go + 部署 local + remote + 重启 agentgw
./scripts/deploy.sh local       # 本地：macOS agentd + agentgw，重启 local agentd + agentgw
./scripts/deploy.sh ws          # 远程：Linux agentd 部署到 $REMOTE_HOST，重启 agentgw
./scripts/deploy.sh gw          # 仅重启 agentgw
./scripts/deploy.sh apk         # 仅构建 APK + 安装 Android
./scripts/deploy.sh ipa         # 仅构建 IPA + 安装 iOS
./scripts/deploy.sh mobile      # 不重新构建，直接安装现有 APK/IPA 到设备
./scripts/deploy.sh web         # 构建 Flutter Web 并同步到 agentgw/static
./scripts/deploy.sh flutter-android  # 使用 flutter install 刷入 Android
./scripts/deploy.sh flutter-ios      # 使用 flutter install 刷入 iOS
./scripts/deploy.sh sim         # 构建并安装到 iOS Simulator
./scripts/deploy.sh cfgutil     # 通过 Apple Configurator 2 安装现有 IPA
./scripts/deploy.sh devices     # 列出已连接的移动设备
```

**环境变量**：
- `REMOTE_HOST` — 远程 SSH host（默认：`ws`）
- `AGENTGW_HUB` — Tunnelhub base URL
- `AGENTGW_TUNNEL_URL` — 完整 tunnel URL（覆盖 AGENTGW_HUB）

**部署原则**：
- 远程 agentd 必须以普通用户运行（非 sudo），否则 `os.UserHomeDir()` 返回 `/root`，watcher 找不到 session 文件。
- 上传二进制到临时文件名，停止进程后再 `mv` 替换，避免 SCP 覆盖运行中文件失败。
- 重启 agentd 后 agentgw 的 proxy 连接会断开，脚本自动重启 agentgw 重建 WS tunnel。

### `scripts/install.sh` — 安装与本地服务管理

首次安装或日常管理本地 agentgw + agentd。扫描 SSH config 发现远程节点并可选部署。

```bash
# 首次安装（交互式）
./scripts/install.sh
./scripts/install.sh --token mytoken
./scripts/install.sh --local-only       # 仅本地 agentgw，不部署远程
./scripts/install.sh --no-browser       # 安装后不自动打开浏览器

# 日常管理
./scripts/install.sh restart            # 重启本地 agentgw + agentd（幂等）
./scripts/install.sh stop               # 停止本地服务
./scripts/install.sh status             # 查看服务状态与日志位置
./scripts/install.sh --help
```

**环境变量**：
- `AGENTGW_HUB` — Tunnelhub base URL（默认：`https://ilovin.xyz`）
- `AGENTGW_TUNNEL_URL` — 完整 tunnel URL
- `AGENTGW_APP_URL` — App-facing remote URL
- `AGENTGW_REALITY_PUB` / `AGENTGW_REALITY_SID` / `AGENTGW_REALITY_SNI` — REALITY 配置

**安装后产物**：
- `~/.agentgw/agentgw` — gateway 二进制
- `~/.agentgw/config.json` — 配置（token、nodes、port）
- `~/.agentgw/static/` — Web 静态资源
- `~/.agentgw/runtime.env` — 运行时环境变量（用于 restart 恢复参数）
- `~/.agentgw/local_auth.json` — 本地凭据

### `scripts/release.sh` — 发布打包

构建所有产物并打包为可分发 tarball，含 manifest.json 和 SHA256 校验。

```bash
./scripts/release.sh                    # 完整发布
./scripts/release.sh --skip-apk         # 跳过 Android APK
./scripts/release.sh --skip-ios         # 跳过 iOS IPA
VERSION=v0.5.0 ./scripts/release.sh     # 强制指定版本
```

**输出**：`release/phone-talk-vX.Y.Z.tar.gz`

**包内容**：
- `bin/agentd` / `bin/agentd-linux` / `bin/agentgw-macos-arm64` / `bin/agentgw-linux`
- `bin/agentapp.apk` / `bin/agentapp.ipa`（如构建成功）
- `install.sh` — 一键安装脚本
- `scripts/` — 辅助脚本
- `static/` — Web 静态资源
- `README.md` / `VERSION` / `manifest.json`

### `scripts/tunnelhub.sh` — TunnelHub 管理

启动/停止/管理 TunnelHub WebSocket 中继和可选的 cloudflared 隧道。

```bash
./scripts/tunnelhub.sh start
./scripts/tunnelhub.sh start --cloudflared      # 同时启动 cloudflared 隧道
USERS="alice:token1;bob:token2" ./scripts/tunnelhub.sh start --cloudflared
./scripts/tunnelhub.sh stop
./scripts/tunnelhub.sh restart
./scripts/tunnelhub.sh status
./scripts/tunnelhub.sh logs
./scripts/tunnelhub.sh url              # 查看当前 cloudflared 隧道 URL
```

---

## Development Workflow

### MUST

- Follow **test-driven development (TDD)** for all non-trivial changes.
- After **every development step or code change**, run the relevant tests before continuing.
- Minimum test expectation per change:
  - backend changes: targeted Go unit tests
  - app changes: targeted Flutter tests
  - cross-component/session changes: relevant integration tests
- For session/chat pipeline work, use `agentgw/test_plan.md` as the executable test plan.
- Final acceptance is gated by **real Chrome interaction in the existing tab**; do not treat a change as done until Chrome validation passes.
- Any debug scripts, screenshots, temporary captures, and generated debug artifacts must go under `agentapp/scripts/debug/` (or equivalent component-local `scripts/debug/`). Do not leave debug artifacts in the project root.

### Debug Tools

#### `scripts/debug/web_debug.py` — Web UI 诊断脚本

用于 attach 到已打开的 Chrome 标签页（`localhost:7374`），收集诊断信息、探测 WebSocket、截图。

```bash
# Playwright 模式（默认）：attach 到已有 Chrome tab
python3 scripts/debug/web_debug.py

# 原生 CDP 模式（无需 Playwright）：attach 或创建新 tab
python3 scripts/debug/web_debug.py --raw
```

**前置条件**：Chrome 必须以 `--remote-debugging-port=9222 --remote-allow-origins='*'` 启动，且已打开 `http://localhost:7374`。

**输出**：
- Console 日志（Flutter/Web 运行时输出）
- 诊断信息（Flutter 加载状态、localStorage、性能指标、Service Worker）
- WebSocket 连通性探测
- 全页截图（`scripts/debug/screenshot.png`）

### SHOULD

- Prefer running targeted tests first, then broaden scope only when the change surface is large.
- Keep changes minimal and directly scoped to the requested task.

---

## Manager-Driven Delivery Workflow

### MUST

- Use a single **Manager** role to run delivery end-to-end: requirement intake, clarification/discussion, task decomposition, assignment, progress tracking, and final status sync.
- Manager must delegate implementation to teammates/sub-agents and **must not implement code directly**.
- Manager may create up to **5 teammates** for parallel development.
- Manager must define clear scope and acceptance criteria before assigning work.
- Manager must track teammate ownership and progress continuously, and re-balance assignments when blocked.
- Manager must ensure all delegated work follows this repo's TDD/test/validation requirements.

### Role Assignment

- **Manager**: owns requirement alignment, decomposition, assignment, dependency management, progress tracking, and release readiness decision.
- **Developer teammate**: implements assigned tasks and self-checks against acceptance criteria.
- **Reviewer teammate**: reviews code quality/scope/risk; should not be the same teammate as the primary developer for that task whenever possible.
- **Tester/Acceptance teammate**: runs test and acceptance checklist, validates behavior/regression, and records acceptance evidence.

### Documentation Requirement (mandatory)

- Keep requirement and progress summaries in docs, separated by purpose:
  - `docs/requirements/` — requirement discussion outcomes, scope, acceptance criteria, task split, assignees. Each requirement is an independent file (`r-XXX-*.md`).
  - `docs/management/progress.md` — execution progress, task status, blockers, risks, ETA updates, completion summary.
- Update both documents whenever scope/assignment/progress changes materially.

### Acceptance Criteria (Definition of Done)

A task is accepted only when all items below are satisfied:

1. Requirement scope and expected output are matched (no hidden scope expansion).
2. Required tests for the change type are executed and passing (unit/integration/UI as applicable).
3. Reviewer sign-off is recorded with no unresolved critical issues.
4. For UI-related work, real Chrome interaction validation in the existing tab is completed and recorded.
5. Risks/blockers/follow-ups are documented in `progress.md`.
6. Requirement-to-delivery status is updated in both the relevant `requirements/r-XXX-*.md` and `progress.md`.

### SHOULD

- Prefer small, independently testable task slices per teammate.
- Keep at least one teammate slot free for urgent fixes/review support when possible.
- Prefer assigning reviewer and tester as different teammates on medium/large changes.

---

## Conventions

- Commit messages: `feat(component):`, `fix(component):`, `chore:` prefix style
- Go module paths: `github.com/phone-talk/agentd`, `github.com/phone-talk/agentgw`
- Feature branches: `feature/<name>`, developed in git worktrees under `.worktrees/`
- Integration tests gated by `//go:build integration` build tag
- Design docs live under `docs/designs/`, plans under `docs/plans/`, requirements under `docs/requirements/`. See `docs/README.md` for full navigation.
