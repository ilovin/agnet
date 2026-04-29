# Testing Guide

## Unified Test Entry Point: `scripts/test.sh`

The project provides `scripts/test.sh` as the unified entry point for running tests.

### Usage

```bash
./scripts/test.sh [SUBCOMMAND] [OPTIONS]
```

### Subcommands

| Subcommand | Description |
|------------|-------------|
| `unit`     | Run all Go unit tests (non-integration) across `agentd/` and `agentgw/` |
| `flutter`  | Run Flutter unit tests (default) or integration tests in Chrome (`-d chrome`) |
| `smoke`    | Run deployment smoke tests (build + local deploy + health checks) |
| `help`     | Show usage information |

### Examples

```bash
# Run all Go unit tests
./scripts/test.sh unit

# Run Flutter unit tests
./scripts/test.sh flutter

# Run Flutter integration tests in Chrome
./scripts/test.sh flutter -d chrome

# Show help
./scripts/test.sh help
```

### Behavior

- `unit`: Runs `go test ./...` in both `agentd/` and `agentgw/` directories.
  - Excludes integration tests (files tagged with `//go:build integration`).
  - Prints a consolidated pass/fail summary with per-module results.
- `flutter`: Runs `flutter test` in `agentapp/`.
  - With `-d chrome`, runs integration tests against an **already running** Flutter web app.
  - The script detects the `flutter run -d chrome` process, extracts its VM service URL, and connects via `flutter drive --use-existing-app=<url>`.
  - **Never launches a new Chrome tab or window** (TEST-004).
- Exits with a non-zero status if any test fails.

### Flutter Integration Test Prerequisite

Before running `./scripts/test.sh flutter -d chrome`, start the Flutter web app in a separate terminal:

```bash
cd agentapp && flutter run -d chrome --web-port 8080
```

Wait for the app to fully start and for the VM service URL to be printed (e.g., `http://127.0.0.1:xxxxx/yyyyy=/`). Then run the integration tests in another terminal:

```bash
./scripts/test.sh flutter -d chrome
```

If no running Flutter web app is detected, the script prints a helpful message with the exact command to start it.

---

## Deployment Smoke Tests (TEST-005)

### Purpose

Smoke tests verify that the project's deployment scripts (`build.sh`, `deploy.sh`) work correctly and that the built services start up and respond to health checks.

### What Smoke Tests Cover

| Test | What It Verifies |
|------|------------------|
| **Build** | `scripts/build.sh go` compiles all Go binaries without errors |
| **Artifacts** | Expected binaries exist in `out/darwin-arm64/` and `out/linux-amd64/` |
| **Local Deploy** | `scripts/deploy.sh local` prepares local runtime artifacts |
| **Health Checks** | agentd (port 7373) and agentgw (port 7374) respond with valid JSON |
| **Static Files** | agentgw serves the Flutter web app at `http://localhost:7374/` |
| **Cleanup** | Services can be stopped and ports are freed (only when test started them) |

### Running Smoke Tests

```bash
# Run deployment smoke tests
./scripts/test.sh smoke
```

### Behavior

- **Prerequisites check**: Verifies `go` and `curl` are installed.
- **Port conflict detection**: If ports 7373/7374 are already in use, the test skips starting new services and only runs health checks against the existing ones. It will NOT stop pre-existing services to avoid disrupting your development environment.
- **Build verification**: Runs `scripts/build.sh go` and checks that all four expected binaries are created.
- **Health verification**: Queries `/status` endpoints and validates JSON responses contain a `version` field.
- **Cleanup**: If the smoke test started the services, it runs `scripts/install.sh stop` and verifies ports are freed.

### Prerequisites

- Go toolchain installed
- `curl` installed
- Ports 7373 and 7374 available (or existing services running)
- `~/.agentgw/config.json` with a valid `token` field (for agentgw health check authentication)

### Exit Codes

- `0`: All smoke tests passed
- `1`: One or more smoke tests failed

---

## Flutter Integration Tests

### Setup

The `integration_test` package is declared as a dev dependency in `agentapp/pubspec.yaml`:

```yaml
dev_dependencies:
  flutter_test:
    sdk: flutter
  integration_test:
    sdk: flutter
```

### Test File

Integration tests live in `agentapp/integration_test/app_test.dart`.

The current test verifies:
1. The Flutter web app builds and loads successfully.
2. The `Agent Manager` title is rendered on the initial `/connections` route.
3. Expected UI elements (e.g., `IconButton` widgets) are present.

### Running Integration Tests

```bash
# 1. Start the Flutter web app in a terminal
cd agentapp && flutter run -d chrome --web-port 8080

# 2. In another terminal, run integration tests against the existing app
./scripts/test.sh flutter -d chrome
```

The test connects to an **already running** Flutter web app in Chrome, per TEST-004. The script never launches a new Chrome tab or window.

**Prerequisites for web integration tests:**
- A Flutter web app must already be running via `flutter run -d chrome`.
- The script auto-detects the VM service URL from the running process.

---

### Example Output

```
========================================
       DEPLOYMENT SMOKE TESTS
========================================

[smoke] Checking prerequisites...
[smoke] Prerequisites OK

[smoke] TEST 1/3: Build smoke test
[smoke] Running: scripts/build.sh go
[smoke] Build: PASS
[smoke] Artifacts verified in out/

[smoke] TEST 2/3: Local deploy smoke test
[smoke] Running: scripts/deploy.sh local
[smoke] Local deploy: PASS

[smoke] TEST 3/3: Health check smoke test
[smoke] agentd health (port 7373): PASS (version: agentd v0.1.0)
[smoke] agentgw health (port 7374): PASS (version: v0.1.0)
[smoke] agentgw static files: PASS

[smoke] Cleanup
[smoke] Cleanup: PASS (ports freed)

========================================
         SMOKE TEST SUMMARY
========================================
  Build          : PASS
  Local Deploy   : PASS
  Health Checks  : PASS
  Cleanup        : PASS
========================================
All smoke tests passed.
```

---

# Browser Testing Plan

## Goal
Establish an end-to-end testing system with browser display as the sole acceptance criterion.

## Approach: Playwright + integration_test

### 1. Playwright (JavaScript) — Smoke tests + screenshot comparison
Responsibilities:
- Page load tests
- Route switching tests
- Basic element rendering validation
- Screenshot comparison (visual regression)
- Performance baseline tests

### 2. Flutter integration_test (Dart) — Complex interaction tests
Responsibilities:
- Provider state management tests
- WebSocket connection simulation
- Form interaction tests
- Agent conversation flow tests

## Directory Structure
```
agentapp/
├── integration_test/          # Dart integration tests
│   └── app_test.dart
├── e2e/                       # Playwright tests
│   ├── playwright.config.js
│   ├── tests/
│   │   ├── smoke.spec.js      # Smoke tests
│   │   ├── visual.spec.js     # Visual regression tests
│   │   └── flows.spec.js      # User flow tests
│   └── screenshots/           # Baseline screenshots
├── scripts/
│   └── test-browser.sh        # One-click test script
└── pubspec.yaml               # integration_test dependency
```

## Acceptance Criteria
1. All Playwright tests pass
2. All integration_test tests pass
3. Screenshot diff < 1%
4. First paint < 3 seconds

## Commands
```bash
# One-click run all browser tests
./scripts/test-browser.sh

# Run Playwright only
cd agentapp && npx playwright test

# Run integration_test only (requires existing Flutter web app)
cd agentapp && flutter run -d chrome --web-port 8080
# Then in another terminal:
./scripts/test.sh flutter -d chrome
```
