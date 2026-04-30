---
id: R-004
status: Pending
date: 2026-04-24
priority: Medium
source: user
---

# R-004: Tap Session to Show Last Message Time

## Problem Statement

Users need to quickly see when a session last sent a message by tapping on it in the dashboard.

## In-scope

- Display last message send time on tap/click interaction with session card
- Time format: relative (e.g., "2 min ago") or absolute timestamp
- Reuse existing agent/session data if time field already available

## Out-of-scope

- Backend changes (if time field already in AgentModel)
- Long-press / context menu

## Tasks

| Task ID | Task Description | Owner | Status |
|---|---|---|---|
| T-014 | Implement tap-to-show last message time | dev-2 | Pending |
| T-015 | Validate time display feature | tester-1 | Blocked by T-014 |
| T-016 | Review time display implementation | reviewer-1 | Blocked by T-014 |
