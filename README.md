# Fileloom

Fileloom is an HTML-first, filesystem-backed static CMS inspired by ShellCMS.

> **The filesystem is the database. HTML is the content format. `public/` is the published site.**

Fileloom is designed for a small exe.dev VM deployment, but the Go server can run anywhere with a filesystem and a reverse proxy.

## What it does

- Creates and visually edits ordinary HTML pages and posts with VvvebJs.
- Preserves source front matter, comments, unknown metadata, and untouched HTML when visual content is saved.
- Keeps filesystem revision snapshots with restore support.
- Builds static pages, archives, tags, categories, RSS, sitemap, and media assets.
- Supports drafts, private content, scheduled publishing, and a best-effort scheduler.
- Runs link and accessibility checks during every build without checking external URLs.
- Offers a CSS custom-property/theme-token editor that patches `style.css` in place.
- Shows owner-only “Edit this page” controls on the public site.
- Exports the generated site as a bounded ZIP containing only `site/public`.
- Provides optional, separate Git integration for the website repository under `site/`.

## Quick start

```bash
cp .env.example .env
# Edit .env and set FILELOOM_OWNER_EMAIL for your exe.dev account.
set -a
. ./.env
set +a
make test
make build
./fileloom -listen :8000 -site site -web web
```

The public site is available at `http://localhost:8000/`. CMS routes require one canonical `X-ExeDev-Email` value matching the configured owner, so local API testing can use a request such as:

```bash
curl -H 'X-ExeDev-Email: you@example.com' http://localhost:8000/_cms/api/site
```

For a browser-facing deployment, put Fileloom behind the exe.dev proxy or another trusted identity-aware proxy. The supplied `fileloom.service` binds Fileloom to `127.0.0.1:8000`; keep the Go listener loopback-only when the proxy is the authorization boundary. The CLI's `:8000` default remains convenient for local development, not public exposure. Do not expose a second direct route to the Go process, and configure the proxy to strip all client-supplied `X-ExeDev-Email` values before injecting exactly one authenticated value.

## Deploy on an exe.dev VM

1. Clone the repository into the VM.
2. Copy `.env.example` to `.env` and set:

   ```dotenv
   FILELOOM_OWNER_EMAIL=you@example.com
   FILELOOM_BASE_URL=https://your-vm.exe.xyz
   ```

3. Build and test:

   ```bash
   make test
   make build
   ```

4. Edit `fileloom.service` so `WorkingDirectory`, `ExecStart`, and `EnvironmentFile` point at the clone.
5. Install and enable the service:

   ```bash
   sudo cp fileloom.service /etc/systemd/system/fileloom.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now fileloom
   ```

6. Authenticate through exe.dev at:

   ```text
   https://YOUR_VM.exe.xyz/__exe.dev/login?redirect=/_cms/
   ```

The service loads `.env` through systemd. The Go binary itself intentionally does not parse dotenv files. If `FILELOOM_OWNER_EMAIL` is missing, CMS access fails closed with a 404.

## Workspace layout

```text
site/
├── content/                 # canonical HTML source
│   ├── pages/about.html
│   └── posts/2026/welcome-to-fileloom.html
├── media/                   # uploaded media
├── themes/                  # plain HTML/CSS themes
├── .fileloom/               # local revisions and build metadata; ignored
└── public/                  # generated output; safe to delete and rebuild
```

A source document looks like:

```html
---
title: Welcome to Fileloom
slug: welcome-to-fileloom
date: 2026-09-11
status: published
tags: [fileloom, static-sites]
category: Notes
excerpt: A short card description.
---

<p>Ordinary HTML goes here.</p>
```

Scheduled content adds an RFC3339 `publish_at` value:

```yaml
status: scheduled
publish_at: 2026-09-15T14:00:00Z
```

## Editing and revisions

The VvvebJs editor sends the edited body HTML to Fileloom. Fileloom patches only the body range of the source file instead of serializing the entire document. A SHA-256 precondition prevents an older editor tab from overwriting newer source changes.

Before accepted content or theme-token changes, Fileloom stores a snapshot under `site/.fileloom/revisions/`. The dashboard's **History** action lists snapshots and restores them by creating another safety snapshot first. Revisions are bounded to 100 snapshots per path and 128 MiB across the workspace; the newest snapshots are retained. These filesystem revisions are independent of optional site Git commits; Git remains a separate user-controlled history mechanism.

