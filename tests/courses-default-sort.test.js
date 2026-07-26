/* jshint esversion: 6, node: true */
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

const fakeWorker = String.raw`
"use strict";

const entry = {
  id: "course-content",
  title: "Курс с метаданными",
  author: "Автор",
  year: 2026,
  last_added_at: "2026-07-27T00:00:00Z",
  categories: ["development"],
  formats: ["video"],
  links: [{
    url: "https://files.test/course",
    host: "files.test",
    provider: "files",
    kind: "file_host",
    role: "primary",
    primary: true,
    label: null,
    content: {
      name: "Курс <img src=x onerror=alert(1)>",
      kind: "folder",
      size_bytes: 1610612736,
      file_count: 7,
      folder_count: 2,
      items: [
        { name: "intro.mp4", kind: "file", size_bytes: 1 },
        { name: "урок 2.mkv", kind: "file", size_bytes: 2 },
        { name: "notes.pdf", kind: "file", size_bytes: 3 },
        { name: "bonus.zip", kind: "file", size_bytes: 4 },
        { name: "cover.jpg", kind: "file", size_bytes: 5 },
      ],
      material_types: ["archive", "video"],
    },
  }],
  passwords: [],
  notes: [],
};

self.addEventListener("message", (event) => {
  const message = event.data || {};
  let data;
  if (message.type === "boot") {
    data = {
      cached: true,
      meta: { entry_count: 1, version: "fixture-v1" },
      facets: {
        formats: [],
        categories: [],
        providers: [],
        years: [],
        hasPassword: { withPassword: 0, withoutPassword: 0 },
      },
    };
  } else if (message.type === "search") {
    data = { entries: [entry], total: 1 };
  } else if (message.type === "get") {
    data = { entry };
  } else {
    data = {};
  }
  self.postMessage({ requestId: message.requestId, type: message.type, data });
});
`;

