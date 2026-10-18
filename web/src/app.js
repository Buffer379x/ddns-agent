import { escapeHTML as h, hostname, families, filterRecords } from "./utils.js";
import { t, locale, setLocale, loadLocale, setTimezone, date } from "./i18n.js";
import { button, field, formActions, stateBadge } from "./ui.js";
import { shell, page, login, pages, isAdmin, recordTable } from "./views.js";

const state = {
  languages: [
    { code: "de", name: "Deutsch" },
    { code: "en", name: "English" },
  ],
  user: null,
  version: "…",
  page: "overview",
  theme:
    localStorage.getItem("ddns.theme") ||
    localStorage.getItem("ddns_theme") ||
    "auto",
  records: [],
  providers: [],
  users: [],
  hooks: [],
  settings: {},
  backup: {},
  recent: [],
  logs: [],
  logTotal: 0,
  logPage: 0,
  logQuery: "",
  logLevel: "",
  recordQuery: "",
  recordStatus: "all",
  settingsTab: "general",
  connected: false,
  livePaused: false,
};
const app = document.querySelector("#app"),
  dialog = document.querySelector("#dialog");
let source,
  liveTimer,
  noticeTimer,
  routeGeneration = 0;
localStorage.removeItem("ddns_token");
setLocale(locale);
applyTheme();
function applyTheme() {
  document.documentElement.dataset.theme = state.theme;
}
function setTheme(theme) {
  state.theme = theme;
  localStorage.setItem("ddns.theme", theme);
  applyTheme();
  const select = document.querySelector('select[name="theme"]');
  if (select) select.value = theme;
}
function notice(message, error = false) {
  const node = document.querySelector("#notice");
  node.textContent = t(message);
  node.classList.toggle("error", error);
  node.hidden = false;
  clearTimeout(noticeTimer);
  noticeTimer = setTimeout(
    () => {
      node.hidden = true;
    },
    error ? 10000 : 4500,
  );
}
async function api(path, method = "GET", body, download = false) {
  let response;
  try {
    response = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: body !== undefined ? { "Content-Type": "application/json" } : {},
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new Error(t("Network unavailable"));
  }
  if (response.status === 401 && state.user) {
    signOutLocal();
    notice("You are signed out. Please sign in again.", true);
  }
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(t(data.error || "Request failed"));
  }
  return download ? response.blob() : response.json();
}
function signOutLocal() {
  state.user = null;
  routeGeneration++;
  Object.assign(state, {
    records: [],
    users: [],
    hooks: [],
    settings: {},
    backup: {},
    recent: [],
    logs: [],
    settingsTab: "general",
    loadError: "",
  });
  closeLive();
  dialog.close();
  render();
}
function render() {
  if (!state.user || state.user.must_change_password) {
    document.title =
      t(state.user ? "Change password" : "Sign in") + " · DDNS Agent";
    app.innerHTML = login(state, !!state.user);
    return;
  }
  app.innerHTML = shell(state);
  renderMain();
}
function renderMain() {
  if (!state.user || state.user.must_change_password) return;
  const active = document.activeElement,
    id = active?.id,
    selection = active?.selectionStart;
  document.querySelector("#main").innerHTML = page(state);
  if (id) {
    const next = document.getElementById(id);
    if (next) {
      next.focus({ preventScroll: true });
      if (typeof selection === "number" && next.setSelectionRange)
        next.setSelectionRange(selection, selection);
    }
  }
  document.title = `${t(pages[state.page][0])} · DDNS Agent`;
}
async function loadRecords() {
  state.records = (await api("/api/records")) || [];
}
async function loadLogs() {
  const query = new URLSearchParams({
    limit: "50",
    offset: String(state.logPage * 50),
    search: state.logQuery,
    level: state.logLevel,
  });
  const result = await api("/api/logs?" + query);
  state.logs = result.logs;
  state.logTotal = result.total;
}
async function loadPage() {
  const generation = ++routeGeneration;
  state.loadError = "";
  try {
    if (state.page === "overview") {
      await loadRecords();
      state.recent = (await api("/api/logs?limit=4")).logs;
    }
    if (state.page === "records") await loadRecords();
    if (state.page === "logs") await loadLogs();
    if (state.page === "users") state.users = await api("/api/users");
    if (state.page === "settings") {
      state.settings = await api("/api/settings");
      setTimezone(state.settings.app_timezone);
      if (isAdmin(state))
        [state.hooks, state.backup] = await Promise.all([
          api("/api/webhooks"),
          api("/api/backup/status"),
        ]);
    }
    if (generation === routeGeneration) renderMain();
  } catch (error) {
    state.loadError = error.message;
    renderMain();
    throw error;
  }
}
async function route() {
  const requested = location.hash.slice(1);
  state.page =
    pages[requested] && !(requested === "users" && !isAdmin(state))
      ? requested
      : "overview";
  if (state.user && !state.user.must_change_password) {
    render();
    await loadPage();
  }
}
function closeLive() {
  source?.close();
  source = null;
  clearTimeout(liveTimer);
  state.connected = false;
}
function liveLabel() {
  const node = document.querySelector(".live-state");
  if (node) {
    node.querySelector("#live-label").textContent = t(
      state.livePaused
        ? "Live paused"
        : state.connected
          ? "Live"
          : "Reconnecting",
    );
    node
      .querySelector(".live-dot")
      .classList.toggle("connected", state.connected && !state.livePaused);
    node.title = t(state.livePaused ? "Resume live view" : "Pause live view");
  }
}
function startLive() {
  closeLive();
  if (state.livePaused || !state.user || state.user.must_change_password)
    return;
  source = new EventSource("/api/events");
  source.onopen = () => {
    state.connected = true;
    liveLabel();
    scheduleLive();
  };
  source.onerror = async () => {
    state.connected = false;
    liveLabel();
    try {
      await api("/api/auth/me");
    } catch {}
  };
  source.addEventListener("records", scheduleLive);
  source.addEventListener("log", scheduleLive);
  liveLabel();
}
function scheduleLive() {
  if (liveTimer) return;
  liveTimer = setTimeout(async () => {
    liveTimer = null;
    if (!state.user) return;
    try {
      if (["overview", "records", "logs"].includes(state.page))
        await loadPage();
    } catch (error) {
      notice(error.message, true);
    }
  }, 2500);
}
async function authenticated(user) {
  state.user = user;
  render();
  if (user.must_change_password) return;
  [state.providers, state.settings] = await Promise.all([
    api("/api/providers"),
    api("/api/settings"),
  ]);
  setTimezone(state.settings.app_timezone);
  await route();
  startLive();
}
function modal(title, body, form = "") {
  dialog.innerHTML = `<div class="dialog-heading"><h2 id="dialog-title">${h(t(title))}</h2>${button("Close", "close", "", "close-button")}</div>${form ? `<form id="${form}">${body}</form>` : body}`;
  if (!dialog.open) dialog.showModal();
  const first = dialog.querySelector(
    "input:not([type=checkbox]),select,textarea",
  );
  first?.focus();
}
function confirm(title, run, success = "Deleted") {
  modal(
    title,
    `<p>${h(t("This action cannot be undone."))}</p><div class="form-actions">${button("Cancel", "close")}<button type="button" class="danger" id="confirm-action">${h(t("Confirm"))}</button></div>`,
  );
  dialog.querySelector("#confirm-action").onclick = async (event) => {
    event.target.disabled = true;
    try {
      await run();
      dialog.close();
      if (success) notice(success);
      await loadPage();
    } catch (error) {
      notice(error.message, true);
    } finally {
      event.target.disabled = false;
    }
  };
}
let editingRecord = null,
  editingUser = null,
  editingHook = null,
  importData = "",
  importPassword = "",
  recordDraft = {};
