# Editor migration plan

Fileloom keeps the Go server, filesystem source model, static build, themes, Exe.dev owner boundary, media API, revisions, and Git workflow. The editor is a replaceable browser client.

> **Status — September 13, 2026:** The Deckflow migration is complete for content and theme-layout editing. New sites default to Deckflow, the checked-in browser suite covers desktop/tablet/Android-sized Chromium plus manual responsive smoke checks, and the legacy editor assets/routes have been removed.

| Migration slice | Status |
| --- | --- |
| Editor-neutral GET/save/SHA contract | Done |
| Checked-in Deckflow static bundle | Done |
| Desktop/mobile shell and basic HTML editing | Done |
| Selection-aware insertion, duplicate/delete/move controls, property inspector, local insertion undo/redo, media drag/drop | Initial browser/backend coverage done; broader HTML cases remain |
| Code blocks with language selector and public syntax highlighting | Responsive editor, reliable source-preserving updates, theme-driven styling, and public highlighting |
| Metadata/status/revision controls inside new editor | Initial save/conflict/restore coverage now includes API and browser paths |
| Theme layout/token editing | Done with protected-slot Deckflow theme mode and separate Style tokens workflow |
| Browser/device regression suite | 78 Playwright tests across desktop, tablet, and Android-sized Chromium; full gate passed |
| Legacy editor removal | Done |
| Comments engine | Opt-in Artalk integration with site-level enablement, per-page/post opt-out, stable document IDs, local theme-aware client assets, and external-service security boundary; see `docs/comments-plan.md` |

## Current transition

The frontend source lives under `web/editor`; run `make editor-build` after changing it. The generated static bundle is checked into `web/editor-dist` so the Go service remains deployable without a Node runtime.

- `site.json` selects `editor_engine: "deckflow"` for the active site.
- `GET /_cms/api/editor?path=...` returns the content body fragment, document metadata, full-source SHA, theme CSS, preview URL, and available engines; `GET /_cms/api/editor?theme=...` returns a protected-slot theme-layout resource.
- `POST /_cms/api/editor-save` accepts content and protected-slot theme-layout JSON resources with source preconditions and refreshed `source_sha256`/`ETag` responses. Metadata updates preserve unknown front matter and validate status/scheduling before rebuilding affected public output.
- `POST /_cms/api/editor-preview` renders the current unsaved content body or protected-slot theme layout through the active theme in memory. It does not write source or `site/public`; the Deckflow Preview action opens this actual theme-applied render.
- The content editor's **HTML source** action opens the body HTML directly without exposing or rewriting front matter or metadata. Apply HTML returns to the same Deckflow canvas; Save still uses the source-preserving SHA-guarded boundary.

## Editor boundary

The browser editor must treat the HTML body fragment as the editable representation and the full source SHA as its concurrency token. It must not write front matter, generated output, or theme templates as content body HTML.

The editor shell owns mobile and desktop UX:

- mobile: tap-to-add blocks, bottom sheets, keyboard-safe layout, large controls;
- desktop: side panels, keyboard shortcuts, metadata/status/history controls, optional drag/resize interactions;
- both: explicit save, preview, media, status, conflict handling, and source-preserving reload;
- code blocks: language-aware semantic insertion/editing with a responsive code sheet, plain-source persistence, theme-driven public/preview styling, and syntax highlighting.

Deckflow supplies source-aware selection, text editing, structural edits, and undo/redo. Fileloom supplies block insertion, media, themes, metadata, publishing, revisions, and persistence.

## Current limitations

- The new shell now has a 78-test Playwright regression matrix (`web/editor/e2e`) for desktop, tablet, and Android-sized Chromium viewports. By default Playwright copies the checked-in site into a temporary workspace and starts a disposable Go server, so save/upload tests do not mutate the developer's site; `FILELOOM_E2E_URL` opts into an existing server. Manual responsive smoke checks cover desktop, Pixel-sized Android, and iPhone-sized layouts.
- Save conflicts now show escaped local/remote body summaries, metadata conflict fields, explicit remote/local overwrite choices, and a safe merge path when the changed sides do not overlap. This is not a character-level diff/merge editor.
- Fileloom block insertion and initial duplicate/delete/sibling movement controls are selection-aware when the selected source element can be resolved, with a body-end fallback; link/image properties now patch only the selected opening tag and reject unsafe URLs/classes, but this is not yet a block data model.
- Inline text editing preserves mixed child markup, supports range formatting, rejects structure-changing contenteditable edits, and now has desktop/tablet/Android-sized keyboard regression coverage. Native touch/soft-keyboard behavior still deserves device testing beyond Chromium emulation.
- Theme-layout visual editing now uses the separate Deckflow theme-layout mode with protected template slots; CSS custom-property editing remains in the CMS Style tokens workflow.
- Keyboard coverage exercises inline edit commit/cancel, mixed-markup structure rejection, keyboard save, ordered host undo/redo across inline and structural edits, duplicate/delete/clear-selection shortcuts, and desktop/mobile undo controls across all three browser profiles.
- The media sheet handles images, file selection, drag/drop, selected-image replacement, and contextual placement. Generic JSON uploads, nested media paths, public serving, and generated asset coverage are now tested; batch/deferred builds and richer placement are next.
- Deckflow does not edit front matter directly; Fileloom's Details sheet handles supported content metadata/status, while theme layouts and CSS tokens remain in the separate CMS workflow.
- Preview renders the current body or protected-slot theme layout through the active theme in memory. Save/reload, conflict, revision restore, media, generated public output, theme preview, and preview non-mutation paths are covered by the regression suite.
- No alternate editor engine query is supported; Deckflow is the only editor engine.

Puck is not the canonical model for existing HTML pages. If a structured block-page format is added later, it should be opt-in for new content and have an explicit React/Node rendering strategy. Plumix is a UX and host-editor reference, not a dependency. Theme templates remain a separate workflow from content-body editing.

**Gate status: passed.** The suite covers 78 browser cases across desktop, tablet, and Android-sized Chromium plus API coverage for content/theme save/reload/public output, stale conflicts, revisions, real media uploads, theme/public preview parity, preview non-mutation, keyboard editing, ordered undo/redo, HTML source editing, reliable code-block editing, theme-driven code styling, opt-in Artalk rendering/disablement, and single-surface canvas scrolling. Manual responsive smoke checks cover desktop, Pixel-sized Android, and iPhone-sized layouts. Go tests, race tests, the production build, source-fidelity checks, and attribution review all pass.
