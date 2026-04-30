---
id: R-005
status: in-progress
date: 2026-04-28
priority: high
source: bug-report
---

# R-005: Session 列表字母序排序不稳定导致上下跳动

## 问题描述

Dashboard 中 session/agent 列表按字母序排序展示，但当多个 session 拥有相同排序键（如相同的 name 或空名）时，列表位置在每次状态更新后上下跳动，严重影响使用体验。

## 根因分析

1. `visibleManagedAgentsForNode` 和 `buildVisibleDashboardSessions` 均使用 `managedAgentSortTitle` 作为主排序键。
2. Dart 的 `List.sort` 是**不稳定排序**。当两个元素的排序键相同时，相对顺序取决于输入列表的原始顺序。
3. WebSocket `agent.status_changed` 事件会重建 agent 列表，导致输入顺序变化，进而使相同键的 session "乱跳"。

## 修复方案

在所有使用 `managedAgentSortTitle` 排序的地方，添加稳定的 secondary key（`agent.id`）作为 tie-breaker，确保排序结果唯一且稳定。

### 需修改的位置

- `agentapp/lib/screens/dashboard_screen.dart:2229` — `visibleManagedAgentsForNode`
- `agentapp/lib/screens/dashboard_screen.dart:504` — `buildVisibleDashboardSessions`

## 验收标准

1. 单元测试覆盖：构造多个 name 相同但 id 不同的 agent，验证排序后顺序稳定且按 id 二次排序。
2. `flutter test` 通过。
3. 在 Chrome 中验证 dashboard session 列表在状态更新后不再跳动。
