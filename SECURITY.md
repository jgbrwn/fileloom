# Security policy

## Supported versions

The `main` branch is the supported development version while Fileloom is private/pre-release.

## Reporting a vulnerability

Please do not publish credentials, tokens, or an exploitable proof of concept in a public issue. When this repository is public, use GitHub Security Advisories or contact the repository maintainers privately.

Include the affected route or feature, impact, reproduction steps, and any relevant logs with secrets removed.

## Deployment guidance

- Put Fileloom behind the exe.dev proxy or another trusted identity-aware reverse proxy.
- The supplied systemd unit binds the Go listener to `127.0.0.1:8000`; do not change it to a public wildcard listener unless an equivalent trusted boundary is enforced separately.
- Configure `FILELOOM_OWNER_EMAIL` and keep `.env` outside version control.
- The proxy must strip every client-supplied `X-ExeDev-Email` value and inject exactly one authenticated value. Multiple identity-header values are rejected by Fileloom.
- Disable intermediary caching for `/_cms/*`; Fileloom also emits `Cache-Control: no-store` for CMS responses.
- Do not expose a second public path directly to the Go listener that bypasses identity-header enforcement.
- Keep site Git credentials in SSH agents or Git credential helpers, never in `site/site.json`.
- Keep automatic push disabled until the remote and effective push URL are verified.
- Treat site HTML, theme templates, and uploaded media as owner-controlled content.

## Uploads and generated output

- SVG uploads are sanitized with an XML/element/attribute allowlist. Scripts, event handlers, foreign content, directives, external references, and unsafe CSS attributes are removed or rejected.
- Other allowed media formats are preserved as bytes; the upload body remains bounded at 16 MiB and writes use a temporary file followed by an atomic rename.
- Builds and exports reject symlinks and non-regular files. Generated output excludes nested `.git`, `.fileloom`, and `.env` metadata paths.

## Operational hardening

- CMS mutation requests have bounded bodies, admission control, HTTP server timeouts, and structured `slog` audit events containing action, route, actor, status, and result; request bodies and credentials are not logged. Build-dependent mutations roll back source/config/media changes when publication fails.
- Revision snapshots are retained up to 100 per path and 128 MiB total, retaining newest snapshots first.
- Git commands are non-interactive and time-bounded. Automatic pushes validate effective fetch/push URLs and reject local, plain-HTTP, and Git-protocol push targets.
- CSP is opt-in through `FILELOOM_CMS_CSP` and `FILELOOM_PUBLIC_CSP`; it is intentionally not enabled by default because existing VvvebJs/editor and user-theme behavior may depend on inline or external resources.
