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

## Key Design Decisions

- **agentd** is a single static binary with zero runtime dependencies — easy to SCP to remote machines
- **EventBuffer** uses a circular buffer (head/count indices) for O(1) append, not array shifting
- **PTY Kill order**: kill process first, then close ptmx fd (avoids SIGHUP zombies)
- **Agent struct stores cmd/args** so `agent.restart` reuses original launch parameters
- **agentgw NodeManager.LoadAll()** loads persisted nodes in batch at startup (avoids N redundant file writes)
- **agentgw event forwarding**: agentd push events get `nodeId` injected before broadcast to App clients

## Conventions

- Commit messages: `feat(component):`, `fix(component):`, `chore:` prefix style
- Go module paths: `github.com/phone-talk/agentd`, `github.com/phone-talk/agentgw`
- Feature branches: `feature/<name>`, developed in git worktrees under `.worktrees/`
- Integration tests gated by `//go:build integration` build tag
- Design docs and plans live under `docs/superpowers/{specs,plans}/`
