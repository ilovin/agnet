# Requirements Management

## Active Requirements

### R-001: Large-Screen Detail Canvas
- Requirement ID: R-001
- Source/Stakeholder: user
- Date: 2026-04-24
- Priority: High

**Problem statement:**
Dashboard needs a large-screen split view where users can see multiple session details simultaneously without navigating away from the main list.

**In-scope:**
- Left panel: existing dashboard list unchanged
- Right panel: detail canvas with selectable sessions
- Canvas panels: user-adjustable sizing/layout controls
- Panel supports direct inline reply send
- Remove large-screen unread statistics chip (node-level aggregate)

**Out-of-scope:**
- Mobile/small-screen behavior changes
- New data models or RPC endpoints

**Constraints:**
- Reuse existing providers/RPC patterns
- Keep small-screen behavior untouched

**Tasks:**
| Task ID | Task Description | Owner | Status |
|---|---|---|---|
| T-001 | Implement large-screen detail canvas | dev-1 | **Completed** |
| T-002 | Validate canvas with tests | tester-1 | **Completed** |
| T-003 | Review canvas implementation | reviewer-1 | **Completed** |

---

### R-002: Session Logo Customization
- Requirement ID: R-002
- Source/Stakeholder: user
- Date: 2026-04-24
- Priority: Medium

**Problem statement:**
Each session should have a visual logo/avatar for easier identification. Default assignment should be unique per session.

**In-scope:**
- Unique default logo per session
- Manual logo editing capability
- Logo display in dashboard

**Out-of-scope:**
- Backend logo storage (if not already supported)

**Tasks:**
| Task ID | Task Description | Owner | Status |
|---|---|---|---|
| T-004 | Implement session logo customization | dev-1 | **Completed** |
| T-005 | Validate logo feature | tester-1 | **Completed** |
| T-006 | Review logo implementation | reviewer-1 | **Completed** |
| T-015 | Fix R-002 review blockers | reviewer-1 | **Completed** |

---

### R-003: Compact Dashboard Header
- Requirement ID: R-003
- Source/Stakeholder: user
- Date: 2026-04-24
- Priority: High

**Problem statement:**
Dashboard header takes too much vertical space. Need to maximize conversation display area.

**In-scope:**
- Move subtitle directly under logo
- Dashboard title typography distinct from session titles
- Fold/collapse non-critical status info even on large-screen web
- Maximize space for conversation content

**Out-of-scope:**
- Mobile layout changes (already compact)
- New navigation patterns

**Tasks:**
| Task ID | Task Description | Owner | Status |
|---|---|---|---|
| T-007 | Implement compact dashboard header | dev-1 | **Completed** |
| T-008 | Validate R-003 with tests and Chrome | tester-1 | **Completed** |
| T-009 | Review R-003 changes | reviewer-1 | **Completed** |
| T-012 | Re-review R-003 after fixes | reviewer-1 | **Completed** |
| T-016 | Fix R-003 review findings (M-01/M-03) | reviewer-1 | **Completed** |

---

### R-004: Tap Session to Show Last Message Time
- Requirement ID: R-004
- Source/Stakeholder: user
- Date: 2026-04-24
- Priority: Medium

**Problem statement:**
Users need to quickly see when a session last sent a message by tapping on it in the dashboard.

**In-scope:**
- Display last message send time on tap/click interaction with session card
- Time format: relative (e.g., "2 min ago") or absolute timestamp
- Reuse existing agent/session data if time field already available

**Out-of-scope:**
- Backend changes (if time field already in AgentModel)
- Long-press / context menu

**Tasks:**
| Task ID | Task Description | Owner | Status |
|---|---|---|---|
| T-014 | Implement tap-to-show last message time | dev-2 | **Pending** |
| T-015 | Validate time display feature | tester-1 | Blocked by T-014 |
| T-016 | Review time display implementation | reviewer-1 | Blocked by T-014 |

## Decision Log
| Date | Decision | Why | Impact |
|---|---|---|---|
| 2026-04-24 | Three requirements parallel tracked | All from same user session, shared dev resource | dev-1 has 3 concurrent in_progress tasks |
| 2026-04-24 | R-003 marked High priority | User explicitly requested after R-001/R-002 | May delay R-002 completion |
| 2026-04-24 | R-004 added to queue | User new request | Queue after C-01 fix |

## Requirement Status Summary
- Current phase: Ready to merge
- Overall status: In Progress (R-001/R-002/R-003 mergeable, R-004 pending)
- Manager notes: All three requirements (R-001/R-002/R-003) implemented, tested, reviewed, and validated. Ready for user decision on merge. R-004 pre-research completed, queued for implementation.
