# Editor migration plan

Fileloom keeps the Go server, filesystem source model, static build, themes, Exe.dev owner boundary, media API, revisions, and Git workflow. The editor is a replaceable browser client.

> **Status — September 13, 2026:** The Deckflow foundation is implemented and enabled for this checkout, but migration is incomplete and experimental. Server-generated site configurations still default to VvvebJs for backward compatibility. Manual desktop/Android smoke checks pass; no automated browser/device matrix sign-off exists yet. Keep VvvebJs until the completion gate passes.

| Migration slice | Status |
| --- | --- |
| Editor-neutral GET/save/SHA contract | Done |
| Checked-in Deckflow static bundle | Done |
| Desktop/mobile shell and basic HTML editing | Done, experimental |
| Selection-aware insertion, local insertion undo/redo, media drag/drop | Initial slice done; needs broader testing |
| Metadata/status/revision controls inside new editor | Next |
| Theme layout/token editing | Separate existing CMS workflow |
| Browser/device regression suite | Not started |
| Vvveb removal | Blocked by completion gate |
| Comments engine | Design-only; see `docs/comments-plan.md` |

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

## Current limitations

- The new shell does not yet provide automated browser/device coverage.
- A save conflict preserves local edits and now offers keep/reload actions, but does not yet provide an in-editor diff/merge workflow.
- Fileloom block insertion is now selection-aware when the selected source element can be resolved, with a body-end fallback; it is not yet a block data model.
- The media sheet handles images, file selection, and drag/drop; batch/deferred builds and richer placement are next.
- Deckflow does not edit front matter, publishing metadata, theme layouts, or CSS tokens.
- Preview is a synthetic body-plus-active-theme-stylesheet canvas, not the complete generated public template.
- `?engine=vvveb` is a compatibility override, not a persisted per-user engine choice.

Puck is not the canonical model for existing HTML pages. If a structured block-page format is added later, it should be opt-in for new content and have an explicit React/Node rendering strategy. Plumix is a UX and host-editor reference, not a dependency. Theme templates remain a separate workflow from content-body editing.

**Gate status: not met.** Before removing VvvebJs, complete browser coverage on desktop, tablet, and Android-sized viewports for existing-page load, inline text/headings/links/code, contextual block insertion and movement, image upload/selection, save/reload, stale conflicts, undo/redo, revisions, public build output, keyboard behavior, and theme-token compatibility. Then decide whether to close the remaining media/theme gaps and update VvvebJs attribution/NOTICE.
