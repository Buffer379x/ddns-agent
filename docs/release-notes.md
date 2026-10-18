## DDNS Agent v1.1.2

Refines the interface with a blue palette matched to the logo and a quieter overview.

- Blue light, dark and system themes, including the sign-in page.
- Top-right controls are Live status, an icon-only light/dark switch and icon-only sign out. The role label and password-change shortcut are removed. Mandatory first-login password changes remain available.
- The overview is read-only: no add, check, edit or action buttons in the hostname list. Hostname management remains on the Hostnames page.
- The account block sits directly below Settings in the navigation.
- All settings tabs use the same content width as the other pages. Dropdowns and text fields share a consistent height.
- Activity and Hostnames use matching search fields, with an icon and placeholder. Activity search submits using the magnifier or Enter.
- Updated screenshots of the running app use synthetic demonstration data.

No database, authentication, provider or updater changes. Existing v1.1.x installations can update the image while retaining their data volume. For upgrades from 1.0.x, follow [UPGRADE.md](https://github.com/Buffer379x/ddns-agent/blob/main/docs/UPGRADE.md).

Images: `ghcr.io/buffer379x/ddns-agent:v1.1.2`, `:1.1.2`, and `:latest` for Linux AMD64 and ARM64.
