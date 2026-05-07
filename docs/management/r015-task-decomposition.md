# R-015 Task Decomposition (T-024 ~ T-029)

**Requirement**: R-015 Domain Normalization & Download Portal
**PRD**: `docs/plans/domain-download-portal-prd.md`
**Worktree**: `.worktrees/r015-domain-download-portal` (branch: `feature/r015-domain-download-portal`)
**Status**: Pending Manager Review

---

## Vertical Slice 1: Domain Variable Injection (T-024)

**Task ID**: T-024
**Description**: Replace all hardcoded `ilovin.xyz` with compile-time injectable domain variables + runtime override
**Priority**: P0 (blocks all other slices)
**Estimated Effort**: 2-3 days
**Assignee**: TBD

### Acceptance Criteria
- [ ] Define `DefaultHubDomain`, `DefaultAPIDomain`, `DefaultDownloadDomain` variables in `agentgw/cmd/agentgw/main.go` (injectable via `-ldflags -X`)
- [ ] `scripts/build.sh` supports `DOMAIN` env var: `./scripts/build.sh DOMAIN=ilovim.xyz`
- [ ] `scripts/build.sh` injects domain via `-ldflags` for both Go and Flutter builds
- [ ] All `ilovin.xyz` references in `scripts/install.sh` use variables
- [ ] All `ilovin.xyz` references in `agentgw/cmd/agentgw/main.go` use `DefaultHubDomain`
- [ ] Test files updated to use variable or mock domain
- [ ] Documentation updated (`docs/operations/development-workflow.md`)

### Affected Files
- `scripts/build.sh` — add DOMAIN env var handling + ldflags injection
- `scripts/install.sh` — replace hardcoded URL with variable
- `agentgw/cmd/agentgw/main.go` — add DefaultHubDomain variable, update help text
- `agentgw/cmd/agentgw/main_test.go` — update test cases
- `agentapp/test/widget_test.dart` — update test URLs
- `docs/operations/development-workflow.md` — document new build flag

### Dependencies
- None (first slice)

### Test Requirements
- Unit tests: verify ldflags injection produces correct default values
- Integration test: build with custom DOMAIN, verify binary has correct default
- Test existing functionality: ensure no regressions in tunnel connection

### Risk Notes
- **HIGH**: Must maintain backward compatibility during migration period
- Old `ilovin.xyz` DNS must remain active with 301 redirect

---

## Vertical Slice 2: Release Manifest System (T-025)

**Task ID**: T-025
**Description**: Standardize release manifest with SHA256 checksums and platform metadata
**Priority**: P0
**Estimated Effort**: 2 days
**Assignee**: TBD
**Dependency**: T-024 (domain variables needed for manifest URLs)

### Acceptance Criteria
- [ ] `scripts/release.sh` generates `manifest.json` with:
  - Version, build timestamp
  - Platform/Architecture matrix (darwin-arm64, linux-amd64, linux-arm64, android, ios)
  - Per-artifact: download URL, SHA256 checksum, file size
- [ ] `scripts/release.sh --publish` uploads artifacts to OSS bucket
- [ ] `scripts/release.sh --publish` refreshes CDN cache
- [ ] Manifest schema versioned (v1)
- [ ] Manifest validated against JSON schema before upload

### Affected Files
- `scripts/release.sh` — add manifest generation + --publish flag
- New: `scripts/lib/manifest.sh` — manifest generation helpers
- New: `scripts/lib/oss.sh` — OSS upload helpers (optional abstraction)

### Test Requirements
- Unit test: manifest.json generation with mock artifacts
- Verify SHA256 checksums are correct
- Test --publish with dry-run mode (no actual upload)

### Risk Notes
- OSS credentials must be configured (env vars: ALIYUN_ACCESS_KEY_ID, etc.)
- CDN flush API rate limits

---

## Vertical Slice 3: One-Line Install Script (T-026)

**Task ID**: T-026
**Description**: Enhanced `install.sh` with platform auto-detection, idempotent config, and update capability
**Priority**: P1
**Estimated Effort**: 2-3 days
**Assignee**: TBD
**Dependency**: T-024 (domain variables), T-025 (manifest for update checks)

### Acceptance Criteria
- [ ] Platform detection: `uname -s` + `uname -m` → target triple (darwin-arm64, linux-amd64, etc.)
- [ ] Downloads correct artifact from manifest
- [ ] Idempotent config: first run generates token, subsequent runs preserve user config
- [ ] `install.sh --update` checks manifest, downloads only changed artifacts
- [ ] Checksum verification after download
- [ ] Install completion message with next steps (`agentgw start --qr`)
- [ ] Error handling: network failure, unsupported platform, checksum mismatch

### Affected Files
- `scripts/install.sh` — major rewrite
- `scripts/lib/platform.sh` — platform detection library (new)
- `scripts/lib/download.sh` — download + verify library (new)

### Test Requirements
- Containerized tests: simulate different `uname` outputs
- Test idempotency: run twice, verify config preserved
- Test update flow: simulate newer version in manifest
- Test failure paths: network timeout, bad checksum

### Risk Notes
- `curl | sh` security concerns — must document manual install alternative
- Platform detection edge cases (WSL, musl libc, etc.)

---

## Vertical Slice 4: Download Portal (T-027)