const testDriver = String.raw`
<script>
"use strict";

const NativeWorker = window.Worker;
window.__searchRequests = [];
window.Worker = function Worker(url, options) {
  const worker = new NativeWorker(url, options);
  const postMessage = worker.postMessage.bind(worker);
  worker.postMessage = (message, transfer) => {
    if (message && message.type === "search") {
      window.__searchRequests.push(structuredClone(message.data));
    }
    postMessage(message, transfer);
  };
  return worker;
};
window.Worker.prototype = NativeWorker.prototype;

function expect(condition, message) {
  if (!condition) throw new Error(message);
}

async function waitForRequests(count) {
  const deadline = Date.now() + 5000;
  while (window.__searchRequests.length < count) {
    if (Date.now() >= deadline) {
      throw new Error("timed out waiting for search request " + count);
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  await new Promise((resolve) => setTimeout(resolve, 20));
}

async function waitFor(check, description) {
  const deadline = Date.now() + 5000;
  while (!check()) {
    if (Date.now() >= deadline) {
      throw new Error("timed out waiting for " + description);
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

function expectRequest(index, query, field, direction) {
  const request = window.__searchRequests[index];
  expect(request.query === query, "request " + index + " query was " + request.query);
  expect(request.sort.field === field, "request " + index + " sort was " + request.sort.field);
  expect(
    request.sort.direction === direction,
    "request " + index + " direction was " + request.sort.direction,
  );
}

window.addEventListener("DOMContentLoaded", async () => {
  try {
    await waitForRequests(1);
    const search = document.querySelector("#search-input");
    const sort = document.querySelector("#sort-select");
    const direction = document.querySelector("#sort-direction");

    expectRequest(0, "", "added_at", "desc");
    expect(sort.value === "added_at", "default sort control did not show added_at");
    expect(direction.querySelector("span").textContent === "↓", "default direction was not descending");

    await waitFor(() => document.querySelector(".result-select"), "initial result");
    document.querySelector(".result-select").click();
    await waitFor(
      () => document.querySelectorAll(".link-content-summary").length === 2,
      "content summaries",
    );
    const summaries = [
      document.querySelector("#desktop-detail .link-content-summary"),
      document.querySelector("#mobile-detail .link-content-summary"),
    ];
    const expectedSummary = "Курс <img src=x onerror=alert(1)> · 1,5 ГБ · 7 файлов · 2 папки · архив, видео · intro.mp4, урок 2.mkv, notes.pdf +2";
    expect(
      summaries.every((summary) => summary && summary.textContent === expectedSummary),
      "desktop/mobile content summaries differ or are incomplete",
    );
    expect(
      summaries.every((summary) => summary.querySelector("img") === null),
      "content metadata was interpreted as HTML",
    );

    search.value = "python";
    search.dispatchEvent(new Event("input", { bubbles: true }));
    await waitForRequests(2);
    expectRequest(1, "python", "relevance", "desc");
    expect(sort.value === "relevance", "query sort control did not show relevance");

    sort.value = "year";
    sort.dispatchEvent(new Event("change", { bubbles: true }));
    await waitForRequests(3);
    expectRequest(2, "python", "year", "desc");

    search.value = "design";
    search.dispatchEvent(new Event("input", { bubbles: true }));
    await waitForRequests(4);
    expectRequest(3, "design", "year", "desc");

    direction.click();
    await waitForRequests(5);
    expectRequest(4, "design", "year", "asc");

    search.value = "";
    search.dispatchEvent(new Event("input", { bubbles: true }));
    await waitForRequests(6);
    expectRequest(5, "", "year", "asc");

    if (window.innerWidth < 960) {
      const filtersButton = document.querySelector("#filters-button");
      const filtersDialog = document.querySelector("#filters-dialog");
      const detailDialog = document.querySelector("#detail-dialog");
      const resultButton = document.querySelector(".result-select");
      const applyFilters = document.querySelector("#apply-filters");
      const providerSelect = document.querySelector("#provider-select");
      const selectedBefore = resultButton.getAttribute("aria-current");
      let resultClicks = 0;
      resultButton.addEventListener("click", () => {
        resultClicks += 1;
      });

      if (detailDialog.open) detailDialog.close();
      filtersButton.click();
      await waitFor(() => filtersDialog.open, "filters dialog");

      const dialogBounds = filtersDialog.getBoundingClientRect();
      const resultBounds = resultButton.getBoundingClientRect();
      const outsideX = (resultBounds.left + resultBounds.right) / 2;
      const outsideY = (resultBounds.top + resultBounds.bottom) / 2;
      expect(
        outsideX < dialogBounds.left
          || outsideX > dialogBounds.right
          || outsideY < dialogBounds.top
          || outsideY > dialogBounds.bottom,
        "result center was not outside the filters panel",
      );
      const outsidePointerDown = new PointerEvent("pointerdown", {
        bubbles: true,
        cancelable: true,
        clientX: outsideX,
        clientY: outsideY,
        pointerId: 1,
        pointerType: "touch",
        isPrimary: true,
      });
      filtersDialog.dispatchEvent(outsidePointerDown);
      const openBeforeClick = filtersDialog.open;
      const clickTarget = openBeforeClick ? filtersDialog : resultButton;
      const outsideClick = new PointerEvent("click", {
        bubbles: true,
        cancelable: true,
        clientX: outsideX,
        clientY: outsideY,
        pointerId: 1,
        pointerType: "touch",
        isPrimary: true,
      });
      clickTarget.dispatchEvent(outsideClick);

      expect(resultClicks === 0, "outside pointer sequence clicked a result");
      expect(!detailDialog.open, "outside pointer sequence opened detail dialog");
      expect(openBeforeClick, "filters closed before the outside click");
      expect(!outsidePointerDown.defaultPrevented, "outside pointerdown was consumed");
      expect(outsideClick.defaultPrevented, "outside click was not consumed");
      expect(!filtersDialog.open, "outside click did not close filters");
      expect(
        resultButton.getAttribute("aria-current") === selectedBefore,
        "outside pointer sequence changed the selected course",
      );
      expect(document.activeElement === filtersButton, "focus did not return to filters button");

      filtersButton.click();
      await waitFor(() => filtersDialog.open, "reopened filters dialog");
      const insideBounds = filtersDialog.getBoundingClientRect();
      const inside = new PointerEvent("pointerdown", {
        bubbles: true,
        cancelable: true,
        clientX: insideBounds.left + 20,
        clientY: insideBounds.top + 20,
        pointerId: 2,
        pointerType: "touch",
        isPrimary: true,
      });
      providerSelect.dispatchEvent(inside);
      expect(!inside.defaultPrevented, "inside pointerdown was consumed");
      expect(filtersDialog.open, "inside pointerdown closed filters");

      const insideClick = new PointerEvent("click", {
        bubbles: true,
        cancelable: true,
        clientX: 0,
        clientY: 0,
        pointerId: 2,
        pointerType: "touch",
        isPrimary: true,
      });
      providerSelect.dispatchEvent(insideClick);
      expect(!insideClick.defaultPrevented, "inside click was consumed");
      expect(filtersDialog.open, "inside click closed filters");

      let applyDefaultPrevented;
      applyFilters.addEventListener("click", (event) => {
        applyDefaultPrevented = event.defaultPrevented;
      }, { once: true });
      applyFilters.click();
      expect(applyDefaultPrevented === false, "apply click was consumed before its handler");
      expect(!filtersDialog.open, "apply button did not close filters");
      expect(document.activeElement === filtersButton, "apply did not return focus");
    }

    await fetch("/__result?status=PASS");
  } catch (error) {
    await fetch("/__result?status=FAIL&message=" + encodeURIComponent(error.message));
  }
});
</script>
`;

