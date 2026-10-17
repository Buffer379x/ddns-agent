export const escapeHTML = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (char) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        char
      ],
  );
export const hostname = (record) =>
  !record.owner || record.owner === "@"
    ? record.domain
    : `${record.owner}.${record.domain}`;
export const families = (record) =>
  record.ip_version === "dual" || record.ip_version === "both"
    ? ["ipv4", "ipv6"]
    : [record.ip_version];
export const recordState = (record) =>
  record.enabled ? record.status : "paused";
export function filterRecords(records, query, status, activeOnly = false) {
  query = query.toLocaleLowerCase().trim();
  return records
    .filter(
      (record) =>
        (!activeOnly || record.enabled) &&
        (status === "all" || recordState(record) === status) &&
        `${hostname(record)} ${record.provider}`
          .toLocaleLowerCase()
          .includes(query),
    )
    .sort((a, b) => hostname(a).localeCompare(hostname(b)));
}
