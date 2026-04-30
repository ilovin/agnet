---
id: R-001
status: Completed
date: 2026-04-24
priority: High
source: user
---

# R-001: Large-Screen Detail Canvas

## Problem Statement

Dashboard needs a large-screen split view where users can see multiple session details simultaneously without navigating away from the main list.

## In-scope

- Left panel: existing dashboard list unchanged
- Right panel: detail canvas with selectable sessions
- Canvas panels: user-adjustable sizing/layout controls
- Panel supports direct inline reply send
- Remove large-screen unread statistics chip (node-level aggregate)

## Out-of-scope

- Mobile/small-screen behavior changes
- New data models or RPC endpoints

## Constraints

- Reuse existing providers/RPC patterns
- Keep small-screen behavior untouched

## Tasks

| Task ID | Task Description | Owner | Status |
|---|---|---|---|
| T-001 | Implement large-screen detail canvas | dev-1 | Completed |
| T-002 | Validate canvas with tests | tester-1 | Completed |
| T-003 | Review canvas implementation | reviewer-1 | Completed |
