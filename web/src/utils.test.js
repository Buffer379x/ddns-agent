import { test } from "node:test";
import assert from "node:assert/strict";
import { escapeHTML, hostname, families, filterRecords } from "./utils.js";
test("untrusted API strings cannot become HTML", () => {
  assert.equal(
    escapeHTML('<img src=x onerror="x">&\''),
    "&lt;img src=x onerror=&quot;x&quot;&gt;&amp;&#39;",
  );
});
test("paused and dual records remain distinct in search", () => {
  const records = [
    {
      domain: "example.net",
      owner: "@",
      provider: "duckdns",
      enabled: false,
      status: "success",
      ip_version: "dual",
    },
    {
      domain: "example.net",
      owner: "vpn",
      provider: "cloudflare",
      enabled: true,
      status: "error",
    },
  ];
  assert.equal(hostname(records[0]), "example.net");
  assert.deepEqual(families(records[0]), ["ipv4", "ipv6"]);
  assert.equal(filterRecords(records, "EXAMPLE", "paused").length, 1);
  assert.equal(
    filterRecords(records, "cloudflare", "error", true)[0].owner,
    "vpn",
  );
});
