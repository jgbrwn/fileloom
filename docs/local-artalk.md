# Local Artalk sidecar

Fileloom supports two Artalk deployment modes:

- **Local sidecar** — the recommended one-VM setup. Artalk runs as a pinned Go binary under `systemd`, listens only on `127.0.0.1:23366`, and Fileloom proxies the public Artalk API through `/_fileloom/artalk`.
- **External server** — Fileloom publishes the configured browser-reachable Artalk URL and does not proxy it.

The local mode is deliberately opt-in. Installing Artalk does not enable comments or create an Artalk administrator automatically.

## Install the local sidecar

Run this from the Fileloom repository on a Linux VM with `systemd`, `curl`, `tar`, and `sha256sum`:

```bash
sudo ./scripts/install-artalk.sh \
  --site-key "my-fileloom-site"
```

The script reads `FILELOOM_BASE_URL` from the environment or the repository's `.env`; pass `--site-url` explicitly when the public origin is not available there.

The installer:

- downloads the pinned Artalk `2.10.0` Linux release and verifies its official SHA-256 checksum;
- supports Linux `amd64`, `arm64`, and `armv7`;
- installs the binary under `/usr/local/libexec/fileloom-artalk/`;
- creates the dedicated `artalk` system user;
- keeps the SQLite database and uploads under `/var/lib/fileloom-artalk/`;
- writes a loopback-only service at `fileloom-artalk.service`;
- preserves the existing database, app key, and custom `artalk.yml` on reruns; and
- updates only the managed site/port/trusted-domain environment values when rerun.

Set `FILELOOM_BASE_URL` in `.env` to the real public origin before enabling comments. Do not use the Fileloom listener address or `http://127.0.0.1:23366` as the browser-facing Artalk server URL. A visitor's `127.0.0.1` is the visitor's own machine; local mode uses the same-origin `/_fileloom/artalk` proxy instead.

After installation, verify the service:

```bash
sudo systemctl status fileloom-artalk
sudo journalctl -u fileloom-artalk -f
```

Rerun the same installer command after changing the public site URL, site key, port, or Artalk version. It is designed to be safe to rerun. Use `--no-start` when preparing files before a maintenance window.

## Create the Artalk administrator

Fileloom intentionally does not receive or store Artalk administrator credentials. Create the first Artalk administrator locally with the Artalk CLI:

```bash
read -rsp "Artalk password: " ARTALK_PASSWORD; echo
sudo -u artalk /usr/local/libexec/fileloom-artalk/artalk \
  -w /var/lib/fileloom-artalk \
  -c /etc/fileloom-artalk/artalk.yml \
  admin --name admin --email you@example.com --password "$ARTALK_PASSWORD"
unset ARTALK_PASSWORD
```

Keep the Artalk admin surface private. Use an SSH tunnel to the loopback port when administering a VM that does not expose Artalk separately:

```bash
ssh -N -L 23366:127.0.0.1:23366 user@your-vm
```

Then open `http://127.0.0.1:23366` in the local browser. This tunnel is for administration only; public commenters use Fileloom's same-origin API proxy.

## Connect Fileloom to local Artalk

1. Open **Community → Comments** in the Fileloom CMS.
2. If the service is running, Fileloom shows **Local Artalk detected** and preselects local mode.
3. Confirm the suggested site key and local port. The public server value is `/_fileloom/artalk`, not the loopback address.
4. Save the local connection. This still leaves comments disabled unless you explicitly check **Enable comments for this site**.
5. Configure moderation, CAPTCHA/spam controls, notification behavior, privacy messaging, and backups in Artalk.
6. Enable comments only after the service and Artalk administrator are ready.

The CMS can show a ready-to-run installer command, but it never executes `sudo`, downloads a binary, or creates a system service from an HTTP request.

## Files and operations

| Purpose | Location |
| --- | --- |
| Pinned binary symlink | `/usr/local/libexec/fileloom-artalk/artalk` |
| Versioned binaries | `/usr/local/libexec/fileloom-artalk/v*/artalk` |
| Artalk baseline config | `/etc/fileloom-artalk/artalk.yml` |
| Managed alignment values and app key | `/etc/fileloom-artalk/artalk.env` |
| Database, logs, and images | `/var/lib/fileloom-artalk/` |
| systemd unit | `/etc/systemd/system/fileloom-artalk.service` |
| Fileloom public proxy | `/_fileloom/artalk/api/v2/...` |

Back up `/var/lib/fileloom-artalk/` independently of the Fileloom site. Fileloom exports, revisions, and Git intentionally exclude Artalk comment records.

To stop/remove the service while retaining comments, run:

```bash
sudo systemctl disable --now fileloom-artalk
sudo rm -f /etc/systemd/system/fileloom-artalk.service
sudo systemctl daemon-reload
```

Keep `/var/lib/fileloom-artalk/` until its backup and retention policy are complete. The versioned binaries and `/etc/fileloom-artalk/` can then be removed during a deliberate uninstall.


Use external mode when the Artalk server is already public, shared by several sites, or managed by a separate platform. Enter its HTTPS browser URL in the CMS and configure its trusted domain/CORS list with the exact Fileloom public origin.

Local mode depends on the running Fileloom Go server for its same-origin proxy. A raw static export copied to another host will not carry a working local Artalk sidecar; use external mode for independently hosted static output.

## Upgrades and recovery

The installer is pinned to the Artalk client/server version used by Fileloom (`2.10.0`). Upgrade deliberately by passing a reviewed version to the installer, then test the comments flow before enabling it publicly:

```bash
sudo ./scripts/install-artalk.sh --version 2.10.0 \
  --site-url "${FILELOOM_BASE_URL}" --site-key "my-fileloom-site"
```

Restore the Artalk database and `/var/lib/fileloom-artalk/artalk-img/` from the Artalk backup, then restart:

```bash
sudo systemctl restart fileloom-artalk
```

See the [Artalk deployment guide](https://artalk.js.org/en/guide/deploy.html), [configuration reference](https://artalk.js.org/en/guide/backend/config.html), and [release checksums](https://github.com/ArtalkJS/Artalk/releases) when reviewing upgrades.
