# Fileloom

Fileloom is an HTML-first, filesystem-backed static CMS inspired by ShellCMS.

> **The filesystem is the database. HTML is the content format. `public/` is the published site.**

It is intentionally small:

- Go single-binary server
- no application database
- ordinary HTML source files with a tiny front matter header
- plain HTML/CSS themes
- generated pages, tag archives, RSS, sitemap, and media
- VvvebJs visual editor at `/_cms/editor`
- generated output served from `site/public`

## Run it

```bash
make test
make build
./fileloom
```

Open:

- CMS: `http://localhost:8000/_cms/`
- public site: `http://localhost:8000/`

On exe.dev, the default port is available at:

`https://jgbrwn-playground.exe.xyz/`

The app creates a starter site on first run. To use another workspace:

```bash
./fileloom -site ./my-site -web ./web -listen :8000
```

Set `FILELOOM_OWNER_EMAIL` (or pass `-owner-email`) to restrict CMS routes when the exe.dev proxy sends `X-ExeDev-Email`. Local requests without that header remain convenient for development.

## Workspace layout

```text
site/
├── content/
│   ├── pages/about.html
│   └── posts/2026/welcome-to-fileloom.html
├── media/
├── themes/default/
│   ├── layout.html
│   ├── index.html
│   ├── page.html
│   ├── post.html
│   ├── tag.html
│   └── assets/style.css
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
excerpt: A short card description.
---

<p>Ordinary HTML goes here.</p>
```

## Current slice

The first slice is deliberately vertical: create content, edit it visually with VvvebJs, save it back to HTML, build the static site, and preview the generated result. Theme visual editing, richer media management, Git publishing, and source-aware patch preservation are the next layers.
