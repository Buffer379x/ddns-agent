## DDNS Agent v1.1.1

Corrects Vultr pagination introduced in v1.1.0: the API's next-page value is an opaque cursor, not a URL. Records on later pages now update correctly. Repeated cursors stop safely without creating records, and URL-like cursor values cannot change the request destination. Regression tests and the full quality pipeline cover the fix.

The runtime image also applies Alpine package updates during the build, including zlib's fix for CVE-2026-85091. The final image is checked separately from Go's reachability analysis.

The interface and database schema are unchanged from v1.1.0. Existing v1.1.0 installations can update the image and retain their data volume. Source, binary and image versions are 1.1.1; the UI reads the running server's version.

Images: `ghcr.io/buffer379x/ddns-agent:v1.1.1`, `:1.1.1`, and `:latest` (Linux AMD64 and ARM64).

### Included changes from v1.1.0

A complete interface refresh and a security/reliability cleanup, retaining Go, SQLite, the embedded web panel and all 51 provider adapters.

- Responsive light/dark interface, German/English translations, searchable hostnames, independent IPv4/IPv6 status and live activity.
- Revocable sessions, safe proxy handling, secure first login, local browser assets, encrypted notification endpoints and redacted API responses.
- Transactional configuration import with preview/pre-import backup; password-protected portable exports; database log/history retention.
- Current Hetzner Console integration, corrected IONOS/Njalla paths, pagination, exact record matching and strict provider error handling.
- Bounded updater/webhook work, graceful shutdown, health checks on the configured port and a non-root application process.
- Regression tests, race detection, static analysis, vulnerability checks, and matching release/container versions for Linux AMD64 and ARM64.

**Upgrade:** stop and back up the complete data directory, including `.key`, before updating. See [UPGRADE.md](https://github.com/Buffer379x/ddns-agent/blob/main/docs/UPGRADE.md). Existing `admin/admin` accounts must change their password; other account passwords are retained. Hetzner users need a current Console API token. Browser sessions must be renewed after upgrading from 1.0.x. Configuration exports exclude user accounts; full database backups include them.

The [review and validation report](https://github.com/Buffer379x/ddns-agent/blob/main/docs/v1.1.0-review.md) documents scope, checks and remaining test limits. Screenshots use synthetic data; no production DNS zone was changed during development.
