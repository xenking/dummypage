const assert = require("node:assert/strict");
const { spawn } = require("node:child_process");
const { mkdtemp, readFile, rm } = require("node:fs/promises");
const http = require("node:http");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const repoRoot = path.resolve(__dirname, "..");
const chromePath =
  process.env.CHROME_BIN ||
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

const testPage = String.raw`<!doctype html>
<meta charset="utf-8">
<pre id="result">RUNNING</pre>
<script>
"use strict";

function expect(condition, message) {
  if (!condition) throw new Error(message);
}

function createClient() {
  const worker = new Worker("/static/js/courses-search-worker.js");
  const pending = new Map();
  const events = [];
  let nextRequest = 1;

  worker.addEventListener("message", (event) => {
    const message = event.data;
    if (message && message.requestId != null && pending.has(message.requestId)) {
      const resolve = pending.get(message.requestId);
      pending.delete(message.requestId);
      resolve(message);
      return;
    }
    events.push(message);
  });

  return {
    events,
    request(type, data) {
      const requestId = "request-" + nextRequest++;
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          pending.delete(requestId);
          reject(new Error(type + " timed out"));
        }, 10000);
        pending.set(requestId, (message) => {
          clearTimeout(timer);
          resolve(message);
        });
        worker.postMessage({ requestId, type, data: data || {} });
      });
    },
    close() {
      worker.terminate();
    },
  };
}

function countStoredRecords(storeName) {
  return new Promise((resolve, reject) => {
    const openRequest = indexedDB.open("dummypage-courses", 1);
    openRequest.onerror = () => reject(openRequest.error);
    openRequest.onsuccess = () => {
      const database = openRequest.result;
      const transaction = database.transaction(storeName, "readonly");
      const countRequest = transaction.objectStore(storeName).count();
      countRequest.onerror = () => reject(countRequest.error);
      countRequest.onsuccess = () => {
        database.close();
        resolve(countRequest.result);
      };
    };
  });
}

const emptyFilters = {
  categories: [],
  formats: [],
  providers: [],
  years: [],
  hasPassword: null,
};

const catalog = {
  schema_version: "courses-catalog/v2",
  source_schema: "fixture/v1",
  exported_at: "2026-07-26T00:00:00Z",
  source: { title: "Fixture" },
  stats: { entries: 4, links: 4, passwords: 1 },
  categories: [
    { id: "it", label: "IT", count: 2 },
    { id: "design", label: "Design", count: 1 },
    { id: "service", label: "Service", count: 1, hidden: true },
  ],
  formats: [
    { id: "video", label: "Video", count: 3 },
    { id: "book", label: "Book", count: 1 },
  ],
  entries: [
    {
      id: "course-1",
      title: "Ёл\u200bка Pro",
      author: "Анна",
      year: 2024,
      year_range: null,
      first_added_at: "2020-01-01T00:00:00Z",
      last_added_at: "2026-01-04T00:00:00Z",
      origins: ["origin-marker-337"],
      availability: ["availability-marker-338"],
      categories: ["it"],
      primary_category: "it",
      formats: ["video"],
      primary_format: "video",
      format_sources: ["format-source-marker-339"],
      links: [{
        url: "https://alpha.test/link-marker-334",
        host: "alpha.test",
        provider: "alpha",
        kind: "file_host",
        role: "primary",
        primary: true,
        label: null,
      }],
      passwords: ["password-marker-335"],
      notes: ["note-marker-336"],
      sources: [{
        entry_id: "source-entry-marker-330",
        message_id: "fixture:message-marker-331",
        telegram_message_id: 9332,
        message_url: "https://telegram.test/message-marker-332",
        source_message_ids: ["fixture:source-marker-333"],
        added_at: "2019-08-17T00:00:00Z",
        origin: "origin-marker-337",
        availability: "availability-marker-338",
      }],
    },
    {
      id: "course-2",
      title: "Alpha Design",
      author: null,
      year: 2023,
      year_range: null,
      first_added_at: "2025-01-01T00:00:00Z",
      last_added_at: "2026-01-03T00:00:00Z",
      origins: ["text_block"],
      availability: ["download_link"],
      categories: ["design"],
      primary_category: "design",
      formats: ["book"],
      primary_format: "book",
      format_sources: ["title"],
      links: [{
        url: "https://beta.test/b",
        host: "beta.test",
        provider: "beta",
        kind: "file_host",
        role: "primary",
        primary: true,
        label: null,
      }],
      passwords: [],
      notes: ["Layouts"],
      sources: [{
        entry_id: "source-2",
        message_id: "fixture:2",
        telegram_message_id: 2,
        message_url: "https://telegram.test/2",
        source_message_ids: ["fixture:2"],
        added_at: "2025-01-01T00:00:00Z",
        origin: "text_block",
        availability: "download_link",
      }],
    },
    {
      id: "course-3",
      title: "Service record",
      author: null,
      year: 2025,
      year_range: null,
      first_added_at: "2026-01-05T00:00:00Z",
      last_added_at: "2026-01-05T00:00:00Z",
      origins: ["text_block"],
      availability: ["reference_only"],
      categories: ["service"],
      primary_category: "service",
      formats: ["video"],
      primary_format: "video",
      format_sources: ["title"],
      links: [{
        url: "https://hidden.test/c",
        host: "hidden.test",
        provider: "hidden",
        kind: "web",
        role: "primary",
        primary: true,
        label: null,
      }],
      passwords: [],
      notes: [],
      sources: [{
        entry_id: "source-3",
        message_id: "fixture:3",
        telegram_message_id: 3,
        message_url: "https://telegram.test/3",
        source_message_ids: ["fixture:3"],
        added_at: "2026-01-05T00:00:00Z",
        origin: "text_block",
        availability: "reference_only",
      }],
    },
    {
      id: "course-4",
      title: "Prefixation",
      author: "Boris",
      year: 2022,
      year_range: null,
      first_added_at: "2026-01-02T00:00:00Z",
      last_added_at: "2026-01-02T00:00:00Z",
      origins: ["text_block"],
      availability: ["external_link"],
      categories: ["it"],
      primary_category: "it",
      formats: ["video"],
      primary_format: "video",
      format_sources: ["title"],
      links: [{
        url: "https://alpha.test/d",
        host: "alpha.test",
        provider: "alpha",
        kind: "web",
        role: "primary",
        primary: true,
        label: null,
      }],
      passwords: [],
      notes: [],
      sources: [{
        entry_id: "source-4",
        message_id: "fixture:4",
        telegram_message_id: 4,
        message_url: "https://telegram.test/4",
        source_message_ids: ["fixture:4"],
        added_at: "2026-01-02T00:00:00Z",
        origin: "text_block",
        availability: "external_link",
      }],
    },
  ],
};

(async () => {
  let client = createClient();

  const emptyBoot = await client.request("boot");
  expect(emptyBoot.ok === true, "empty boot failed");
  expect(emptyBoot.data.cached === false, "empty boot reported cached data");

  const rejectedV1 = await client.request("import", {
    catalog: { ...catalog, schema_version: "courses-catalog/v1" },
    meta: { available: true, schema: "courses-catalog/v1", version: "fixture-legacy" },
    version: "fixture-legacy",
  });
  expect(
    rejectedV1.ok === false && rejectedV1.error.code === "INVALID_CATALOG",
    "v1 catalog was accepted",
  );

  const imported = await client.request("import", {
    catalog,
    meta: { available: true, schema: "courses-catalog/v2", version: "fixture-v2" },
    version: "fixture-v2",
  });
  expect(imported.ok === true, "import failed: " + JSON.stringify(imported.error || null));
  expect(imported.type === "import", "import response type missing");
  expect(imported.data.cached === true, "import did not report cached");
  expect(imported.data.meta.entry_count === 3, "service entry counted as visible");
  expect(imported.data.facets.categories.every((item) => item.value !== "service"), "service facet leaked");
  expect(imported.data.facets.formats.find((item) => item.value === "video").count === 2, "format facet incorrect");
  expect(imported.data.facets.hasPassword.withPassword === 1, "password facet incorrect");

  const normalized = await client.request("search", {
    query: "елка",
    filters: emptyFilters,
    sort: { field: "relevance", direction: "desc" },
    offset: 0,
    limit: 10,
  });
  expect(normalized.ok === true && normalized.data.entries[0].id === "course-1", "NFC/ё/zero-width normalization failed");

  for (const query of [
    "source entry marker 330",
    "message marker 331",
    "9332",
    "message marker 332",
    "source marker 333",
    "link marker 334",
    "password marker 335",
    "note marker 336",
    "origin marker 337",
    "availability marker 338",
    "format source marker 339",
    "2019 08 17",
  ]) {
    const allFields = await client.request("search", {
      query,
      filters: emptyFilters,
      sort: { field: "relevance", direction: "desc" },
      offset: 0,
      limit: 10,
    });
    expect(
      allFields.data.total === 1 && allFields.data.entries[0].id === "course-1",
      "local field missing from search text: " + query,
    );
  }

  const prefix = await client.request("search", {
    query: "pref",
    filters: emptyFilters,
    sort: { field: "relevance", direction: "desc" },
    offset: 0,
    limit: 10,
  });
  expect(prefix.data.total === 1 && prefix.data.entries[0].id === "course-4", "prefix search failed");

  const fuzzy = await client.request("search", {
    query: "prefexation",
    filters: emptyFilters,
    sort: { field: "relevance", direction: "desc" },
    offset: 0,
    limit: 10,
  });
  expect(fuzzy.data.total === 1 && fuzzy.data.entries[0].id === "course-4", "long-term fuzzy fallback failed");

  const shortTypo = await client.request("search", {
    query: "alpa",
    filters: emptyFilters,
    sort: { field: "relevance", direction: "desc" },
    offset: 0,
    limit: 10,
  });
  expect(shortTypo.data.total === 0, "short term used fuzzy search");

  const filtered = await client.request("search", {
    query: "",
    filters: {
      categories: ["it", "design"],
      formats: ["book"],
      providers: ["beta"],
      years: [2023],
      hasPassword: false,
    },
    sort: { field: "year", direction: "desc" },
    offset: 0,
    limit: 10,
  });
  expect(filtered.data.total === 1 && filtered.data.entries[0].id === "course-2", "facet filters failed");

  const sorted = await client.request("search", {
    query: "",
    filters: emptyFilters,
    sort: { field: "year", direction: "desc" },
    offset: 0,
    limit: 2,
  });
  expect(sorted.data.total === 3, "service entry appeared in wildcard results");
  expect(sorted.data.entries.map((entry) => entry.id).join(",") === "course-1,course-2", "year sort or pagination failed");

  const addedAtSorted = await client.request("search", {
    query: "",
    filters: emptyFilters,
    sort: { field: "added_at", direction: "desc" },
    offset: 0,
    limit: 2,
  });
  expect(
    addedAtSorted.data.entries.map((entry) => entry.id).join(",") === "course-1,course-2",
    "added_at sort did not use last_added_at",
  );
  expect(
    addedAtSorted.data.entries[0].added_at === catalog.entries[0].last_added_at,
    "search result did not expose last_added_at as added_at",
  );

  const visibleGet = await client.request("get", { id: "course-1" });
  expect(
    visibleGet.ok === true
      && visibleGet.data.entry.added_at === catalog.entries[0].last_added_at,
    "get result did not expose last_added_at as added_at",
  );

  const hiddenGet = await client.request("get", { id: "course-3" });
  expect(hiddenGet.ok === true && hiddenGet.data.entry === null, "service entry returned from get");

  const invalidCatalog = { ...catalog, entries: [catalog.entries[0], catalog.entries[0]] };
  const invalidImport = await client.request("import", {
    catalog: invalidCatalog,
    meta: { version: "fixture-v2" },
    version: "fixture-v2",
  });
  expect(invalidImport.ok === false && invalidImport.error.code === "INVALID_CATALOG", "duplicate IDs accepted");

  const afterInvalid = await client.request("search", {
    query: "елка",
    filters: emptyFilters,
    sort: { field: "relevance", direction: "desc" },
    offset: 0,
    limit: 10,
  });
  expect(afterInvalid.data.entries[0].id === "course-1", "failed import replaced active data");
  expect(client.events.some((event) => event && event.type === "progress"), "progress events missing");
  expect(client.events.some((event) => event && event.type === "status"), "status events missing");

  client.close();
  client = createClient();

  const cachedBoot = await client.request("boot");
  expect(cachedBoot.ok === true && cachedBoot.data.cached === true, "cached boot failed");
  expect(cachedBoot.data.meta.version === "fixture-v2", "wrong active cached version");

  const updatedCatalog = {
    ...catalog,
    exported_at: "2026-07-27T00:00:00Z",
    entries: catalog.entries.map((entry) => ({ ...entry })),
  };
  const secondImport = await client.request("import", {
    catalog: updatedCatalog,
    meta: { available: true, schema: "courses-catalog/v2", version: "fixture-v3" },
    version: "fixture-v3",
  });
  expect(secondImport.ok === true, "second import failed");
  expect(await countStoredRecords("catalogs") === 1, "inactive catalogs were not pruned");
  expect(await countStoredRecords("indexes") === 1, "inactive indexes were not pruned");
  client.close();
  client = createClient();

  const updatedBoot = await client.request("boot");
  expect(updatedBoot.ok === true && updatedBoot.data.meta.version === "fixture-v3", "updated active version was not persisted");

  const forgetRequest = client.request("forget");
  const getDuringForgetRequest = client.request("get", { id: "course-1" });
  const [forgotten, getDuringForget] = await Promise.all([
    forgetRequest,
    getDuringForgetRequest,
  ]);
  expect(forgotten.ok === true && forgotten.data.cleared === true, "forget failed");
  expect(
    getDuringForget.ok === false && getDuringForget.error.code === "NOT_READY",
    "read raced forget and returned stale catalog data",
  );
  client.close();
  client = createClient();

  const forgottenBoot = await client.request("boot");
  expect(forgottenBoot.ok === true && forgottenBoot.data.cached === false, "forget left cached data");
  client.close();

  document.getElementById("result").textContent = "PASS";
  await fetch("/__result?status=PASS");
})().catch((error) => {
  document.getElementById("result").textContent = "FAIL: " + error.message;
  fetch("/__result?status=FAIL&message=" + encodeURIComponent(error.message));
});
</script>`;

