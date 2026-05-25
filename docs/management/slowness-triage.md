# Phone-Talk ContentMatch 性能诊断报告

## 结论

**哪个 RPC 慢**：`agent.scan` 和 `session.catalog` RPC 端点
- `agent.scan`: handler.go:1281 调用 `h.server.manager.ScanExisting()`
- `session.catalog`: handler.go:448 调用 `h.agentScan(req)`，进而触发扫描

**单次耗时**：15-30秒 (从日志时间戳"20:34:31"到"20:34:46"观察，间隔15秒)

**瓶颈**：staged contentMatch 在 81 个候选中运行 3 个阶段 (5+12+20=37 个候选)，每个候选执行：
1. `capture-pane`: 获取 tmux 窗格内容 (~100ms 每次)
2. JSONL 文件读取和解析 (64KB 尾部扫描)
3. 正则表达式匹配 (nonWordRe 使用 \p{L}\p{N})

---

## 证据

### 日志片段

```
2026/05/25 20:34:31 [ContentMatch] candidate_total=81 staged_max=[5 12 20]
2026/05/25 20:34:31 [ContentMatch] stage_max=5 scored_candidates=5
2026/05/25 20:34:31 [ContentMatch] candidate=... fpCount=20 toolFpCount=3 score=7 forced=false
[... 5 个候选评分 ...]
2026/05/25 20:34:31 [ContentMatch] stage_max=12 scored_candidates=7
[... 7 个候选评分 ...]
2026/05/25 20:34:31 [ContentMatch] stage_max=20 scored_candidates=8
[... 8 个候选评分 ...]
2026/05/25 20:34:31 [ContentMatch] reject: ambiguous bestScore=8 secondBest=7 minMargin=2
```

### 代码引用

**入口点**：
- `agentd/internal/ws/handler.go:1281` - `agentScan()` RPC 处理
- `agentd/internal/ws/handler.go:448` - `sessionCatalog()` 内部调用 agentScan

**扫描入口**：
- `agentd/internal/agent/manager.go:1825` - `AutoAttachExisting()` 定期调用 ScanExisting()
- `agentd/internal/scanner/scanner.go:393` - contentMatchSession 调用处

**ContentMatch 实现**：
- `agentd/internal/scanner/content_match.go:216-332` - contentMatchSession 函数
- `agentd/internal/scanner/content_match.go:298` - extractFingerprints 调用
- `agentd/internal/scanner/content_match.go:16` - 正则表达式定义 nonWordRe

### 性能特征

从日志统计：
- 132 次 stage_max=20 阶段触发 (表示前两个阶段未达到阈值)
- 3,245 个候选分数计算行
- **平均**：81 个候选 × 37% 评分率 = ~30 个评分/次调用
- **时间间隔**：15 秒重复周期 (从 20:34:31 → 20:34:46 → 20:35:01...)

每次运行成本估算：
- 37 × 100ms (capture-pane) = 3.7 秒
- 37 × 正则解析 = ~2-5 秒
- **总计**：5-10 秒/次调用

---

## 缓解方案

### A. 缓存 ContentMatch 结果 [高优先级]

**描述**：为每个 PID 缓存 contentMatch 结果 N 秒，避免重复计算

**位置**：`agentd/internal/scanner/content_match.go` 新增缓存层
```go
type matchCache struct {
  pid       int
  timestamp time.Time
  result    *SessionCandidate
}
// 在 contentMatchSession 前检查，如果 time.Since(cache.timestamp) < 10*time.Second，返回缓存
```

**预估收益**：
- 若 dashboard 调用频率 > 1/10s，收益 80-90%
- 实现简单，无副作用

**风险**：低。缓存 TTL 可配置，session 无效时主动清除

---

### B. 提前 Break 未满足阈值的阶段 [中优先级]

**描述**：当 `bestScore < contentMatchMinScore` 时，在 stage_max=5 后立即 break，不再执行后续阶段

**位置**：`agentd/internal/scanner/content_match.go:310-312`

当前逻辑：
```go
if bestScore >= contentMatchMinScore && (bestScore-secondBestScore) >= contentMatchMinMargin {
  break
}
```

优化后：
```go
if bestScore >= contentMatchMinScore {
  if (bestScore-secondBestScore) >= contentMatchMinMargin {
    break  // 已找到明确匹配
  }
} else if stageMax == 5 {
  break  // 第一阶段无任何匹配，后续不太可能产生好结果
}
```

**预估收益**：
- 当第一阶段无匹配时（约占 40% 情况），节省 12+20 阶段成本 = 60%+ 加速
- 平均收益：25-35%

**风险**：中。可能误判低分情况，需在后续阶段仍有机会验证

---

### C. 无歧义快速路径：单候选跳过 ContentMatch [低优先级]

**描述**：若 `len(candidates) <= 1` 直接返回，无需运行 contentMatch

**位置**：`agentd/internal/scanner/content_match.go:230-239`

```go
if len(candidates) <= 1 {
  if len(candidates) == 1 {
    return &candidates[0]
  }
  return nil
}
```

**预估收益**：
- 适用于新用户或单会话场景，节省整个 contentMatch 运行 = 5-10s
- 典型收益：5-15%（因 81 个候选较常见）

**风险**：极低。正逻辑（无歧义则无需匹配）

---

### D. 异步 ContentMatch + 立即返回 [高风险, 低优先级]

**描述**：将 contentMatch 移到后台 goroutine，`agent.scan` RPC 立即返回候选列表，ContentMatch 结果后续发布

**位置**：`agentd/internal/scanner/scanner.go:393`

**预估收益**：
- 对 UI 感知延迟：100%（RPC 立即返回）
- 实际问题未解决，只是推迟到后台

**风险**：高。
- 引入竞态条件（session 在匹配期间失效）
- 多个 dashboard 调用可能产生冲突的后台任务
- 难以调试和追踪

**不推荐**。

---

### E. 禁用 CJK 正则、使用快速路径 [中优先级]

**描述**：在 cleanTUIText 中，对仅包含 ASCII 的候选使用快速路径，跳过 \p{L}\p{N} Unicode 正则

**位置**：`agentd/internal/scanner/content_match.go:34-41`

当前：
```go
clean = nonWordRe.ReplaceAllString(clean, " ")  // 每次都用 Unicode 正则
```

优化后：
```go
if containsOnlyASCII(raw) {
  clean = asciiNonWordRe.ReplaceAllString(clean, " ")  // 简单 ASCII 正则
} else {
  clean = nonWordRe.ReplaceAllString(clean, " ")       // Unicode 正则
}
```

**预估收益**：
- ASCII-only 场景：15-25% 加速（减少正则编译 + 执行开销）
- 平均收益（混合场景）：5-10%

**风险**：低。仅优化路径选择

---

## 推荐实施路径

### 第一阶段（立即）
1. **方案 A**（缓存）：实施 10s TTL 缓存，几乎无风险，收益显著
2. **方案 C**（单候选快路径）：一行代码，收益小但稳定

### 第二阶段（如果第一阶段收益不足）
3. **方案 B**（提前 break）：需测试，但提高 contentMatch 逻辑的鲁棒性
4. **方案 E**（ASCII 快速路径）：增量优化，投入小

### 不推荐
- **方案 D**（异步）：复杂度高，风险大，问题本质未解决

---

## 其他建议

1. **监控指标**：在 dashboard 加载时记录 RPC 延迟，定期告警 > 5s
2. **用户影响评估**：收集 agent.scan 调用频率数据，判断缓存 TTL 是否合理
3. **后续优化**：考虑将候选预排序（时间戳优先），减少 contentMatch 候选数
