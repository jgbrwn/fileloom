# Fileloom security review

Review date: September 11, 2026

## Current protections

- `/_cms` requires an exact configured `X-ExeDev-Email`; missing or mismatched requests receive 404.
- CMS mutation requests reject cross-site `Sec-Fetch-Site`/`Origin` requests when those browser signals are present.
- CMS and public responses receive `nosniff`, strict referrer, and restrictive permissions headers.
- Content paths are normalized and cannot escape `site/content`.
- Builds use a staging directory and atomic public-directory replacement, so a failed build does not erase the last good site.
- ZIP export is limited by file count and compressed output size and excludes source, revisions, Git metadata, and symlinks.
- Editor saves use source hashes and return a conflict instead of overwriting a changed file.
- Revisions and theme-token writes are atomic and checksum-backed.
- Site Git is constrained to `site/.git`; credentials remain outside Fileloom configuration.

## Remaining hardening plan

### 1. Verify the proxy boundary before public release

The identity header is an authorization boundary only when requests can reach Fileloom through the trusted exe.dev proxy. Confirm that the VM cannot be reached through an alternate public port/path and that the proxy overwrites or strips client-supplied identity headers. If the deployment supports it, bind Fileloom to loopback and let the proxy/reverse proxy be the only network listener.

### 2. Harden uploads

SVG is currently accepted as media. Before accepting untrusted uploads, either remove SVG from the default allowlist or sanitize it and serve it with a restrictive content policy. Add MIME sniffing and image dimension/file-type limits if public uploads are enabled.

### 3. Harden Git operations

Keep auto-push disabled by default. Consider rejecting `file://` remotes for unattended deployments, running Git with a sanitized environment, and documenting that site Git hooks and repository configuration are user-controlled code.

### 4. Add operational limits

Add request concurrency limits, scheduler/build timeouts, structured audit logs for mutations, and retention limits for revision snapshots. These are useful on a shared or internet-exposed VM but should be introduced without changing the filesystem source model.

### 5. Add a deliberate CSP profile

The CMS editor needs inline scripts, iframe communication, and vendored assets, while user themes may need their own resources. Define separate, opt-in CSP policies for the CMS and generated public site rather than applying a restrictive global policy that breaks VvvebJs or existing themes.

### 6. Test the deployment, not only the handler

Before making the repository public, test:

- unauthenticated, wrong-account, and correct-account proxy requests;
- forged identity headers sent through the public URL;
- direct listener access and alternate exe.dev ports;
- CSRF requests from another origin;
- malicious paths, symlinks, uploads, theme CSS values, Git remotes, and ZIP contents; and
- restart/catch-up behavior for scheduled publishing.

The conservative release rule is to keep the CMS behind the proxy, keep automatic push off, and treat site HTML/theme code as trusted owner code until a sandboxed rendering model exists.
