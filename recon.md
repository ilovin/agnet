# Test Performance Recon

## Baseline (build cache warm, test cache cold)
- Total: **24.5s** via `bash scripts/test.sh unit`
- agentd:  ~16-20s (parallel: watcher 16.8s, ws 16-20s)
- agentgw: ~3.2s (longest: ws 3.2s)
- agentcli: ~0.3s

## Top slow packages
1. **agentd/internal/ws** (~16-20s) — websocket+httptest+sqlite roundtrips
2. **agentd/internal/watcher** (~14-16s) — file polling (TestClaudeWatcherStreamingTextIsWorking 4s, TestClaudeWatcherSkipExisting 3s, TestClaudeWatcherDetectsMessages 2s, TestClaudeWatcherDetectsWorking 2s)
3. **agentd/internal/scanner** (~3s)

## Optimizations (by predicted ROI)
1. **Run agentd/agentgw/agentcli in parallel from test.sh** — saves overhead of sequential subshells (~2-4s)
2. **Add t.Parallel() to safe (non-Setenv) tests** in `internal/watcher/claude_test.go` (4 slow tests are safe!) — slow ones run concurrently → wall ~4s instead of ~11s
3. **Add t.Parallel() to safe ws tests** — agent_service, handler_test, handler_opencode_tmux files all SAFE
4. **Reduce TestClaudeWatcherSkipExisting 3s sleep** — uses fixed 3-second sleep that could be polling-based

## Constraints
- t.Setenv tests in ws/server_test.go cannot use t.Parallel
- Goroutines that mutate global state: HOME env via Setenv
- Cannot delete or skip tests
