# Sandbox Development (R-012)

When you work in a `.claude/worktrees/agent-XXX/` worktree, you cannot touch
the main `~/.agentd/`, `~/.agentgw/`, `~/.claude/`, port 7373, or port 7374
without poisoning the running main runtime (and possibly other parallel
worktrees).

The `sandbox` subcommand of `scripts/deploy.sh` solves this with **HOME
redirection + random ports + per-sandbox config + per-sandbox dist**, fully in
bash, with **zero Go code changes**.

## Quick start

```bash
# 1. From the worktree root, spawn an isolated runtime:
scripts/deploy.sh sandbox my-feature

# Output:
#   ✓ Sandbox my-feature started
#     agentd PID:  40373  port: 19738
#     agentgw PID: 40659 port: 19484
#     Web:         http://localhost:19484/  (HTTP 200)
#     Token:       6d0df0fc...
#     Sandbox dir: <worktree>/.sandbox/my-feature
#     Stop:        scripts/deploy.sh sandbox-stop my-feature

# 2. (Optional) Spawn a Claude CLI bound to the sandbox HOME:
scripts/sandbox-claude.sh my-feature

# 3. List all running sandboxes:
scripts/deploy.sh sandbox-list

# 4. Stop and clean up:
scripts/deploy.sh sandbox-stop my-feature
```

## What gets isolated

```
<worktree>/.sandbox/<id>/
├── home/                       # Sandboxed HOME (passed to agentd, agentgw, claude)
│   ├── .agentd/
│   │   ├── config.json         # port=$AGENTD_PORT, data_dir=<sandbox>/home/.agentd/data
│   │   └── data/agents.db      # Independent SQLite — main agents.db never touched
│   ├── .agentgw/
│   │   ├── config.json         # port=$AGENTGW_PORT, token=<random>, tunnel disabled
│   │   └── static -> ~/.agentgw/static  (or full copy if --with-web)
│   └── .claude/                # Created lazily by `claude` CLI when invoked via wrapper
│       └── projects/...        # Sessions land here, NOT in real ~/.claude/projects
├── dist/
│   ├── agentd                  # Compiled fresh from this worktree's source
│   └── agentgw
├── logs/
│   ├── agentd.log
│   └── agentgw.log
├── pid/
│   ├── agentd.pid
│   └── agentgw.pid
└── sandbox.env                 # AGENTD_PORT / AGENTGW_PORT / SANDBOX_TOKEN / REAL_HOME
```

Ports are picked at random from `17000-19999`, far from the main runtime's
`7373/7374`. The agentgw tunnel is **disabled** in the sandbox — it only
serves local HTTP. If you need tunnel testing, use the main deployment.

## Common workflows

### Run integration tests against a fresh runtime

```bash
scripts/deploy.sh sandbox itest
source .sandbox/itest/sandbox.env
# Now $AGENTD_PORT, $AGENTGW_PORT, $SANDBOX_TOKEN are exported
go test ./... -run TestSomething -tags integration
scripts/deploy.sh sandbox-stop itest
```

### Test a UI change without rebuilding the main static dir

```bash
# Default: links the sandbox static dir to ~/.agentgw/static (read-only)
scripts/deploy.sh sandbox ui-experiment

# To rebuild Flutter Web into the sandbox (does NOT touch ~/.agentgw/static):
scripts/deploy.sh sandbox ui-experiment --with-web
```

Open `http://localhost:<AGENTGW_PORT>/` — you'll see the sandbox-built Web.

### Drive an isolated Claude CLI session

```bash
scripts/deploy.sh sandbox cli-test
scripts/sandbox-claude.sh cli-test --print "hello"
# Session jsonl lands in .sandbox/cli-test/home/.claude/projects/, not ~/.claude/projects/
scripts/deploy.sh sandbox-stop cli-test
```

The wrapper is just `exec env HOME=<sandbox>/home claude "$@"`. Anything Claude
writes — `.claude/projects/`, `.claude/sessions/`, `.claude.json`, agent
configs — lands inside the sandbox HOME and disappears on `sandbox-stop`.

## Safety guarantees

1. **`scripts/deploy.sh local|web|npm|tunnelhub|all` refuses to run from a
   worktree subdirectory** (R-012 T5 fail-fast). Sandbox is exempt because it
   never touches the main runtime.
2. **Main runtime untouched**: agentd PID at 7373 + agentgw PID at 7374 are not
   restarted, killed, or reconfigured by any sandbox subcommand. Verified by
   the R-012 acceptance suite.
3. **No Go code changes**: only `scripts/` and config files. Reverting R-012
   leaves the runtime unaffected.
4. **Sandboxes are gitignored**: `.sandbox/` is in `.gitignore`; sandbox
   artifacts (binaries, logs, sqlite) never enter version control.
5. **`sandbox-stop` is destructive**: it kills both processes and `rm -rf`s
   the sandbox dir. No confirmation prompt — use a stable id you won't reuse.

## Failure modes and recovery

- **"agentgw responded with HTTP 000/404"** — usually means agentgw failed to
  start. Check `<sandbox>/logs/agentgw.log`. Common cause: another process
  already owns the random-picked port (rare; retry).
- **"sandbox already running"** — `sandbox-stop` first, then `sandbox` again.
- **PID file points to dead process** — `sandbox-stop` is idempotent; it skips
  dead PIDs and still removes the dir.
- **`sandbox-list` shows `dead`** — process crashed; check logs and rerun.

## Implementation notes

- Sandbox compiles agentd+agentgw to `<sandbox>/dist/`, **not** the main
  `out/`. This keeps each sandbox bound to its worktree's source revision.
- The agentgw `static` dir is symlinked to the user's real `~/.agentgw/static`
  by default (read-only). Use `--with-web` to rebuild Flutter Web into the
  sandbox without polluting the main static dir.
- Token, ports, paths are all written to `<sandbox>/sandbox.env` in
  `KEY=value` format — `source` it from any shell to get the env exported.

## See also

- `docs/requirements/r-012-sandboxed-worktree-isolation.md` — full requirement
- `docs/management/manager-workflow.md` — when Manager should ask dev agents to
  use sandbox mode
- `docs/operations/development-workflow.md` — overall TDD + Chrome workflow
