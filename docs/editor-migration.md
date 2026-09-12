# Editor migration plan

Fileloom keeps the Go server, filesystem source model, static build, themes, Exe.dev owner boundary, media API, revisions, and Git workflow. The editor is a replaceable browser client.

## Current transition

The frontend source lives under `web/editor`; run `make editor-build` after changing it. The generated static bundle is checked into `web/editor-dist` so the Go service remains deployable without a Node runtime.

- `site.json` selects `editor_engine: "deckflow"` for the active site.
- `vvveb` remains available with `?engine=vvveb` as a compatibility fallback.
- `GET /_cms/api/editor?path=...` returns the body fragment, document metadata, full-source SHA, theme CSS, preview URL, and available engines.
- `POST /_cms/api/editor-save` accepts the existing form contract and JSON `{path, html, base_sha256}`. JSON clients must send a precondition and receive a refreshed `source_sha256`/`ETag`.
- The initial Deckflow client is static output under `web/editor-dist`; Node is a development/build dependency, not a production service dependency.

## Editor boundary

The browser editor must treat the HTML body fragment as the editable representation and the full source SHA as its concurrency token. It must not write front matter, generated output, or theme templates as content body HTML.

The editor shell owns mobile and desktop UX:

- mobile: tap-to-add blocks, bottom sheets, keyboard-safe layout, large controls;
- desktop: side panels, keyboard shortcuts, optional drag/resize interactions;
- both: explicit save, preview, media, status, conflict handling, and source-preserving reload.

Deckflow supplies source-aware selection, text editing, structural edits, and undo/redo. Fileloom supplies block insertion, media, themes, metadata, publishing, revisions, and persistence.

## Deliberate non-goals

Puck is not the canonical model for existing HTML pages. If a structured block-page format is added later, it should be opt-in for new content and have an explicit React/Node rendering strategy. Plumix is a UX and host-editor reference, not a dependency. Theme templates remain a separate workflow from content-body editing.

## Completion gate

Before removing Vvveb, test both desktop and Android-sized viewports for existing-page load, inline text editing, headings, links, code blocks, image selection/upload, save/reload, stale-source conflict, revisions, public output, keyboard behavior, and theme-token compatibility.