Uploads retain the existing 16 MiB request limit and filename allowlist. SVG uploads are sanitized through an XML allowlist: scripts, event handlers, foreign content, external references, directives, and unsafe attributes are removed or rejected. Other media formats are copied unchanged. Uploads are written to a temporary file and renamed only after the copy and SVG sanitization succeed.


Each build stages output in a temporary directory, runs checks, and atomically swaps it into `site/public` only after generation succeeds. Builds reject symlinked content, themes, media, and generated assets; private metadata paths such as nested `.git`, `.fileloom`, and `.env` entries are not copied into generated output. Checks report:

- missing `html lang` attributes;
- empty/missing document titles;
- images without `alt` attributes;
- likely unlabeled form controls; and
- missing internal links or media targets.

External URLs are not fetched. Use **Export ZIP** in the dashboard to download a bounded archive of generated `site/public` files only; source, Git metadata, revisions, and secrets are excluded. The export mutation endpoint is POST-only and enforces file-count, compressed-size, uncompressed-size, and per-file limits.

## Git workflow

Site Git is deliberately independent from Fileloom’s own development repository. The dashboard only uses a repository initialized at `site/.git`; it never discovers a parent repository.

Git automation is opt-in in `site/site.json`:

```json
"git": {
  "enabled": true,
  "auto_commit": true,
  "auto_push": false,
  "commit_on": "build",
  "remote": "origin",
  "branch": "main"
}
```

Remote credentials are never stored by Fileloom. Use SSH keys or a Git credential helper. Keep `auto_push` off until the remote and credentials are verified. Automatic pushes accept only HTTPS/SSH effective push URLs; fetch URLs, configured `pushurl` values, and Git rewrite results are validated before unattended pushes. Git commands have bounded timeouts and non-interactive prompts.

## Optional CSP profiles

No CSP is enabled by default, preserving existing editor and theme behavior. To opt in, set `FILELOOM_CMS_CSP=default` for the CMS/editor profile and/or `FILELOOM_PUBLIC_CSP=default` for the generated-site profile. You can provide a complete policy value instead of `default`; public themes may require a customized policy for external assets or scripts. Direct SVG responses always receive a restrictive media policy.


## Code blocks and media

The visual editor includes a **Code block** helper that creates a semantic `<pre><code>` pair with selectable languages. Generated pages include a small dependency-free highlighter and theme-integrated CSS; themes can override `--fileloom-code-*` variables in their stylesheet.

The media panel supports multi-file selection and drag-and-drop uploads. Fileloom accepts AVIF, GIF, JPEG, JPG, PNG, SVG, and WebP images, plus the existing audio/video/PDF formats. SVG uploads are sanitized before storage. Public content uses `/media/...` URLs while uploads and media scanning remain owner-only CMS operations.

Themes are plain folders containing `theme.json`, HTML templates, and `assets/style.css`. The dashboard can activate themes, edit layout HTML visually, and edit CSS custom properties through the **Style tokens** editor. The built-in themes are intentionally inspectable and dependency-light.

Generated pages include attribution links for [Fileloom](https://github.com/jgbrwn/fileloom) and [VvvebJs](https://github.com/givanz/VvvebJs). VvvebJs is vendored under `web/vvvebjs`; see [NOTICE](NOTICE) and its bundled Apache 2.0 license.

Project development is documented in [CONTRIBUTING.md](CONTRIBUTING.md).


- [Mobile dashboard](docs/screenshots/dashboard-mobile-clean.png)
- [Theme-token editor](docs/screenshots/theme-tokens-mobile.png)


```bash
make test       # go test ./...
make build      # build ./fileloom
make fmt        # gofmt the Go sources, when available
```

The project intentionally keeps the server standard-library-first. The HTML build checker uses `golang.org/x/net/html` for safe read-only parsing.

## License

Fileloom is licensed under the MIT License. See [LICENSE](LICENSE). Third-party notices are in [NOTICE](NOTICE). Deployment security guidance is in [SECURITY.md](SECURITY.md), with the detailed review in [docs/security-review.md](docs/security-review.md).
