# Comments engine plan

Comments are a later, dynamic subsystem. They should not be written into `site/content`, `site/public`, generated exports, or the static Git scope.

## Recommended boundary

Add a private comment store under `site/.fileloom/` or a separately configured data directory, with APIs under `/_cms/api/comments/...` and a public same-origin widget/HTML endpoint. The static site should contain only the page's stable comment key and widget configuration; comment data remains dynamic.

Prefer a stable immutable document ID in front matter. URL slugs and paths can change, so they should be stored as current display metadata, not used as the only identity key.

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

The first comments milestone should be a decision spike, not implementation: define anonymous/authenticated identity, moderation policy, cache behavior, stable document identity, backup/restore, and whether a hosted/self-hosted sidecar is acceptable.