const providerFields = () =>
  state.providers.find((p) => p.name === recordDraft.provider)?.fields || [];
function recordDialog() {
  const r = recordDraft;
  modal(
    editingRecord ? "Edit hostname" : "Add hostname",
    `
    <div class="form-grid">${field("provider", "Provider", r.provider, { required: true, options: state.providers.map((p) => [p.name, p.label]), readonly: !!editingRecord })}${field(
      "ip_version",
      "Address families",
      r.ip_version,
      {
        options: [
          ["ipv4", "IPv4"],
          ["ipv6", "IPv6"],
          ["dual", "IPv4 + IPv6"],
        ],
      },
    )}${field("domain", "Domain", r.domain, { required: true, placeholder: "example.net" })}${field("owner", "Record name", r.owner, { required: true, help: "Use @ for the root domain." })}</div>
    ${field("enabled", "Enabled", r.enabled, { type: "checkbox" })}<hr><h3>${h(t("Credentials"))}</h3><p class="muted">${h(t("Credentials are encrypted. Leave secret fields blank to keep their stored values."))}</p>
    <div class="form-grid provider-fields">${providerFields()
      .map((f) =>
        field(
          "config." + f.name,
          f.label,
          r.config[f.name] ?? (f.type === "checkbox" ? false : ""),
          {
            type: f.type,
            required: f.required && !editingRecord,
            placeholder:
              editingRecord && ["password", "textarea"].includes(f.type)
                ? "Keep stored value"
                : f.placeholder || "",
            help: f.help || "",
            autocomplete: f.type === "password" ? "new-password" : "",
          },
        ),
      )
      .join("")}</div>${formActions("Save")}`,
    "record-form",
  );
  if (editingRecord) dialog.querySelector("[name=provider]").disabled = true;
}
async function openRecord(id) {
  editingRecord = id || null;
  const r = id
    ? await api("/api/records/" + id)
    : {
        provider: state.providers[0]?.name,
        domain: "",
        owner: "@",
        ip_version: "ipv4",
        enabled: true,
      };
  recordDraft = {
    ...r,
    config: r.provider_config ? JSON.parse(r.provider_config) : {},
  };
  recordDialog();
}
function recordFromForm(form) {
  const config = {};
  for (const f of providerFields()) {
    const input = form.elements["config." + f.name];
    if (f.type === "checkbox") config[f.name] = input.checked;
    else if (input.value !== "") config[f.name] = input.value;
  }
  return {
    provider: recordDraft.provider,
    domain: form.elements.domain.value.trim(),
    owner: form.elements.owner.value.trim(),
    ip_version: form.elements.ip_version.value,
    enabled: form.elements.enabled.checked,
    provider_config: JSON.stringify(config),
  };
}
function recordDetails(id) {
  const r = state.records.find((record) => record.id === id);
  if (!r) return;
  modal(
    "Details",
    `<p class="detail-host">${h(hostname(r))}</p><p>${h(r.provider)} · ${stateBadge(r.enabled ? r.status : "paused")}</p><div class="detail-families">${families(
      r,
    )
      .map(
        (f) =>
          `<section><h3>${f === "ipv4" ? "IPv4 · A" : "IPv6 · AAAA"}</h3>${stateBadge(r.enabled ? r["status_" + f] : "paused")}<p><code>${h(r["current_" + f] || "—")}</code></p><p class="muted">${h(r["message_" + f])}</p></section>`,
      )
      .join(
        "",
      )}</div><dl><dt>${h(t("Last successful update"))}</dt><dd>${h(date(r.last_success))}</dd><dt>${h(t("Last attempt"))}</dt><dd>${h(date(r.last_update))}</dd></dl><div class="form-actions">${button("Close", "close")}${isAdmin(state) ? button("Edit", "record-edit", id, "primary") : ""}</div>`,
  );
}
function userDialog(id) {
  editingUser = id || null;
  const user = state.users.find((u) => u.id === id) || {
    username: "",
    role: "viewer",
  };
  modal(
    id ? "Edit user" : "Add user",
    `${field("username", "Username", user.username, { required: true, autocomplete: "off" })}${field("password", "Password", "", { type: "password", required: !id, autocomplete: "new-password", help: id ? "Leave blank to keep the current password." : "Use 12–72 bytes. Changing a password signs out other sessions." })}${field(
      "role",
      "Role",
      user.role,
      {
        options: [
          ["admin", "Administrator"],
          ["viewer", "Viewer"],
        ],
      },
    )}${formActions("Save")}`,
    "user-form",
  );
}
function hookDialog(id) {
  editingHook = id || null;
  const hook = state.hooks.find((v) => v.id === id) || {
    name: "",
    type: "discord",
    events: "ip_change,error",
    enabled: true,
  };
  modal(
    id ? "Edit notification" : "Add notification",
    `${field("name", "Name", hook.name, { required: true })}${field("type", "Type", hook.type, { options: ["discord", "slack", "telegram", "gotify", "generic"].map((v) => [v, v === "generic" ? "Webhook" : v[0].toUpperCase() + v.slice(1)]) })}${field("url", "Destination", "", { type: "password", required: !id, placeholder: id ? "Keep stored value" : "https://…", help: "Telegram: bot_token|chat_id. Gotify: server URL with ?token=…", autocomplete: "new-password" })}<fieldset><legend>${h(t("Events"))}</legend>${[
      ["ip_change", "IP change"],
      ["error", "Error"],
      ["warning", "Warning"],
    ]
      .map(([key, label]) =>
        field("event." + key, label, hook.events.split(",").includes(key), {
          type: "checkbox",
        }),
      )
      .join(
        "",
      )}</fieldset>${field("enabled", "Enabled", hook.enabled, { type: "checkbox" })}${formActions("Save")}`,
    "hook-form",
  );
}
function exportDialog() {
  modal(
    "Export configuration",
    `${field("mode", "Type", "portable", {
      options: [
        ["portable", "Portable export"],
        ["instance", "Instance export"],
      ],
    })}${field("password", "Export password", "", { type: "password", required: true, autocomplete: "new-password" })}<p class="muted">${h(t("Instance exports require this instance’s encryption key. Portable exports are protected by a separate password. Users are not included."))}</p>${formActions("Download export")}`,
    "export-form",
  );
}
function importDialog() {
  importData = "";
  importPassword = "";
  modal(
    "Import configuration",
    `${field("file", "Configuration file", "", { type: "file", required: true })}${field("password", "Import password (portable exports)", "", { type: "password", autocomplete: "off" })}${formActions("Preview import")}`,
    "import-preview-form",
  );
  dialog.querySelector("[type=file]").accept = ".json,application/json";
}
function download(blob, name) {
  const url = URL.createObjectURL(blob),
    a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
async function refreshLocal() {
  await loadPage();
  notice("Saved");
}
const actions = {
  close: () => dialog.close(),
  theme: () => {
    const dark =
      state.theme === "dark" ||
      (state.theme === "auto" &&
        matchMedia("(prefers-color-scheme: dark)").matches);
    setTheme(dark ? "light" : "dark");
  },
  logout: async () => {
    await api("/api/auth/logout", "POST", {});
    signOutLocal();
  },
  "record-new": () => openRecord(),
  "record-edit": (id) => openRecord(id),
  "record-details": recordDetails,
  "record-delete": (id) =>
    confirm("Delete this hostname?", () => api("/api/records/" + id, "DELETE")),
  "record-toggle": async (id) => {
    const r = state.records.find((v) => v.id === id);
    await api("/api/records/" + id, "PUT", {
      ...r,
      enabled: !r.enabled,
      provider_config: "",
    });
    await refreshLocal();
  },
  "record-refresh": async (id) => {
    await api("/api/records/" + id + "/refresh", "POST", {});
    await loadPage();
    notice("Request accepted");
  },
  "refresh-all": async () => {
    await api("/api/records/refresh-all", "POST", {});
    notice("Request accepted");
  },
  "clear-filters": () => {
    state.recordQuery = "";
    state.recordStatus = "all";
    renderMain();
  },
  "user-new": () => userDialog(),
  "user-edit": userDialog,
  "user-delete": (id) =>
    confirm("Delete this user?", () => api("/api/users/" + id, "DELETE")),
  "hook-new": () => hookDialog(),
  "hook-edit": hookDialog,
  "hook-delete": (id) =>
    confirm("Delete this notification?", () =>
      api("/api/webhooks/" + id, "DELETE"),
    ),
  "hook-test": async (id) => {
    await api("/api/webhooks/" + id + "/test", "POST", {});
    notice("Test delivered");
  },
  "settings-tab": (id) => {
    state.settingsTab = id;
    renderMain();
  },
  backup: async () => {
    state.backup = await api("/api/backup", "POST", {});
    renderMain();
    notice("Backup created");
  },
  export: exportDialog,
  import: importDialog,
  "db-check": async () => {
    const result = await api("/api/database/check", "POST", {});
    modal(
      "Database health",
      `<p>${h(result.ok ? t("Database is healthy") : result.issues.join("\n"))}</p><div class="form-actions">${button("Close", "close")}</div>`,
    );
  },
  "db-repair": () =>
    confirm(
      "Repair database now?",
      async () => {
        const result = await api("/api/database/repair", "POST", {});
        notice(
          result.ok ? "Database is healthy" : JSON.stringify(result),
          !result.ok,
        );
      },
      null,
    ),
  "logs-clear": () =>
    confirm("Delete all activity entries?", () => api("/api/logs", "DELETE")),
  "logs-prev": async () => {
    if (state.logPage > 0) {
      state.logPage--;
      await loadPage();
    }
  },
  "logs-next": async () => {
    if ((state.logPage + 1) * 50 < state.logTotal) {
      state.logPage++;
      await loadPage();
    }
  },
  live: async () => {
    state.livePaused = !state.livePaused;
    if (state.livePaused) closeLive();
    else {
      startLive();
      await loadPage();
    }
    liveLabel();
  },
  retry: loadPage,
};
document.addEventListener("click", async (event) => {
  if (event.target.closest(".skip")) {
    event.preventDefault();
    document.querySelector("#main")?.focus();
    return;
  }
  document.querySelectorAll("details[open]").forEach((menu) => {
    if (!menu.contains(event.target) || event.target.closest("[data-action]"))
      menu.open = false;
  });
  const target = event.target.closest("[data-action]");
  if (!target || target.disabled) return;
  const action = actions[target.dataset.action];
  if (!action) return;
  const id = target.dataset.id;
  target.disabled = true;
  try {
    await action(id && /^\d+$/.test(id) ? Number(id) : id);
  } catch (error) {
    notice(error.message, true);
  } finally {
    target.disabled = false;
  }
});
document.addEventListener("input", (event) => {
  if (event.target.dataset.filter === "recordQuery") {
    state.recordQuery = event.target.value;
    document.querySelector("#record-table").innerHTML = recordTable(
      filterRecords(state.records, state.recordQuery, state.recordStatus),
      state,
    );
  }
  if (event.target.closest("#log-filter"))
    state[event.target.name] = event.target.value;
  if (event.target.closest("#settings-form"))
    state.settings[event.target.name] = event.target.value;
});
document.addEventListener("change", async (event) => {
  const target = event.target;
  if (target.name === "locale") {
    await loadLocale(target.value);
    render();
    if (dialog.open) dialog.close();
  }
  if (target.name === "theme") {
    setTheme(target.value);
  }
  if (target.name === "recordStatus") {
    state.recordStatus = target.value;
    document.querySelector("#record-table").innerHTML = recordTable(
      filterRecords(state.records, state.recordQuery, state.recordStatus),
      state,
    );
  }
  if (target.name === "provider" && target.closest("#record-form")) {
    const f = target.form;
    recordDraft = { ...recordFromForm(f), provider: target.value, config: {} };
    recordDialog();
  }
  if (target.name === "mode" && target.closest("#export-form")) {
    const f = target.form;
    f.elements.password.required = target.value === "portable";
    f.elements.password.closest("label").hidden = target.value !== "portable";
  }
});
const submits = {
  "login-form": async (form, data) => {
    const result = await api("/api/auth/login", "POST", data);
    await authenticated(result.user);
  },
  "password-form": async (form, data) => {
    if (data.password !== data.confirm_password)
      throw new Error(t("Passwords do not match"));
    const result = await api("/api/auth/password", "POST", {
      current_password: data.current_password,
      password: data.password,
    });
    dialog.close();
    await authenticated(result.user);
    notice("Saved");
  },
  "record-form": async (form) => {
    await api(
      "/api/records" + (editingRecord ? "/" + editingRecord : ""),
      editingRecord ? "PUT" : "POST",
      recordFromForm(form),
    );
    dialog.close();
    await refreshLocal();
  },
  "user-form": async (form, data) => {
    await api(
      "/api/users" + (editingUser ? "/" + editingUser : ""),
      editingUser ? "PUT" : "POST",
      data,
    );
    dialog.close();
    if (editingUser === state.user.id) {
      signOutLocal();
      notice("You are signed out. Please sign in again.");
    } else await refreshLocal();
  },
  "hook-form": async (form, data) => {
    const events = ["ip_change", "error", "warning"]
      .filter((key) => form.elements["event." + key].checked)
      .join(",");
    if (!events) throw new Error(t("Please select at least one event."));
    await api(
      "/api/webhooks" + (editingHook ? "/" + editingHook : ""),
      editingHook ? "PUT" : "POST",
      {
        name: data.name,
        type: data.type,
        url: data.url,
        events,
        enabled: form.elements.enabled.checked,
      },
    );
    dialog.close();
    await refreshLocal();
  },
  "settings-form": async (form, data) => {
    await api("/api/settings", "PUT", data);
    await refreshLocal();
  },
  "log-filter": async (form, data) => {
    state.logQuery = data.logQuery;
    state.logLevel = data.logLevel;
    state.logPage = 0;
    await loadPage();
  },
  "export-form": async (form, data) => {
    const portable = data.mode === "portable";
    const blob = await api(
      "/api/config/export",
      portable ? "POST" : "GET",
      portable ? { password: data.password } : undefined,
      true,
    );
    download(
      blob,
      portable ? "ddns-agent-portable.json" : "ddns-agent-config.json",
    );
    dialog.close();
  },
  "import-preview-form": async (form, data) => {
    const file = form.elements.file.files[0];
    if (!file) throw new Error(t("Configuration file"));
    if (file.size > 10 * 1024 * 1024) throw new Error("File exceeds 10 MiB");
    importData = await file.text();
    importPassword = data.password;
    const result = await api("/api/config/preview", "POST", {
      data: importData,
      password: importPassword,
    });
    modal(
      "Import preview",
      `<dl><dt>${h(t("Records"))}</dt><dd>${result.records}</dd><dt>${h(t("New notifications"))}</dt><dd>${result.new_webhooks}</dd><dt>${h(t("Settings to update"))}</dt><dd>${result.settings}</dd></dl><p class="muted">${h(t("Existing settings are updated, notifications are merged, users stay unchanged. A local backup is required before importing."))}</p>${field(
        "mode",
        "Type",
        "merge",
        {
          options: [
            ["merge", "Merge records"],
            ["replace", "Replace records"],
          ],
        },
      )}${formActions("Apply import")}`,
      "import-form",
    );
  },
  "import-form": async (form, data) => {
    await api("/api/config/import?records=" + data.mode, "POST", {
      data: importData,
      password: importPassword,
    });
    importData = "";
    importPassword = "";
    dialog.close();
    await loadPage();
    notice("Import completed");
  },
};
document.addEventListener("submit", async (event) => {
  const handler = submits[event.target.id];
  if (!handler) return;
  event.preventDefault();
  const form = event.target;
  const submit = form.querySelector("[type=submit]");
  submit.disabled = true;
  const errorNode = form.querySelector(".form-error");
  if (errorNode) errorNode.hidden = true;
  try {
    await handler(form, Object.fromEntries(new FormData(form)));
  } catch (error) {
    if (errorNode) {
      errorNode.textContent = error.message;
      errorNode.hidden = false;
      errorNode.focus();
    } else notice(error.message, true);
  } finally {
    submit.disabled = false;
  }
});
dialog.addEventListener("close", () => {
  dialog.replaceChildren();
  recordDraft = {};
  importData = "";
  importPassword = "";
});
window.addEventListener("hashchange", () =>
  route().catch((error) => notice(error.message, true)),
);
window.addEventListener("online", () => {
  if (state.user) startLive();
});
window.addEventListener("pagehide", closeLive);
async function init() {
  state.version = (await api("/api/version")).version;
  state.languages = await api("/api/lang");
  await loadLocale(locale);
  let user;
  try {
    user = await api("/api/auth/me");
  } catch {
    render();
    return;
  }
  await authenticated(user);
}
init().catch((error) => {
  render();
  notice(error.message, true);
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape")
    document.querySelectorAll("details[open]").forEach((menu) => {
      menu.open = false;
    });
  if (
    !event.target.matches('[role="tab"]') ||
    !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)
  )
    return;
  event.preventDefault();
  const tabs = [...event.target.parentElement.querySelectorAll('[role="tab"]')];
  let index = tabs.indexOf(event.target);
  index =
    event.key === "Home"
      ? 0
      : event.key === "End"
        ? tabs.length - 1
        : (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) %
          tabs.length;
  tabs[index].click();
  document.querySelector('[role="tab"][aria-selected="true"]')?.focus();
});
