# Comments engine plan

> **Status — September 14, 2026:** Artalk is the selected provider. Comments remain opt-in and dynamic; Fileloom does not store comment data. Local Artalk sidecar installation and same-origin proxying are documented and supported.

## Architecture decision

Fileloom integrates with Artalk as an external comment engine. It supports two deployment modes:

- **Local sidecar:** Artalk runs as a separately installed Go binary under `systemd`, bound to loopback. Fileloom proxies only the public Artalk API through the same-origin `/_fileloom/artalk` path. Comment storage and moderation still belong entirely to Artalk.
- **External server:** Fileloom publishes the configured browser-reachable Artalk URL and does not proxy it.

Fileloom owns only:

- site-level provider configuration in `site/site.json`;
- an immutable `id` in each page/post front matter;
- an optional `comments: false` per-document opt-out;
- generated widget markup and theme-aware assets; and
- the owner-only configuration UI and build boundary.

Artalk owns public writes, commenter identity, moderation, spam/rate controls, notifications, comment storage, and comment backups. Comment records never enter `site/content`, `site/public` as data, revisions, ZIP exports, or the website Git scope.

The local sidecar setup is intentionally operator-run. Fileloom's CMS can detect a loopback Artalk service, prefill local mode, and show the idempotent `scripts/install-artalk.sh` command, but it never runs `sudo`, downloads binaries, or creates systemd units from an HTTP request.

The browser bundle is vendored from Artalk `2.10.0` under `web/artalk/`, with its MIT license retained. The generated site uses the local pinned client bundle instead of a third-party CDN, so exported static output does not depend on a CDN. Keep the Artalk server on a compatible release and review the pinned client when upgrading.

Official provider links: [Artalk documentation](https://artalk.js.org), [Artalk releases](https://github.com/ArtalkJS/Artalk/releases), and [Artalk Docker image](https://hub.docker.com/r/artalk/artalk-go). The local one-VM workflow is documented in [docs/local-artalk.md](local-artalk.md).

## Owner controls

When the site setting is enabled, the CMS stores either an external server or a local sidecar connection:

```json
"comments": {
  "enabled": false,
  "provider": "artalk",
  "local": {
    "host": "127.0.0.1",
    "port": 23366,
    "service": "fileloom-artalk"
  },
  "site": "my-fileloom-site"
}
```

External mode instead stores `"server": "https://comments.example.com"`. In local mode, generated pages always use the same-origin `/_fileloom/artalk` proxy; the loopback address is never sent to browsers.

When the site setting is enabled:

- published pages and posts allow comments by default;
- an editor Details control can opt an individual page/post out by writing `comments: false`;
- the per-page control is disabled while site comments are off; and
- drafts, private items, scheduled items that are not due, archives, tags, categories, and synthetic index pages never receive a widget.

Disabling comments at site level removes the widget and its generated references on the next build. Existing per-document opt-outs are preserved.

## Stable document identity

Every page/post receives a random UUID front-matter key:

```yaml
id: 5f4d7f6d-3d75-4d56-9f86-4e7a4bf3a4c1
```

The Artalk page key is `fileloom/<id>`, never a path, slug, title, or URL. Paths and slugs can change; the UUID is intended to remain stable. Fileloom backfills missing IDs, rejects malformed IDs, and rejects duplicate IDs before/while comments are enabled. New documents receive an ID at creation.

## Rendering and themes

A comments-enabled page gets a small escaped data-attribute configuration block and a local bootstrap script. No inline executable configuration is generated. The bootstrap initializes Artalk with:

- the configured Artalk server and site key;
- the immutable Fileloom page key and page title;
- theme-driven light/dark colors rather than system-only dark mode;
- image uploads disabled by default; and
- remote emoticons and editor preview disabled by default.

`web/fileloom-comments.css` maps Artalk's `--at-color-*` variables to Fileloom theme variables. Each built-in theme declares comment colors, borders, radius, shadows, and contrast values alongside its code tokens. Custom themes get safe fallbacks and can override the same `--fileloom-theme-comments-*` variables.

The editor's unsaved Preview intentionally suppresses the live widget so previewing does not create external Artalk traffic or attach comments to unsaved content.

## Deployment and security requirements

1. For **local mode**, run [`scripts/install-artalk.sh`](../scripts/install-artalk.sh) as an operator with `sudo`. It installs the pinned Artalk Go binary, dedicated service user, SQLite data directory, loopback listener, and idempotent systemd unit. For external mode, deploy Artalk separately.
2. Set `FILELOOM_BASE_URL` to the real public Fileloom origin. Local Artalk uses that origin as its `site_url` and trusted domain; external mode must be configured with the exact same origin in Artalk's trusted-domain/CORS setting. Do not use `*`.
3. Create the Artalk administrator separately with the Artalk CLI. Fileloom does not store Artalk credentials. For a loopback-only sidecar, use an SSH tunnel for administration.
4. Configure Artalk's own moderation, CAPTCHA/spam controls, rate limits, email behavior, and backup policy. Fileloom does not duplicate those controls.
5. Fileloom validates external Artalk URLs as absolute HTTP(S) URLs without credentials, query, or fragment. Local mode validates the target as a loopback address and forwards only `/api/` requests to it.
6. Fileloom's default public CSP is augmented with an external Artalk origin only in external mode. Local mode remains same-origin and uses `connect-src 'self'`.
7. The first integration disables public image uploads and remote emoticons in the client; local installation also disables Artalk server-side image uploads by default. Enable richer integrations only after reviewing storage, CSP, privacy, and abuse settings.
8. Artalk may process commenter IP/User-Agent and email-related identity data. Sites using comments should publish an appropriate privacy notice and retain Artalk data according to their own policy.
9. The Fileloom ZIP export and Git integration intentionally do not include Artalk's database or backups. Back up and restore the Artalk service independently.

The Fileloom owner boundary still applies to all CMS settings: `/_cms` remains behind the trusted identity-aware proxy, mutation requests retain same-origin and CSRF protections, and comments configuration changes are audited like other CMS mutations. The public local proxy is not a CMS route and never accepts the owner identity as authorization.

## Future work

- Add an optional owner dashboard link/deep link to the Artalk admin panel without storing Artalk credentials.
- Test Artalk upgrades, custom CSP profiles, social/OIDC login, CAPTCHA, and email notifications.
- Consider a read-only comments health indicator only if it can remain network-free during builds.
- Keep provider-specific configuration narrow; do not make Fileloom responsible for Artalk's identity or moderation schema.

## Reusable ideas from `my-upc-v2`

The earlier discussion remains useful as a security checklist if Fileloom ever owns comments: explicit moderation states, soft deletion, revisions, signed CSRF tokens, per-identity/IP rate limits, audit events, independent backups, and lifecycle locking. Those ideas are intentionally not reimplemented while Artalk is the provider.

Fileloom should not introduce a second owner password, moderation keys in URLs, or a long-lived anonymous bearer identity. If the provider boundary changes later, public guest identity must be designed as a separate subsystem.
