# Fileloom agent notes

Fileloom is intentionally filesystem-first. Keep the content format plain and inspectable:

- `site/content/` is source content.
- `site/themes/` is user-owned HTML/CSS.
- `site/public/` is generated output and should not become the canonical source.
- Keep the Go server dependency-light unless a feature clearly needs a dependency.
- Preserve the `/_cms` route shape and the small vertical workflow: create → visually edit → save HTML → build → serve static output.

The vendored VvvebJs files under `web/vvvebjs` are upstream runtime assets. Do not rewrite them unless integration requires it; adapt them at the Go boundary instead.
