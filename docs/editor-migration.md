# Editor migration plan

Fileloom keeps the Go server, filesystem source model, static build, themes, Exe.dev owner boundary, media API, revisions, and Git workflow. The editor is a replaceable browser client.

> **Status — September 13, 2026:** The Deckflow foundation is implemented and enabled for this checkout, but migration is incomplete and experimental. Server-generated site configurations still default to VvvebJs for backward compatibility. Desktop, tablet, and Android-sized Playwright smoke tests now pass; the full migration gate is still open. Keep VvvebJs until the completion gate passes.

| Migration slice | Status |
| --- | --- |
| Editor-neutral GET/save/SHA contract | Done |
| Checked-in Deckflow static bundle | Done |
| Desktop/mobile shell and basic HTML editing | Done, experimental |
| Selection-aware insertion, duplicate/delete/move controls, property inspector, local insertion undo/redo, media drag/drop | Initial browser/backend coverage done; broader HTML cases remain |
| Code blocks with language selector and public syntax highlighting | Initial Deckflow slice done; source remains plain HTML |
| Metadata/status/revision controls inside new editor | Initial save/conflict/restore coverage now includes API and browser paths |
| Theme layout/token editing | Separate existing CMS workflow |
| Browser/device regression suite | 36 Playwright tests across desktop, tablet, and Android-sized Chromium; full gate pending |
| Vvveb removal | Blocked by completion gate |
| Comments engine | Design-only; see `docs/comments-plan.md` |

## Current transition

The frontend source lives under `web/editor`; run `make editor-build` after changing it. The generated static bundle is checked into `web/editor-dist` so the Go service remains deployable without a Node runtime.

- `site.json` selects `editor_engine: "deckflow"` for the active site.
- `vvveb` remains available with `?engine=vvveb` as a compatibility fallback.
- `GET /_cms/api/editor?path=...` returns the body fragment, document metadata, full-source SHA, theme CSS, preview URL, and available engines.
- `POST /_cms/api/editor-save` accepts the existing form contract and JSON `{path, html, base_sha256, metadata}`. JSON clients must send a precondition and receive a refreshed `source_sha256`/`ETag`. Metadata updates preserve unknown front matter and validate status/scheduling before rebuilding affected public output.
- `POST /_cms/api/editor-preview` renders the current unsaved body and supported metadata through the active page/post template and layout in memory. It does not write source or `site/public`; the Deckflow Preview action opens this actual theme-applied render.

## Editor boundary

The browser editor must treat the HTML body fragment as the editable representation and the full source SHA as its concurrency token. It must not write front matter, generated output, or theme templates as content body HTML.

The editor shell owns mobile and desktop UX:

- mobile: tap-to-add blocks, bottom sheets, keyboard-safe layout, large controls;
- desktop: side panels, keyboard shortcuts, metadata/status/history controls, optional drag/resize interactions;
- both: explicit save, preview, media, status, conflict handling, and source-preserving reload;
- code blocks: language-aware semantic insertion/editing with public/preview syntax highlighting.

Deckflow supplies source-aware selection, text editing, structural edits, and undo/redo. Fileloom supplies block insertion, media, themes, metadata, publishing, revisions, and persistence.

## Current limitations

- The new shell now has a 36-test Playwright regression matrix (`web/editor/e2e`) for desktop, tablet, and Android-sized Chromium viewports. By default Playwright copies the checked-in site into a temporary workspace and starts a disposable Go server, so save/upload tests do not mutate the developer's site; `FILELOOM_E2E_URL` opts into an existing server. It still does not cover every content/media/build path.
- Save conflicts now show escaped local/remote body summaries, metadata conflict fields, explicit remote/local overwrite choices, and a safe merge path when the changed sides do not overlap. This is not a character-level diff/merge editor.
- Fileloom block insertion and initial duplicate/delete/sibling movement controls are selection-aware when the selected source element can be resolved, with a body-end fallback; link/image properties now patch only the selected opening tag and reject unsafe URLs/classes, but this is not yet a block data model.
- The media sheet handles images, file selection, drag/drop, selected-image replacement, and contextual placement. Generic JSON uploads, nested media paths, public serving, and generated asset coverage are now tested; batch/deferred builds and richer placement are next.
- Deckflow does not edit front matter directly; Fileloom's Details sheet handles supported content metadata/status, while theme layouts and CSS tokens remain in the separate CMS workflow.
- Preview now renders the current body through the active theme's page/post template and layout in memory. Save/reload, conflict, revision restore, media, generated public output, and preview non-mutation paths have initial coverage; exact preview/public parity across every theme remains part of the migration gate.
- `?engine=vvveb` is a compatibility override, not a persisted per-user engine choice.

Puck is not the canonical model for existing HTML pages. If a structured block-page format is added later, it should be opt-in for new content and have an explicit React/Node rendering strategy. Plumix is a UX and host-editor reference, not a dependency. Theme templates remain a separate workflow from content-body editing.

**Gate status: not met.** The current suite covers 36 browser cases across desktop, tablet, and Android-sized Chromium plus API coverage for save/reload/public output, stale conflicts, revisions, real media uploads, theme/public preview parity, and preview non-mutation; the default Deckflow route also asserts that Vvveb assets are not requested, and Go coverage exercises a Deckflow-only web bundle. Before removing VvvebJs, add keyboard navigation/undo-redo coverage and richer inline text-editing cases beyond the current persisted link-property flow, then complete the final attribution/NOTICE review.