**Task ID**: T-027
**Description**: Static interactive download site at `download.ilovim.xyz`
**Priority**: P1
**Estimated Effort**: 2-3 days
**Assignee**: TBD
**Dependency**: T-024 (domain), T-025 (manifest for links)

### Acceptance Criteria
- [ ] Static site with scenario cards: Mac / Linux / Android
- [ ] Mac card: shows `curl | sh` command, copy-to-clipboard button
- [ ] Linux card: shows platform-specific install command
- [ ] Android card: QR code for APK download + direct download link
- [ ] Connection guide: remote URL template, QR code viewing instructions
- [ ] iOS entry hidden or marked "developer only"
- [ ] Responsive design (mobile-friendly)
- [ ] Deployed to CDN (Cloudflare Pages or equivalent)

### Affected Files
- New: `portal/` — static site source (HTML/CSS/JS or lightweight framework)
- New: `portal/index.html` — main landing page
- New: `portal/assets/` — images, icons
- `scripts/build.sh` — add portal build step
- `scripts/release.sh` — add portal deployment

### Test Requirements
- Chrome validation: verify all cards render correctly
- Mobile viewport test
- QR code scan test (Android)

### Risk Notes
- CDN setup required
- APK must be signed with release certificate (not debug)

---

## Vertical Slice 5: Telemetry Client (T-028)

**Task ID**: T-028
**Description**: Client-side telemetry module for diagnostic reporting
**Priority**: P2
**Estimated Effort**: 2-3 days
**Assignee**: TBD
**Dependency**: T-024 (api domain for endpoint)

### Acceptance Criteria
- [ ] New package: `agentgw/internal/telemetry/`
- [ ] Auto-trigger: agentgw startup heartbeat
- [ ] Auto-trigger: agentd/agentgw crash/exception (last 50 log lines)
- [ ] Manual trigger: `agentgw debug --submit` command
- [ ] Payload includes: installId (UUID), version, platform, uptime, component status, network mode
- [ ] Log sanitization: strip tokens, IPs, file paths
- [ ] OpenSSO accessToken attached for user association
- [ ] Config flag to disable telemetry (opt-out)
- [ ] Batching + retry with exponential backoff

### Affected Files
- New: `agentgw/internal/telemetry/client.go` — main telemetry client
- New: `agentgw/internal/telemetry/payload.go` — payload structures
- New: `agentgw/internal/telemetry/sanitize.go` — log sanitization
- New: `agentgw/internal/telemetry/client_test.go` — tests
- `agentgw/cmd/agentgw/main.go` — integrate startup/crash reporting
- `agentd/cmd/agentd/main.go` — integrate crash reporting

### Test Requirements
- Unit tests: payload construction, log sanitization rules
- Unit tests: retry logic, backoff calculation
- Integration test: verify telemetry reaches mock API endpoint

### Risk Notes
- Privacy compliance — must not collect PII or config secrets
- Network overhead — batching essential

---

## Vertical Slice 6: API Service (T-029)

**Task ID**: T-029
**Description**: Lightweight API service (`api.ilovim.xyz`) for install script, release manifest, and telemetry
**Priority**: P2
**Estimated Effort**: 3-4 days
**Assignee**: TBD
**Dependency**: T-024 (domain), T-025 (manifest), T-028 (telemetry endpoint)

### Acceptance Criteria
- [ ] New Go service: `api/` directory with its own `go.mod`
- [ ] `GET /v1/health` — health check
- [ ] `GET /v1/release/latest` — returns latest manifest.json
- [ ] `GET /v1/install.sh` — returns platform-specific install script (User-Agent detection)
- [ ] `POST /v1/telemetry` — receives telemetry payloads
- [ ] Rate limiting: 60 req/min/IP for all endpoints
- [ ] Access logging: X-Real-IP, User-Agent, timestamp
- [ ] CORS configured for download portal origin
- [ ] Docker build + K8s deployment manifests

### Affected Files
- New: `api/cmd/api/main.go` — API server entry
- New: `api/internal/handlers/release.go` — release endpoints
- New: `api/internal/handlers/install.go` — install script endpoint
- New: `api/internal/handlers/telemetry.go` — telemetry ingestion
- New: `api/internal/middleware/ratelimit.go` — rate limiting
- New: `api/internal/middleware/logger.go` — access logging
- New: `api/Dockerfile` — container build
- New: `api/k8s/` — Kubernetes manifests

### Test Requirements
- Unit tests: each handler independently
- Integration tests: full request/response cycle
- Load test: rate limiting under burst traffic

### Risk Notes
- K8s cluster access required for deployment
- TLS termination at ingress level

---

## Cross-Cutting Concerns

### Testing Strategy
- Each slice includes TDD unit tests
- Integration tests for slice boundaries
- Final E2E: Chrome validation of download portal

### Deployment Order
1. T-024 (domain variables) — enables all subsequent work
2. T-025 (manifest) — enables install script and portal
3. T-026 + T-027 + T-028 can proceed in parallel after T-024/T-025
4. T-029 (API) can proceed in parallel with T-026/T-027/T-028

### Documentation Updates
- `docs/operations/development-workflow.md` — build flags
- `docs/operations/deployment.md` — API service deployment
- `docs/designs/` — new API service architecture doc (if needed)

### Migration Notes
- `ilovin.xyz` DNS kept active with 301 redirect for 3 months
- Old releases remain accessible
- No breaking changes to existing `agentgw` configs
