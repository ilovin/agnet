# CLAUDE.md

This file is the **project constitution**. Rules here are mandatory and may not be overridden by design docs or plans.

For architecture, build commands, and detailed workflow procedures, see `docs/README.md` and `docs/operations/development-workflow.md`.

## Instruction Priority

Priority order: **direct user request > this `CLAUDE.md` > design plans/docs**.

- If a design plan conflicts with current code/tests/runtime reality, follow the working code path and update implementation accordingly.
- If two instructions conflict at the same level, follow the latest explicit instruction.

## Development Workflow (Mandatory)

- Follow **test-driven development (TDD)** for all non-trivial changes.
- Run **relevant tests after every code change** before continuing.
- Final acceptance is gated by **real Chrome interaction** in the existing tab; do not treat a change as done until Chrome validation passes.
- Debug artifacts must go under `*/scripts/debug/`. Do not leave them in the project root.
- Backend changes: targeted Go unit tests. App changes: targeted Flutter tests. Cross-component/session changes: relevant integration tests.

## Delivery Workflow (Mandatory)

- Use a single **Manager** role to run delivery end-to-end.
- Manager **must not implement code directly**; delegate to teammates/sub-agents.
- See `docs/management/manager-workflow.md` for the full Manager delegation pipeline (explore → review → decompose → develop → validate).
- Requirement summaries go in `docs/requirements/r-XXX-*.md`; progress in `docs/management/progress.md`.

### Manager Self-Check (before every response when in Manager mode)

- [ ] Am I about to directly edit source code? → **STOP. Delegate to a developer agent.**
- [ ] Am I about to directly run tests? → **STOP. Delegate to a tester agent.**
- [ ] Am I about to fix a bug or implement a feature myself? → **STOP. Create a Task and assign it.**
- [ ] Have I used TaskCreate to track the task before acting? → If not, create it first.
- [ ] Is the task scope clearly defined with acceptance criteria? → If not, clarify before delegating.

**Rule of thumb**: If the action involves touching `lib/`, `test/`, `cmd/`, or any source file — you are doing a developer's job. Stop and delegate.

- A task is accepted only when all of the following are satisfied:
  1. Scope matches requirement (no hidden expansion).
  2. Required tests are executed and passing.
  3. Reviewer sign-off with no unresolved critical issues.
  4. UI work: real Chrome validation completed and recorded.
  5. Risks/blockers/follow-ups documented in `progress.md`.
  6. Status updated in both the requirement file and `progress.md`.

## Build / Deploy / Release / Install (Scripts Only)

All operations must go through repo scripts. No manual `go build` + `scp` or ad-hoc commands.

```bash
scripts/build.sh              # build all components
scripts/deploy.sh local       # deploy locally (restart agentd + agentgw)
scripts/deploy.sh <node>      # deploy to remote node via SSH
scripts/release.sh            # package and release artifacts
scripts/install.sh            # full install (first-time setup)
```

## Conventions

- Commit messages: `feat(component):`, `fix(component):`, `chore:` prefix style.
- Go module paths: `github.com/phone-talk/agentd`, `github.com/phone-talk/agentgw`.
- Integration tests gated by `//go:build integration` build tag.
- Design docs: `docs/designs/`, plans: `docs/plans/`, requirements: `docs/requirements/`. See `docs/README.md` for navigation.
