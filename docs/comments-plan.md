# Comments engine plan

> **Status — September 13, 2026:** Artalk is the selected provider and Fileloom now has the first integration slice. Comments remain opt-in and dynamic; Fileloom does not store comment data.

## Architecture decision

Fileloom integrates with a separately hosted Artalk server rather than becoming a second comment backend. Fileloom owns only:

- site-level provider configuration in `site/site.json`;
- an immutable `id` in each page/post front matter;
- an optional `comments: false` per-document opt-out;
- generated widget markup and theme-aware assets; and
- the owner-only configuration UI and build boundary.

Artalk owns public writes, commenter identity, moderation, spam/rate controls, notifications, comment storage, and comment backups. Comment records never enter `site/content`, `site/public` as data, revisions, ZIP exports, or the website Git scope.

The browser bundle is vendored from Artalk `2.10.0` under `web/artalk/`, with its MIT license retained. The generated site uses the local pinned client bundle instead of a third-party CDN, so exported static output does not depend on a CDN. Keep the Artalk server on a compatible release and review the pinned client when upgrading.

Official provider links: [Artalk documentation](https://artalk.js.org), [Artalk releases](https://github.com/ArtalkJS/Artalk/releases), and [Artalk Docker image](https://hub.docker.com/r/artalk/artalk-go).

## Owner controls

Comments are **off by default**. The CMS dashboard's **Community → Comments** panel stores:

```json
"comments": {
  "enabled": false,
  "provider": "artalk",
  "server": "https://comments.example.com",
  "site": "my-fileloom-site"
}
```

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

1. Deploy Artalk separately, preferably behind HTTPS and an identity-aware/reverse-proxy boundary appropriate to the deployment.
2. Configure Artalk's trusted domain/CORS setting with the exact Fileloom public origin. Do not use `*` for a public comments service.
3. Configure Artalk's own moderation, CAPTCHA/spam controls, rate limits, email behavior, and backup policy. Fileloom does not duplicate those controls.
4. Fileloom validates the configured Artalk URL as an absolute HTTP(S) URL without credentials, query, or fragment. It never server-side fetches or proxies that URL.
5. Fileloom's default public CSP is augmented with the configured Artalk origin in `connect-src` when comments are enabled. Custom CSP policies still need compatibility testing, especially if Artalk social login, avatars, email links, or other remote integrations are enabled.
6. The first integration disables public image uploads and remote emoticons in the client. Enable those only after reviewing Artalk storage, CSP, privacy, and abuse settings.
7. Artalk may process commenter IP/User-Agent and email-related identity data. Sites using comments should publish an appropriate privacy notice and retain Artalk data according to their own policy.
8. The Fileloom ZIP export and Git integration intentionally do not include Artalk's database or backups. Back up and restore the Artalk service independently.

The Fileloom owner boundary still applies to all CMS settings: `/_cms` remains behind the trusted identity-aware proxy, mutation requests retain same-origin and CSRF protections, and comments configuration changes are audited like other CMS mutations.

## Future work

- Add an optional owner dashboard link/deep link to the Artalk admin panel without storing Artalk credentials.
- Document a supported Artalk sidecar/reverse-proxy deployment for exe.dev.
- Test Artalk upgrades, custom CSP profiles, social/OIDC login, CAPTCHA, and email notifications.
- Consider a read-only comments health indicator only if it can remain network-free during builds.
- Keep provider-specific configuration narrow; do not make Fileloom responsible for Artalk's identity or moderation schema.

## Reusable ideas from `my-upc-v2`

The earlier discussion remains useful as a security checklist if Fileloom ever owns comments: explicit moderation states, soft deletion, revisions, signed CSRF tokens, per-identity/IP rate limits, audit events, independent backups, and lifecycle locking. Those ideas are intentionally not reimplemented while Artalk is the provider.

Fileloom should not introduce a second owner password, moderation keys in URLs, or a long-lived anonymous bearer identity. If the provider boundary changes later, public guest identity must be designed as a separate subsystem.
