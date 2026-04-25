# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Agent Manager (phone-talk) is a multi-AI-agent remote management system. It lets you monitor, chat with, and control AI agents (Claude Code, OpenCode, etc.) running on remote machines — from a phone or local machine.

## Architecture

Three independent components, each in its own subdirectory with its own module/project:

```
phone-talk/
├── agentd/     — Go daemon, runs on each remote machine, manages agent processes
├── agentgw/    — Go gateway, runs locally, aggregates remote agentd instances via SSH tunnels
└── agentapp/   — Flutter mobile app, connects to agentgw (or directly to agentd)
```

**Data flow:** `agentapp ──WS──► agentgw ──SSH tunnel + WS──► agentd ──PTY──► claude/opencode`

All three communicate via WebSocket JSON-RPC 2.0. Auth supports both `Authorization: Bearer <token>` header and `?token=` query parameter (for Flutter mobile compatibility).

## Design Documents

- `docs/superpowers/specs/2026-03-27-agent-manager-design.md` — system design spec (Chinese)
- `docs/superpowers/plans/2026-03-27-agentd-mvp.md` — agentd implementation plan with TDD steps
- `docs/superpowers/plans/2026-03-27-agentgw-mvp.md` — agentgw implementation plan
- `docs/superpowers/plans/2026-03-27-agentapp-mvp.md` — agentapp implementation plan

Plans contain exact code and shell commands. Follow them task-by-task when implementing.

## Instruction Priority & Conflict Resolution

- Priority order: direct user request > this `CLAUDE.md` > design plans/docs.
- If a design plan conflicts with current code/tests/runtime reality, follow the working code path and update implementation accordingly.
- If two instructions conflict at the same level, follow the latest explicit instruction.

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

### SHOULD

- Prefer running targeted tests first, then broaden scope only when the change surface is large.
- Keep changes minimal and directly scoped to the requested task.

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
  - `docs/superpowers/management/requirements.md` — requirement discussion outcomes, scope, acceptance criteria, task split, assignees.
  - `docs/superpowers/management/progress.md` — execution progress, task status, blockers, risks, ETA updates, completion summary.
- Update both documents whenever scope/assignment/progress changes materially.

### Acceptance Criteria (Definition of Done)

A task is accepted only when all items below are satisfied:

1. Requirement scope and expected output are matched (no hidden scope expansion).
2. Required tests for the change type are executed and passing (unit/integration/UI as applicable).
3. Reviewer sign-off is recorded with no unresolved critical issues.
4. For UI-related work, real Chrome interaction validation in the existing tab is completed and recorded.
5. Risks/blockers/follow-ups are documented in `progress.md`.
6. Requirement-to-delivery status is updated in both `requirements.md` and `progress.md`.

### SHOULD

- Prefer small, independently testable task slices per teammate.
- Keep at least one teammate slot free for urgent fixes/review support when possible.
- Prefer assigning reviewer and tester as different teammates on medium/large changes.

## Key Design Decisions

- **agentd** is a single static binary with zero runtime dependencies — easy to SCP to remote machines
- **EventBuffer** uses a circular buffer (head/count indices) for O(1) append, not array shifting
- **PTY Kill order**: kill process first, then close ptmx fd (avoids SIGHUP zombies)
- **Agent struct stores cmd/args** so `agent.restart` reuses original launch parameters
- **agentgw NodeManager.LoadAll()** loads persisted nodes in batch at startup (avoids N redundant file writes)
- **agentgw event forwarding**: agentd push events get `nodeId` injected before broadcast to App clients

## Session Discovery Pipeline

PID-to-session mapping uses a multi-stage pipeline (scanner + watcher share the same logic):

1. **Task fd discovery** — check which `~/.claude/tasks/<sessionID>` dirs the PID has open (via `/proc` or `lsof`). If exactly one, use it directly.
2. **Candidate list** — always list ALL `.jsonl` files from the project dir, then merge in any task fd sessions not already present. Never use task fd as the exclusive candidate set — the current session may have no task dir yet.
3. **Time-based filtering** — if tmux pane activity is available, filter candidates by time proximity. Otherwise sort by lastActivity descending.
4. **Content matching** — capture tmux pane text, extract fingerprints from JSONL files, pick the best match by substring hit count.
5. **Fallback** — most recently active candidate wins.

Key invariant: the PID mapping file (`sessions/<pid>.json`) is NOT authoritative — it goes stale after `/clear` or `--resume`. Never trust it as the sole source.

## Build / Deploy / Release / Install (Scripts only)

All build, deploy, release, and install operations must go through repo scripts. Do not use manual `go build` + `scp` or ad-hoc deployment/release/install commands.

```bash
scripts/build.sh              # build all components
scripts/deploy.sh local       # deploy locally (restart agentd + agentgw)
scripts/deploy.sh <node>      # deploy to remote node via SSH
scripts/release.sh            # package and release artifacts
scripts/install.sh            # full install (first-time setup)
```

## Conventions

- Commit messages: `feat(component):`, `fix(component):`, `chore:` prefix style
- Go module paths: `github.com/phone-talk/agentd`, `github.com/phone-talk/agentgw`
- Feature branches: `feature/<name>`, developed in git worktrees under `.worktrees/`
- Integration tests gated by `//go:build integration` build tag
- Design docs and plans live under `docs/superpowers/{specs,plans}/`