async function startServer() {
  let resolveResult;
  let workerVersion = null;
  const result = new Promise((resolve) => {
    resolveResult = resolve;
  });
  const template = await readFile(
    path.join(repoRoot, "static", "templates", "courses.html"),
    "utf8",
  );
  const page = template
    .replaceAll("{{.AssetVersion}}", "test-asset-version")
    .replace("</body>", testDriver + "\n</body>");

  const server = http.createServer(async (request, response) => {
    const requestURL = new URL(request.url, "http://127.0.0.1");
    if (requestURL.pathname === "/__courses-sort-test") {
      response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      response.end(page);
      return;
    }
    if (requestURL.pathname === "/__result") {
      response.writeHead(204);
      response.end();
      resolveResult({
        status: requestURL.searchParams.get("status"),
        message: requestURL.searchParams.get("message"),
        workerVersion,
      });
      return;
    }
    if (requestURL.pathname === "/js/courses.js") {
      const body = await readFile(path.join(repoRoot, "static", "js", "courses.js"));
      response.writeHead(200, { "content-type": "text/javascript; charset=utf-8" });
      response.end(body);
      return;
    }
    if (requestURL.pathname === "/css/courses.css") {
      readFile(path.join(repoRoot, "static", "css", "courses.css")).then(function (body) {
        response.writeHead(200, { "content-type": "text/css; charset=utf-8" });
        response.end(body);
      });
      return;
    }
    if (requestURL.pathname === "/js/courses-search-worker.js") {
      workerVersion = requestURL.searchParams.get("v");
      response.writeHead(200, { "content-type": "text/javascript; charset=utf-8" });
      response.end(fakeWorker);
      return;
    }
    if (requestURL.pathname === "/courses/api/meta") {
      response.writeHead(200, { "content-type": "application/json" });
      response.end('{"available":false}');
      return;
    }
    response.writeHead(404);
    response.end();
  });

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  return { server, result };
}

function runChrome(url, profilePath, width, height) {
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
      "--window-size=" + width + "," + height,
      url,
    ],
    { stdio: "ignore" },
  );
}

async function runViewport(width, height) {
  const { server, result } = await startServer();
  const profilePath = await mkdtemp(path.join(os.tmpdir(), "courses-sort-chrome-"));
  let chrome;
  let timeout;
  try {
    const address = server.address();
    chrome = runChrome(
      "http://127.0.0.1:" + address.port + "/__courses-sort-test",
      profilePath,
      width,
      height,
    );
    const browserResult = await Promise.race([
      result,
      new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error("browser test timed out")), 15000);
      }),
    ]);
    assert.equal(browserResult.status, "PASS", browserResult.message);
    assert.equal(browserResult.workerVersion, "test-asset-version");
  } finally {
    clearTimeout(timeout);
    if (chrome && chrome.exitCode == null) chrome.kill("SIGKILL");
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    await rm(profilePath, { recursive: true, force: true });
  }
}

test("catalog sort defaults to newest and preserves explicit choices", { timeout: 45000 }, async (t) => {
  await t.test("desktop", () => runViewport(1440, 900));
  await t.test("mobile 390x844", () => runViewport(390, 844));
});