function startServer() {
  let resolveResult;
  const result = new Promise((resolve) => {
    resolveResult = resolve;
  });
  const server = http.createServer(async (request, response) => {
    try {
      const requestURL = new URL(request.url, "http://127.0.0.1");
      const pathname = requestURL.pathname;
      if (pathname === "/__courses-worker-test") {
        response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
        response.end(testPage);
        return;
      }
      if (pathname === "/__result") {
        response.writeHead(204);
        response.end();
        resolveResult({
          status: requestURL.searchParams.get("status"),
          message: requestURL.searchParams.get("message"),
        });
        return;
      }

      if (!pathname.startsWith("/static/js/")) {
        response.writeHead(404);
        response.end();
        return;
      }

      const filePath = path.resolve(repoRoot, pathname.slice(1));
      if (!filePath.startsWith(path.join(repoRoot, "static", "js") + path.sep)) {
        response.writeHead(403);
        response.end();
        return;
      }
      const body = await readFile(filePath);
      response.writeHead(200, { "content-type": "text/javascript; charset=utf-8" });
      response.end(body);
    } catch {
      response.writeHead(404);
      response.end();
    }
  });

  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve({ server, result }));
  });
}

function runChrome(url, profilePath) {
  return spawn(
    chromePath,
    [
      "--headless=new",
      "--disable-background-networking",
      "--disable-component-update",
      "--disable-gpu",
      "--no-first-run",
      "--no-default-browser-check",
      "--user-data-dir=" + profilePath,
      url,
    ],
    { stdio: "ignore" },
  );
}

test("course worker persists and searches the local catalog", { timeout: 60000 }, async () => {
  const { server, result } = await startServer();
  const profilePath = await mkdtemp(path.join(os.tmpdir(), "courses-worker-chrome-"));
  let chrome;
  let timeout;
  try {
    const address = server.address();
    chrome = runChrome(
      "http://127.0.0.1:" + address.port + "/__courses-worker-test",
      profilePath,
    );
    const browserResult = await Promise.race([
      result,
      new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error("browser test timed out")), 30000);
      }),
    ]);
    assert.equal(browserResult.status, "PASS", browserResult.message);
  } finally {
    clearTimeout(timeout);
    if (chrome && chrome.exitCode == null) chrome.kill("SIGKILL");
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    await rm(profilePath, { recursive: true, force: true });
  }
});
