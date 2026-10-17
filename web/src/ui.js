import { escapeHTML as h } from "./utils.js";
import { t } from "./i18n.js";
const paths = {
  overview: "M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z",
  records:
    "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M3 12h18 M12 3c5 5 5 13 0 18-5-5-5-13 0-18",
  logs: "M6 3h12v18H6z M9 7h6 M9 11h6 M9 15h4",
  users:
    "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2 M9 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M17 4a4 4 0 0 1 0 7 M22 21v-2a4 4 0 0 0-3-4",
  settings: "M4 7h16 M4 17h16 M8 4v6 M16 14v6",
  plus: "M12 5v14 M5 12h14",
  refresh:
    "M20 7v5h-5 M4 17v-5h5 M5 7a8 8 0 0 1 13-2l2 3 M4 16l2 3a8 8 0 0 0 13-2",
  arrow: "M5 12h14 M14 7l5 5-5 5",
  check: "M5 12l4 4L19 6",
  alert: "M12 3l10 18H2z M12 9v4 M12 17h.01",
  logout: "M9 4H4v16h5 M10 12h11 M17 8l4 4-4 4",
  close: "M6 6l12 12 M18 6L6 18",
  lock: "M5 10h14v11H5z M8 10V6a4 4 0 0 1 8 0v4",
  search: "M10 3a7 7 0 1 0 0 14 7 7 0 0 0 0-14 M15 15l6 6",
};
export const icon = (name) =>
  `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="${paths[name] || paths.records}"/></svg>`;
export const button = (label, action, id = "", variant = "", symbol = "") =>
  `<button type="button" class="${variant}" data-action="${action}" ${id !== "" ? `data-id="${h(id)}"` : ""}>${symbol ? icon(symbol) : ""}${h(t(label))}</button>`;
export function field(name, label, value = "", opts = {}) {
  const {
    type = "text",
    required = false,
    min,
    max,
    placeholder = "",
    help = "",
    autocomplete = "",
    readonly = false,
  } = opts;
  const attrs = `id="f-${h(name)}" name="${h(name)}" ${required ? "required" : ""} ${min != null ? `min="${min}"` : ""} ${max != null ? `max="${max}"` : ""} ${autocomplete ? `autocomplete="${h(autocomplete)}"` : ""} ${readonly ? "readonly" : ""}`;
  let input;
  if (opts.options)
    input = `<select ${attrs}>${opts.options.map(([v, l]) => `<option value="${h(v)}" ${String(value) === String(v) ? "selected" : ""}>${h(t(l))}</option>`).join("")}</select>`;
  else if (type === "checkbox")
    input = `<input ${attrs} type="checkbox" ${value ? "checked" : ""}>`;
  else if (type === "textarea")
    input = `<textarea ${attrs} rows="4" placeholder="${h(t(placeholder))}">${h(typeof value === "object" ? JSON.stringify(value) : value)}</textarea>`;
  else
    input = `<input ${attrs} type="${h(type)}" value="${h(value)}" placeholder="${h(t(placeholder))}">`;
  return `<label class="field ${type === "checkbox" ? "check-field" : ""}" for="f-${h(name)}"><span>${h(t(label))}${required ? ' <span class="required">*</span>' : ""}</span>${input}${help ? `<small>${h(t(help))}</small>` : ""}</label>`;
}
export function stateBadge(status) {
  const labels = {
    success: "Synchronized",
    error: "Error",
    pending: "Pending",
    updating: "Updating",
    paused: "Paused",
  };
  return `<span class="badge ${h(status)}"><span aria-hidden="true">${status === "success" ? "✓" : status === "error" ? "!" : status === "paused" ? "Ⅱ" : "•"}</span>${h(t(labels[status] || "Pending"))}</span>`;
}
export const empty = (title, hint = "", action = "") =>
  `<div class="empty">${icon("records")}<h3>${h(t(title))}</h3>${hint ? `<p>${h(t(hint))}</p>` : ""}${action}</div>`;
export const formActions = (label) =>
  `<div class="form-error" role="alert" hidden></div><div class="form-actions"><button type="button" data-action="close">${h(t("Cancel"))}</button><button class="primary" type="submit">${h(t(label))}</button></div>`;
