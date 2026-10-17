import {
  escapeHTML as h,
  hostname,
  families,
  recordState,
  filterRecords,
} from "./utils.js";
import { t, locale, date } from "./i18n.js";
import { icon, button, field, stateBadge, empty } from "./ui.js";
export const pages = {
  overview: ["Overview", "DNS status and open tasks at a glance."],
  records: ["Hostnames", "Manage your DNS records and check individual hosts."],
  logs: ["Activity", "Follow IP changes, updates and errors."],
  users: ["Users", "Control access to this instance."],
  settings: ["Settings", "Updates, notifications and backups."],
};
export const isAdmin = (s) => s.user?.role === "admin";
export function appearance(s) {
  return `<div class="appearance">${field("locale", "Language", locale, {
    options: s.languages.map((language) => [language.code, language.name]),
  })}${field("theme", "Theme", s.theme, {
    options: [
      ["auto", "System"],
      ["light", "Light"],
      ["dark", "Dark"],
    ],
  })}</div>`;
}
export function shell(s) {
  return `<header class="topbar"><a class="brand" href="#overview"><img src="/img/logo.webp" alt="" width="38" height="38"><strong>DDNS Agent</strong><span class="version">v${h(s.version)}</span></a><div class="top-tools"><span class="role">${h(t(isAdmin(s) ? "Administrator" : "Read-only access"))}</span>${button("Change password", "password", "", "icon-button", "lock")}${button("Sign out", "logout", "", "icon-button", "logout")}</div></header>
  <div class="layout"><aside class="sidebar"><nav aria-label="${h(t("Local instance"))}">${Object.entries(
    pages,
  )
    .filter(([p]) => p !== "users" || isAdmin(s))
    .map(
      ([p, [label]]) =>
        `<a href="#${p}" ${p === s.page ? 'aria-current="page"' : ""}>${icon(p)}<span>${h(t(label))}</span></a>`,
    )
    .join(
      "",
    )}</nav><div class="account"><span class="avatar">${h(s.user.username.slice(0, 1).toUpperCase())}</span><div><strong>${h(s.user.username)}</strong><small>${h(t("Local instance"))}</small></div></div></aside>
  <main id="main" tabindex="-1"></main></div><footer><span>DDNS Agent · ${h(t("Your network. Within reach."))}</span><button type="button" class="live-state" data-action="live" title="${h(t(s.livePaused ? "Resume live view" : "Pause live view"))}"><span class="live-dot ${s.connected && !s.livePaused ? "connected" : ""}"></span><span id="live-label">${h(t(s.livePaused ? "Live paused" : s.connected ? "Live" : "Reconnecting"))}</span></button></footer>`;
}
export function page(s) {
  const [title, subtitle] = pages[s.page];
  let action = "";
  if (["overview", "records"].includes(s.page) && isAdmin(s))
    action = button("Add hostname", "record-new", "", "primary", "plus");
  if (s.page === "users")
    action = button("Add user", "user-new", "", "primary", "plus");
  return `<div class="page-heading"><div><p class="eyebrow">${h(t("Your network. Within reach."))}</p><h1>${h(t(title))}</h1><p class="muted">${h(t(subtitle))}</p></div>${action}</div>${s.loadError ? `<div class="attention" role="alert"><div><strong>${h(t("Unable to load data"))}</strong><p>${h(s.loadError)}</p></div>${button("Retry", "retry")}</div>` : ""}${{ overview: overview, records: records, logs: logs, users: users, settings: settings }[s.page](s)}`;
}
function overview(s) {
  const active = s.records.filter((r) => r.enabled),
    errors = active.filter((r) => r.status === "error");
  return `<div class="stats"><div><span>${h(t("Active hostnames"))}</span><strong>${active.length}<small> / ${s.records.length}</small></strong></div><div><span>${h(t("Synchronized"))}</span><strong>${active.filter((r) => r.status === "success").length}</strong></div><div><span>${h(t("Errors"))}</span><strong class="${errors.length ? "text-error" : ""}">${errors.length}</strong></div></div>
  ${errors.length ? `<div class="attention">${icon("alert")}<div><strong>${h(errors.length === 1 ? t("One hostname needs attention") : errors.length + " " + t("hostnames need attention"))}</strong><p>${h(hostname(errors[0]))} · ${h(errors[0].message || t("Error"))}</p></div>${button("Open details", "record-details", errors[0].id)}</div>` : ""}
  <section class="panel"><div class="section-heading"><h2>${h(t("Active hostnames"))}</h2>${isAdmin(s) ? button("Check all", "refresh-all", "", "", "refresh") : ""}</div>${recordTable(filterRecords(s.records, "", "all", true), s)}</section>
  <section class="panel recent"><div class="section-heading"><h2>${h(t("Recent activity"))}</h2><a href="#logs">${h(t("View activity"))} ${icon("arrow")}</a></div>${logRows(s.recent.slice(0, 4))}</section>`;
}
function records(s) {
  return `<section class="panel"><div class="toolbar"><label class="search"><span>${h(t("Search"))}</span><div>${icon("search")}<input id="record-search" data-filter="recordQuery" value="${h(s.recordQuery)}" placeholder="${h(t("Hostname or provider"))}" type="search"></div></label>${field(
    "recordStatus",
    "Status",
    s.recordStatus,
    {
      options: [
        ["all", "All states"],
        ["success", "Synchronized"],
        ["error", "Error"],
        ["pending", "Pending"],
        ["updating", "Updating"],
        ["paused", "Paused"],
      ],
    },
  )}${isAdmin(s) ? button("Check all", "refresh-all", "", "", "refresh") : ""}</div><div id="record-table">${recordTable(filterRecords(s.records, s.recordQuery, s.recordStatus), s)}</div></section>`;
}
export function recordTable(records, s) {
  if (!records.length)
    return empty(
      s.records.length ? "No matching hostnames" : "No hostnames yet",
      s.records.length
        ? ""
        : "Add your first hostname to keep your network reachable.",
      s.records.length
        ? button("Clear filters", "clear-filters")
        : isAdmin(s)
          ? button("Add hostname", "record-new", "", "primary", "plus")
          : "",
    );
  return `<div class="table-scroll"><table class="records-table"><thead><tr>${["Hostname", "Status", "Address", "Actions"].map((l) => `<th scope="col">${h(t(l))}</th>`).join("")}</tr></thead><tbody>${records
    .map(
      (r) =>
        `<tr><td><button class="text-button hostname" data-action="record-details" data-id="${r.id}">${h(hostname(r))}</button><small>${h(s.providers.find((p) => p.name === r.provider)?.label || r.provider)} <span class="protocol">${families(r).length === 2 ? "IPv4 + IPv6" : r.ip_version === "ipv6" ? "IPv6" : "IPv4"}</span></small></td><td>${stateBadge(recordState(r))}<small title="${h(t("Last successful update"))}">${h(r.last_success ? date(r.last_success) : t("No updates yet"))}</small></td><td><div class="addresses">${families(
          r,
        )
          .map(
            (f) =>
              `<div><span class="family">${f === "ipv4" ? "A" : "AAAA"}</span><code>${h(r["current_" + f] || "—")}</code>${r.enabled ? `<span class="family-state ${h(r["status_" + f])}" title="${h(t({ success: "Synchronized", error: "Error", updating: "Updating", pending: "Pending" }[r["status_" + f]] || "Pending"))}">${r["status_" + f] === "success" ? "✓" : r["status_" + f] === "error" ? "!" : "•"}</span>` : ""}</div>`,
          )
          .join(
            "",
          )}</div></td><td class="row-actions">${button(isAdmin(s) ? "Edit" : "Details", isAdmin(s) ? "record-edit" : "record-details", r.id)}${isAdmin(s) ? `<details class="action-menu"><summary aria-label="${h(t("Actions"))}: ${h(hostname(r))}">···</summary><div>${button("Check", "record-refresh", r.id)}${button(r.enabled ? "Pause" : "Resume", "record-toggle", r.id)}${button("Delete", "record-delete", r.id, "danger-text")}</div></details>` : ""}</td></tr>`,
    )
    .join("")}</tbody></table></div>`;
}
export function logRows(entries) {
  if (!entries.length) return empty("No activity yet");
  return `<div class="log-list">${entries.map((l) => `<div class="log-entry"><time>${h(date(l.created_at))}</time><span class="log-level ${h(l.level.toLowerCase())}">${h(l.level.toUpperCase())}</span><span class="log-source">${h(l.source || "agent")}</span><span class="log-message">${h(l.message)}</span></div>`).join("")}</div>`;
}
function logs(s) {
  return `<section class="panel"><form id="log-filter" class="toolbar">${field("logQuery", "Search", s.logQuery, { type: "search" })}${field(
    "logLevel",
    "Level",
    s.logLevel,
    {
      options: [
        ["", "All levels"],
        ["info", "INFO"],
        ["success", "SUCCESS"],
        ["warning", "WARNING"],
        ["error", "ERROR"],
      ],
    },
  )}<button type="submit">${h(t("Search"))}</button>${isAdmin(s) ? button("Clear activity", "logs-clear", "", "danger-text") : ""}</form>${logRows(s.logs)}<div class="pagination">${button("Previous", "logs-prev", "", "", "")}<span>${h(t("Page"))} ${s.logPage + 1}</span>${button("Next", "logs-next")}</div></section>`;
}
function users(s) {
  return `<section class="panel"><p class="muted">${h(t("Viewers can read status and activity. Administrators manage the instance."))}</p><div class="table-scroll"><table><thead><tr>${["Username", "Role", "Created", "Actions"].map((l) => `<th>${h(t(l))}</th>`).join("")}</tr></thead><tbody>${s.users.map((u) => `<tr><td><strong>${h(u.username)}</strong>${u.id === s.user.id ? ` <span class="protocol">${h(t("Account"))}</span>` : ""}</td><td>${h(t(u.role === "admin" ? "Administrator" : "Viewer"))}</td><td>${h(date(u.created_at))}</td><td class="row-actions">${button("Edit", "user-edit", u.id)}${button("Delete", "user-delete", u.id, "danger-text")}</td></tr>`).join("")}</tbody></table></div></section>`;
}
function settings(s) {
  return `<div class="tabs" role="tablist" aria-label="${h(t("Settings"))}">${["general", ...(isAdmin(s) ? ["notifications", "backup"] : [])].map((tab, i) => `<button role="tab" aria-selected="${tab === s.settingsTab}" data-action="settings-tab" data-id="${tab}" aria-controls="settings-panel">${h(t(["General", "Notifications", "Backup & recovery"][i]))}</button>`).join("")}</div><section id="settings-panel" class="panel settings-panel" role="tabpanel">${({ general: general, notifications: notifications, backup: backup }[s.settingsTab] || general)(s)}</section>`;
}
function general(s) {
  return `<h2>${h(t("Appearance"))}</h2><p class="muted">${h(t("These preferences apply to this browser, including viewer accounts."))}</p>${appearance(s)}${
    !isAdmin(s)
      ? ""
      : `<hr><h2>${h(t("General"))}</h2><form id="settings-form"><div class="form-grid">${[
          ["refresh_interval", "Refresh interval (seconds)", 60, 604800],
          ["cooldown_seconds", "Cooldown (seconds)", 0, 604800],
          ["http_timeout_seconds", "HTTP timeout (seconds)", 1, 900],
          ["backup_retention", "Backup copies to keep", 1, 365],
          ["data_retention_days", "Database retention (days)", 1, 3650],
          ["log_archive_days", "Log archive retention (days)", 1, 3650],
        ]
          .map(([k, l, min, max]) =>
            field(k, l, s.settings[k] || "", {
              type: "number",
              required: true,
              min,
              max,
            }),
          )
          .join(
            "",
          )}${field("app_timezone", "Timezone", s.settings.app_timezone || "", { placeholder: "Europe/Berlin" })}</div><p class="muted">${h(t("Cooldown applies after a successful update. Failed address families are retried."))}</p><div class="form-error" role="alert" hidden></div><button class="primary" type="submit">${h(t("Save"))}</button></form>`
  }`;
}
function notifications(s) {
  return `<div class="section-heading"><h2>${h(t("Notifications"))}</h2>${button("Add notification", "hook-new", "", "primary", "plus")}</div>${
    s.hooks.length
      ? `<div class="notification-list">${s.hooks
          .map(
            (hook) =>
              `<article class="notification"><div><h3>${h(hook.name)} <span class="protocol">${h(hook.type)}</span></h3><p class="muted">${h(hook.display_url)}</p><p>${h(
                hook.events
                  .split(",")
                  .map((e) =>
                    t(
                      {
                        ip_change: "IP change",
                        error: "Error",
                        warning: "Warning",
                      }[e] || e,
                    ),
                  )
                  .join(" · "),
              )}${hook.enabled ? "" : ` · ${h(t("Paused"))}`}</p></div><div class="row-actions">${button("Test", "hook-test", hook.id)}${button("Edit", "hook-edit", hook.id)}${button("Delete", "hook-delete", hook.id, "danger-text")}</div></article>`,
          )
          .join("")}</div>`
      : empty("No notifications configured")
  }`;
}
function backup(s) {
  return `<div class="section-heading"><h2>${h(t("Local backups"))}</h2>${button("Back up now", "backup", "", "", "refresh")}</div><p>${h(t("Last backup"))}: <strong>${h(s.backup.last_success ? date(s.backup.last_success) : t("No backup yet"))}</strong></p>${s.backup.error ? `<p class="text-error">${h(t("Backup failed"))}: ${h(s.backup.error)}</p>` : ""}<p class="muted">${h(t("Database backups include a matching .key file. Keep both files together and protect them."))}</p><hr><h2>${h(t("Export & import"))}</h2><p class="muted">${h(t("Instance exports require this instance’s encryption key. Portable exports are protected by a separate password. Users are not included."))}</p><div class="row-actions">${button("Export configuration", "export")}${button("Import configuration", "import")}</div><hr><h2>${h(t("Database health"))}</h2><p class="muted">${h(t("Repair recreates missing schema objects and makes a raw backup first. It cannot recover damaged SQLite pages."))}</p><div class="row-actions">${button("Check database", "db-check")}${button("Repair database", "db-repair", "", "danger-text")}</div>`;
}
export function login(s, mandatory = false) {
  return `<div class="login-layout"><section class="login-brand"><img src="/img/logo.webp" alt="" width="72" height="72"><span class="eyebrow">DDNS AGENT · v${h(s.version)}</span><h1>${h(t("Your network. Within reach."))}</h1><p>${h(t("Manage dynamic DNS from your own network."))}</p><div class="network-art" aria-hidden="true"><span></span><span></span><span></span><i></i></div><small>${h(t("A secure and portable home for your DNS."))}</small></section><main id="main" tabindex="-1" class="login-content"><div class="login-card"><h2>${h(t(mandatory ? "Change password" : "Sign in"))}</h2><p class="muted">${h(t(mandatory ? "Set a new password to continue." : "Manage dynamic DNS from your own network."))}</p><form id="${mandatory ? "password-form" : "login-form"}">${mandatory ? passwordFields() : `${field("username", "Username", "", { required: true, autocomplete: "username" })}${field("password", "Password", "", { type: "password", required: true, autocomplete: "current-password" })}`}<div class="form-error" role="alert" hidden></div><button type="submit" class="primary full">${h(t(mandatory ? "Save" : "Sign in"))}</button></form><p class="hint">${h(t(mandatory ? "Use 12–72 bytes. Changing a password signs out other sessions." : "First start: use the password in /data/bootstrap-password."))}</p>${appearance(s)}${mandatory ? button("Sign out", "logout") : ""}</div></main></div>`;
}
export function passwordFields() {
  return (
    field("current_password", "Current password", "", {
      type: "password",
      required: true,
      autocomplete: "current-password",
    }) +
    field("password", "New password", "", {
      type: "password",
      required: true,
      autocomplete: "new-password",
    }) +
    field("confirm_password", "Confirm password", "", {
      type: "password",
      required: true,
      autocomplete: "new-password",
    })
  );
}
