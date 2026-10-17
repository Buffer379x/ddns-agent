import de from "../lang/de.json";
import en from "../lang/en.json";
let dictionary;
export let locale =
  localStorage.getItem("ddns.locale") ||
  localStorage.getItem("ddns_lang") ||
  (navigator.language.startsWith("de") ? "de" : "en");
export const t = (text) =>
  (dictionary || (locale === "de" ? de : en))[text] || text;
export function setLocale(value) {
  locale = /^[a-z]{2}(-[A-Z]{2})?$/.test(value) ? value : "en";
  dictionary = locale === "de" ? de : en;
  localStorage.setItem("ddns.locale", locale);
  document.documentElement.lang = locale;
}
let timezone = "";
export function setTimezone(value) {
  try {
    if (value) new Intl.DateTimeFormat("en", { timeZone: value });
    timezone = value || "";
  } catch {
    timezone = "";
  }
}
export function date(value) {
  if (!value) return t("Never");
  const parsed = new Date(
    value.endsWith?.("Z") || /[+-]\d\d:\d\d$/.test(value)
      ? value
      : value.replace(" ", "T") + "Z",
  );
  return Number.isNaN(parsed.getTime())
    ? "—"
    : new Intl.DateTimeFormat(locale, {
        timeZone: timezone || undefined,
        dateStyle: "medium",
        timeStyle: "short",
      }).format(parsed);
}

export async function loadLocale(value) {
  setLocale(value);
  if (locale === "de" || locale === "en") return;
  try {
    const response = await fetch("/api/lang/" + encodeURIComponent(locale));
    if (!response.ok) throw new Error("language not found");
    const data = await response.json();
    dictionary = Object.fromEntries(
      Object.entries(data).filter(([, v]) => typeof v === "string"),
    );
  } catch {
    setLocale("en");
  }
}
