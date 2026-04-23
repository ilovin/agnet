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

- Follow **test-driven development (TDD)** for all non-trivial changes.
- After **every development step or code change**, run the relevant tests before continuing.
- Minimum expectation per change:
  - backend changes: targeted Go unit tests
  - app changes: targeted Flutter tests
  - cross-component/session changes: relevant integration tests
- For session/chat pipeline work, use `agentgw/test_plan.md` as the executable test plan.
- Final acceptance is gated by **real Chrome interaction in the existing tab**; do not treat a change as done until Chrome validation passes.
- Any future debug scripts, screenshots, temporary captures, and generated debug artifacts must go under a fixed ignored directory: `agentapp/scripts/debug/` (or an equivalent component-local `scripts/debug/` directory). Do not leave debug PNGs, ad-hoc scripts, or runtime artifacts in the project root.

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

## Build & Deploy

Always use the repo scripts for building and deploying — never run manual `go build` + `scp` sequences:

```bash
scripts/build.sh              # build all components
scripts/deploy.sh local       # deploy locally (restart agentd + agentgw)
scripts/deploy.sh <node>      # deploy to remote node via SSH
scripts/install.sh            # full install (first-time setup)
```

## Conventions

- Commit messages: `feat(component):`, `fix(component):`, `chore:` prefix style
- Go module paths: `github.com/phone-talk/agentd`, `github.com/phone-talk/agentgw`
- Feature branches: `feature/<name>`, developed in git worktrees under `.worktrees/`
- Integration tests gated by `//go:build integration` build tag
- Design docs and plans live under `docs/superpowers/{specs,plans}/`
