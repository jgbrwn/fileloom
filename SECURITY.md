# Security policy

## Supported versions

The `main` branch is the supported development version while Fileloom is private/pre-release.

## Reporting a vulnerability

Please do not publish credentials, tokens, or an exploitable proof of concept in a public issue. When this repository is public, use GitHub Security Advisories or contact the repository maintainers privately.

Include the affected route or feature, impact, reproduction steps, and any relevant logs with secrets removed.

## Deployment guidance

- Put Fileloom behind the exe.dev proxy or another trusted identity-aware reverse proxy.
- Configure `FILELOOM_OWNER_EMAIL` and keep `.env` outside version control.
- Do not expose a second public path directly to the Go listener that bypasses identity-header enforcement.
- Keep site Git credentials in SSH agents or Git credential helpers, never in `site/site.json`.
- Treat site HTML, theme templates, and uploaded media as owner-controlled content.
