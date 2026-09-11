# Fileloom

Fileloom is an HTML-first, filesystem-backed static CMS inspired by ShellCMS.

> **The filesystem is the database. HTML is the content format. `public/` is the published site.**

## Run it

```bash
make test
make build
./fileloom
```

Open:

- CMS: `http://localhost:8000/_cms/` (requires the configured `X-ExeDev-Email`)
- public site: `http://localhost:8000/`
- exe.dev: `https://YOUR_VM.exe.xyz/`

The app creates a starter workspace on first run. Use another workspace with:

```bash
./fileloom -site ./my-site -web ./web -listen :8000
```

## Configuration

The canonical URL used by RSS, sitemap, and feed links is resolved in this order:

1. `-base-url`
2. `FILELOOM_BASE_URL`
3. `site.json` → `base_url`
4. `http://localhost:8000`

There is no `.env` loader in the Go binary. For the systemd deployment, copy `.env.example` to `.env`; `fileloom.service` loads that file with `EnvironmentFile`. Set `FILELOOM_OWNER_EMAIL` (or pass `-owner-email`) to the exe.dev account email allowed to access `/_cms`. Fileloom requires that value and an exact `X-ExeDev-Email` match; missing or mismatched requests receive a normal 404. Keep `.env` private and untracked.

To authenticate through exe.dev before opening the CMS, visit `https://YOUR_VM.exe.xyz/__exe.dev/login?redirect=/_cms/`. The exe.dev proxy then supplies `X-ExeDev-Email` to Fileloom. Direct requests to the Go process must not be exposed as an alternate public path, because the header is trusted only when traffic arrives through the exe.dev proxy.

## Workspace layout

```text
site/
├── content/
│   ├── pages/about.html
│   └── posts/2026/welcome-to-fileloom.html
├── media/
├── themes/
│   ├── default/
│   ├── midnight/
│   ├── terminal/
│   ├── editorial/
│   ├── bento/
│   └── brutalist/
└── public/              # generated; safe to delete and rebuild
```

A source document looks like:

```html
---
title: Welcome to Fileloom
slug: welcome-to-fileloom
date: 2026-09-09
status: published
tags: [fileloom, static-sites]
category: Notes
excerpt: A short card description.
---

<p>Ordinary HTML goes here.</p>
```

## Git workflow

Git integration is deliberately opt-in in `site/site.json`:

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

The dashboard exposes repository status and manual commit/push controls. Keep `auto_push` off until the remote and credentials are configured.

## Theme workflow

Themes are plain folders containing `theme.json`, HTML templates, and `assets/style.css`. The dashboard can activate themes and open a visual layout editor. The built-in themes are intentionally dependency-free; external HTML themes can be adapted by moving their layout into `layout.html`, replacing the page body with `{{content}}`, and preserving the Fileloom tokens.

Good upstream sources to investigate include MIT-licensed Start Bootstrap templates and HTML5 UP themes. Check each theme's license and attribution requirements before bundling it.

## Run as a service

```bash
sudo cp fileloom.service /etc/systemd/system/fileloom.service
sudo systemctl daemon-reload
sudo systemctl enable --now fileloom
```

## Current slice

The current vertical workflow is: create content → edit HTML visually → save source → build static pages, tag/category archives, yearly archives, RSS, sitemap, and media. Mobile editor controls, theme switching/editing, media uploads, draft publishing, and optional Git automation are included. Source-aware patch preservation, revision history, scheduling, and richer theme importing remain good next layers.
