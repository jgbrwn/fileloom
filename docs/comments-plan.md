# Comments engine plan

> **Status — September 13, 2026:** Design-only. No comments routes, store/schema, widget, stable document ID, moderation workflow, identity system, or comment-specific backup/export integration exists yet. No provider has been selected.

Comments are a later, dynamic subsystem. They should not be written into `site/content`, `site/public`, generated exports, or the static Git scope.

## Recommended boundary

Add a private comment store under `site/.fileloom/` or a separately configured data directory, with APIs under `/_cms/api/comments/...` and a public same-origin widget/HTML endpoint. The static site should contain only the page's stable comment key and widget configuration; comment data remains dynamic.

Prefer a stable immutable document ID in front matter. The current `Document` model has path/slug metadata but no immutable ID; add and backfill this identifier before implementing comments. Paths and slugs are mutable display metadata, not sufficient long-term identity keys.

## Reusable ideas from `my-upc-v2`

- separate SQLite discussion storage;
- thread/reply/parent relationships;
- explicit `open`, `held`, and `removed` moderation states;
- edit windows and post revisions;
- soft deletion;
- plain-text or tightly allow-listed Markdown rendering;
- signed CSRF tokens;
- per-identity and per-IP rate limits;
- FTS-backed search and feeds;
- independent backups and lifecycle locking;
- moderation events and workflow status kept separate.

## Do not copy directly

Fileloom should not introduce a second owner password, a moderation key in URLs, or a long-lived anonymous bearer identity for CMS ownership. Owner/moderator actions should use the existing authenticated CMS boundary plus CSRF and audit logging. Public guest identity, if supported, must be a separate explicitly designed subsystem.

## Candidate evaluation

- **Remark42:** small self-hosted Go service and a good low-operations candidate.
- **Artalk:** stronger fit if public commenters, spam controls, and social/email/OIDC identities are important.
- **Comentario:** richer roles and multi-site capabilities, probably excessive for one Fileloom site.
- **giscus:** easy GitHub-backed experiment, but not Fileloom-owned identity or storage.
- **Roll our own:** justified only if comments must be filesystem/Git-native or tightly coupled to Fileloom revisions and publishing.

The first comments milestone remains a decision spike, not implementation. The next gates are: choose in-process versus hosted/sidecar storage; define identity, moderation, spam/rate limits, CSRF, privacy, caching, backup/restore, and export/Git boundaries; specify path/slug migration using immutable IDs; then prototype a read-only widget before accepting public writes.
