# Fileloom agent notes

Fileloom is intentionally filesystem-first. Keep the content format plain and inspectable:

- `site/content/` is source content.
- `site/themes/` is user-owned HTML/CSS.
- `site/public/` is generated output and should not become the canonical source.
- Keep the Go server dependency-light unless a feature clearly needs a dependency.
- Preserve the `/_cms` route shape and the small vertical workflow: create → visually edit → save HTML → build → serve static output.

The checked-in Deckflow editor bundle under `web/editor-dist` is the production browser runtime. Keep frontend source under `web/editor` and rebuild the checked-in bundle after changes; adapt the Go boundary rather than editing generated assets by hand.
