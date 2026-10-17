## DDNS Agent v1.1.0

A complete interface refresh and a security/reliability cleanup, retaining Go, SQLite, the embedded web panel and all 51 provider adapters.

- Responsive light/dark interface, German/English translations, searchable hostnames, independent IPv4/IPv6 status and live activity.
- Revocable sessions, safe proxy handling, secure first login, local browser assets, encrypted notification endpoints and redacted API responses.
- Transactional configuration import with preview/pre-import backup; password-protected portable exports; database log/history retention.
- Current Hetzner Console integration, corrected IONOS/Njalla paths, pagination, exact record matching and strict provider error handling.
- Bounded updater/webhook work, graceful shutdown, health checks on the configured port and a non-root application process.
- Regression tests, race detection, static analysis, vulnerability checks, and matching release/container versions for Linux AMD64 and ARM64.

**Upgrade:** stop and back up the complete data directory, including `.key`, before updating. See [UPGRADE.md](https://github.com/Buffer379x/ddns-agent/blob/main/docs/UPGRADE.md). Existing `admin/admin` accounts must change their password; other account passwords are retained. Hetzner users need a current Console API token. Browser sessions must be renewed after upgrading from 1.0.x. Configuration exports exclude user accounts; full database backups include them.

Images: `ghcr.io/buffer379x/ddns-agent:v1.1.0`, `:1.1.0`, and `:latest`.

The [review and validation report](https://github.com/Buffer379x/ddns-agent/blob/main/docs/v1.1.0-review.md) documents scope, checks and remaining test limits. Screenshots use synthetic data; no production DNS zone was changed during development.
