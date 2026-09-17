# Fileloom security review

Review date: September 17, 2026

## Current protections

- `/_cms` fails closed unless the request carries exactly one configured `X-ExeDev-Email` value matching the owner; duplicate or comma-joined identity values are rejected.
- CMS mutation requests reject cross-site `Sec-Fetch-Site`/`Origin` requests. Same-origin checks accept the configured site origin and the request's normalized origin; missing `Origin` remains supported for local/API clients.
- CMS responses emit `Cache-Control: no-store`, `Vary: X-ExeDev-Email`, and optional CMS CSP headers. Public responses retain owner-toolbar cache separation.
- The supplied systemd unit binds Fileloom to `127.0.0.1:8000`; the CLI `:8000` default remains for local development.
- HTTP server read-header/read/write/idle timeouts, bounded CMS mutation admission, and request-body limits prevent unbounded request accumulation.
- Content, theme, media, revision, Git, and generated-asset paths reject symlinked components and non-regular files. Configured theme names are validated before use.
- Builds use a staging directory and a publication lock, so readers do not observe an incomplete directory swap. Mutations that require a build roll back source/config/media changes when the build fails. Scheduled publishing restores scheduled sources when the resulting build fails.
- SVG uploads are XML-sanitized with an allowlist, temporary-file staging, and a restrictive SVG/media response policy. Other allowed media formats remain byte-preserving. Local MP4/WebM video playback stays same-origin; YouTube insertion accepts only validated HTTPS IDs and renders a fixed `youtube-nocookie.com` click-to-load target without preactivation requests.
- ZIP export is POST-only and bounded by file count, compressed size, uncompressed size, and per-file size; source, revisions, Git metadata, secrets, symlinks, and private metadata paths are excluded.
- Revisions are filesystem snapshots with atomic writes, checksums, per-path retention of 100 snapshots, and a 128 MiB workspace budget.
- CMS mutations emit structured `slog` audit events with actor, action, route, status, and result without request bodies or credentials.
- Git is constrained to a real `site/.git` repository, uses non-interactive time-bounded commands, rejects unsafe configured URLs, and validates effective push URLs including configured `pushurl`/rewrite results before automatic pushes.
- Comments are an explicit opt-in external boundary. Fileloom validates external Artalk URLs, keeps local Artalk loopback-only, proxies only local `/api/` requests through a fixed same-origin path, publishes no comment data into source/public/export/Git, disables Artalk image uploads and remote emoticons in its bootstrap, and adds an external Artalk origin only to public `connect-src` when external mode is enabled.
- Site-level comments configuration changes remain owner-authenticated CMS mutations with same-origin checks, bounded bodies, rollback-on-build-failure, and audit logging. Per-document `comments: false` is source-preserved and cannot enable comments while the site switch is off.
- Generated comment markup uses escaped data attributes and a local pinned Artalk client; no inline executable configuration or third-party CDN is required.

## Comments deployment notes

Artalk can run as a local sidecar or a separate public service. For local mode, run `scripts/install-artalk.sh` as an operator, keep the service bound to `127.0.0.1`, keep its data under `/var/lib/fileloom-artalk/`, and use Fileloom's fixed same-origin API proxy. Before enabling it:

- configure the exact Fileloom public origin in Artalk's trusted-domain/CORS settings (the local installer does this through `ATK_TRUSTED_DOMAINS`); never use a wildcard for a public deployment;
- configure Artalk authentication, moderation, CAPTCHA/spam controls, rate limits, notification/email behavior, and backups in Artalk;
- update the site's privacy notice for provider-processed commenter identity, email, IP, User-Agent, and notification data;
- keep the Fileloom public CSP profile compatible with the Artalk API origin and any explicitly enabled Artalk integrations; and
- back up Artalk independently because Fileloom exports and Git intentionally exclude comment records.

The first integration uses local vendored Artalk `2.10.0` client assets. The local installer pins the matching Artalk server release, disables client and server image uploads by default, and leaves Artalk administration outside Fileloom. Review those choices before enabling richer Artalk plugins or social/OIDC login.

### 1. Verify the proxy boundary before public release

The identity header is an authorization boundary only when requests can reach Fileloom through the trusted exe.dev proxy. The repository and service now make the conservative deployment choice by binding the supplied unit to loopback, but the actual public proxy behavior still needs verification. Confirm that:

- the proxy strips all client-supplied `X-ExeDev-Email` values and injects exactly one canonical value;
- the proxy does not append a second value;
- `/_cms/*` is not cached by an intermediary; and
- no alternate public port or path reaches the Go process directly.

Run forged-header and direct-listener checks after deployment from an external client. Local handler tests cannot prove the proxy boundary.

### 2. Complete upload validation if uploads become internet-facing

SVG sanitization is implemented. Non-SVG media still uses the existing extension allowlist and size limit without full content-signature or image-dimension validation. Add format sniffing and dimension/pixel budgets only if public uploads need that stronger boundary; keep the current owner-only CMS access otherwise.

### 3. Keep Git automation conservative

Automatic push remains opt-in. Effective fetch and push URLs are validated, but site Git configuration and hooks remain owner-controlled code. Keep credentials outside Fileloom configuration, keep prompts disabled, and review hooks before enabling unattended commits or pushes.

### 4. Operational follow-up

Build and Git operations are bounded where they invoke external Git commands and HTTP requests have server timeouts. Automatic Git commit/push is queued after successful builds or configured changes, serialized independently from mutation/build/publication locks, and coalesces changes that arrive during a run; a separate workspace coordination lock prevents it from committing transient mutation state. Manual Git actions remain synchronous. Pending and last-error state is exposed in Git status. Filesystem builds now accept cancellable contexts, observe cancellation during admission, filesystem I/O, rendering, and checks, and preserve the previous public output until the atomic publication swap begins.

### 5. CSP compatibility testing

CSP profiles are opt-in. `make editor-test-csp` runs the CMS/editor profile with Deckflow, theme-layout editing, media upload, editor frames, mobile controls, and all six built-in public themes while capturing browser CSP violations. `make editor-test-all` runs both the normal and CSP matrices. Fileloom adds only `https://www.youtube-nocookie.com` to public `frame-src` when a valid YouTube marker is present; no YouTube source is added to CMS CSP and no restrictive global CSP is enabled by default.

### 6. Deployment test matrix

Before making the repository/site public, test:

- unauthenticated, wrong-account, correct-account, duplicate-header, and forged-header proxy requests;
- direct listener access and alternate exe.dev ports;
- missing, same-origin, malformed, and cross-origin mutation requests;
- malicious paths, symlinked roots/components, FIFOs/special files, SVGs, oversized uploads, and nested private metadata;
- theme names, CSS token values, Git remotes, `pushurl`, hooks, and timeout behavior;
- export archive contents and size limits; and
- scheduled publishing after restart, including build failure and retry behavior.

The conservative release rule remains: keep CMS access behind the trusted proxy, keep automatic push off until verified, and treat site HTML/theme code as trusted owner code until a sandboxed rendering model exists.
