"use strict";

importScripts("./vendor/minisearch.min.js");

const DB_NAME = "dummypage-courses";
const DB_VERSION = 1;
const STATE_STORE = "state";
const CATALOG_STORE = "catalogs";
const INDEX_STORE = "indexes";
const ACTIVE_VERSION_KEY = "activeVersion";
const CATALOG_SCHEMA = "courses-catalog/v2";
const INDEX_BATCH_SIZE = 500;
const MAX_RESULT_LIMIT = 500;
const ZERO_WIDTH_PATTERN = /[\u200b-\u200d\u2060\ufeff]/g;
const SPACE_PATTERN = /\s+/g;
const MUTATING_REQUESTS = new Set(["boot", "import", "forget"]);
const SORT_FIELDS = new Set(["relevance", "title", "author", "year", "added_at"]);
const collator = new Intl.Collator(["ru", "en"], {
  sensitivity: "base",
  numeric: true,
});

const INDEX_OPTIONS = Object.freeze({
  fields: ["title", "author", "search_text"],
  idField: "id",
  storeFields: [],
  processTerm: normalizeTerm,
});

let databasePromise;
let activeRuntime = null;
let mutationQueue = Promise.resolve();

class WorkerError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "WorkerError";
    this.code = code;
  }
}

function normalizeText(value) {
  return String(value)
    .normalize("NFC")
    .toLowerCase()
    .replaceAll("ё", "е")
    .replace(ZERO_WIDTH_PATTERN, "")
    .replace(SPACE_PATTERN, " ")
    .trim();
}

function normalizeTerm(term) {
  return normalizeText(term) || null;
}

function emptyFacets() {
  return {
    categories: [],
    formats: [],
    providers: [],
    years: [],
    hasPassword: {
      withPassword: 0,
      withoutPassword: 0,
    },
  };
}

function postProgress(phase, current, total) {
  self.postMessage({ type: "progress", phase, current, total });
}

function postStatus(status, details) {
  self.postMessage(Object.assign({ type: "status", status }, details || {}));
}

function invalidCatalog(message) {
  throw new WorkerError("INVALID_CATALOG", message);
}

function invalidRequest(message) {
  throw new WorkerError("INVALID_REQUEST", message);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function requireString(value, description, allowEmpty = false) {
  if (typeof value !== "string" || (!allowEmpty && value.trim() === "")) {
    invalidCatalog(description + " must be a non-empty string");
  }
}

function validateDefinitions(definitions, name, required) {
  if (definitions == null && !required) return;
  if (!Array.isArray(definitions)) invalidCatalog(name + " must be an array");

  const ids = new Set();
  for (let index = 0; index < definitions.length; index += 1) {
    const definition = definitions[index];
    if (!isObject(definition)) invalidCatalog(name + "[" + index + "] must be an object");
    requireString(definition.id, name + "[" + index + "].id");
    requireString(definition.label, name + "[" + index + "].label");
    const id = normalizeText(definition.id);
    if (ids.has(id)) invalidCatalog(name + " contains duplicate IDs");
    ids.add(id);
    if (definition.count != null && (!Number.isInteger(definition.count) || definition.count < 0)) {
      invalidCatalog(name + "[" + index + "].count must be a non-negative integer");
    }
    if (definition.hidden != null && typeof definition.hidden !== "boolean") {
      invalidCatalog(name + "[" + index + "].hidden must be a boolean");
    }
  }
}

function validateStringList(value, description, required, nonEmpty = false) {
  if (value == null && !required) return;
  if (!Array.isArray(value)) invalidCatalog(description + " must be an array");
  if (nonEmpty && value.length === 0) invalidCatalog(description + " must not be empty");
  for (let index = 0; index < value.length; index += 1) {
    requireString(value[index], description + "[" + index + "]");
  }
}

function validateTimestamp(value, description) {
  requireString(value, description);
  if (Number.isNaN(Date.parse(value))) invalidCatalog(description + " must be a valid timestamp");
}

function validateLink(link, description) {
  if (!isObject(link)) invalidCatalog(description + " must be an object");
  requireString(link.url, description + ".url");
  if (typeof link.host !== "string") invalidCatalog(description + ".host must be a string");
  requireString(link.provider, description + ".provider");
  requireString(link.kind, description + ".kind");
  requireString(link.role, description + ".role");
  if (typeof link.primary !== "boolean") invalidCatalog(description + ".primary must be a boolean");
  if (link.label != null && typeof link.label !== "string") {
    invalidCatalog(description + ".label must be a string or null");
  }
}

function validateSource(source, description) {
  if (!isObject(source)) invalidCatalog(description + " must be an object");
  requireString(source.entry_id, description + ".entry_id");
  requireString(source.message_id, description + ".message_id");
  if (!Number.isInteger(source.telegram_message_id)) {
    invalidCatalog(description + ".telegram_message_id must be an integer");
  }
  requireString(source.message_url, description + ".message_url");
  validateStringList(
    source.source_message_ids,
    description + ".source_message_ids",
    true,
    true,
  );
  validateTimestamp(source.added_at, description + ".added_at");
  requireString(source.origin, description + ".origin");
  requireString(source.availability, description + ".availability");
}

function validateEntry(entry, index, ids) {
  const prefix = "entries[" + index + "]";
  if (!isObject(entry)) invalidCatalog(prefix + " must be an object");
  requireString(entry.id, prefix + ".id");
  requireString(entry.title, prefix + ".title");

  if (ids.has(entry.id)) invalidCatalog("entries contains duplicate IDs");
  ids.add(entry.id);

  if (entry.author != null && typeof entry.author !== "string") {
    invalidCatalog(prefix + ".author must be a string or null");
  }
  if (entry.year != null && !Number.isInteger(entry.year)) {
    invalidCatalog(prefix + ".year must be an integer or null");
  }
  if (entry.year_range != null) {
    if (
      !isObject(entry.year_range) ||
      !Number.isInteger(entry.year_range.from) ||
      !Number.isInteger(entry.year_range.to)
    ) {
      invalidCatalog(prefix + ".year_range must contain integer from/to values");
    }
  }

  validateTimestamp(entry.first_added_at, prefix + ".first_added_at");
  validateTimestamp(entry.last_added_at, prefix + ".last_added_at");
  validateStringList(entry.origins, prefix + ".origins", true, true);
  validateStringList(entry.availability, prefix + ".availability", true, true);
  validateStringList(entry.categories, prefix + ".categories", true, true);
  requireString(entry.primary_category, prefix + ".primary_category");
  if (!entry.categories.includes(entry.primary_category)) {
    invalidCatalog(prefix + ".primary_category must be present in categories");
  }
  validateStringList(entry.formats, prefix + ".formats", true, true);
  requireString(entry.primary_format, prefix + ".primary_format");
  if (!entry.formats.includes(entry.primary_format)) {
    invalidCatalog(prefix + ".primary_format must be present in formats");
  }
  validateStringList(entry.format_sources, prefix + ".format_sources", true, true);
  validateStringList(entry.passwords, prefix + ".passwords", true);
  validateStringList(entry.notes, prefix + ".notes", true);

  if (!Array.isArray(entry.links)) invalidCatalog(prefix + ".links must be an array");
  for (let linkIndex = 0; linkIndex < entry.links.length; linkIndex += 1) {
    validateLink(entry.links[linkIndex], prefix + ".links[" + linkIndex + "]");
  }

  if (!Array.isArray(entry.sources) || entry.sources.length === 0) {
    invalidCatalog(prefix + ".sources must be a non-empty array");
  }
  for (let sourceIndex = 0; sourceIndex < entry.sources.length; sourceIndex += 1) {
    validateSource(entry.sources[sourceIndex], prefix + ".sources[" + sourceIndex + "]");
  }
}

function validateCatalog(catalog, phase) {
  if (!isObject(catalog)) invalidCatalog("catalog must be an object");
  if (catalog.schema_version !== CATALOG_SCHEMA) {
    invalidCatalog('schema_version must be "' + CATALOG_SCHEMA + '"');
  }
  if (!Array.isArray(catalog.entries) || catalog.entries.length === 0) {
    invalidCatalog("entries must be a non-empty array");
  }
  validateDefinitions(catalog.categories, "categories", true);
  validateDefinitions(catalog.formats, "formats", true);

  const ids = new Set();
  for (let index = 0; index < catalog.entries.length; index += 1) {
    validateEntry(catalog.entries[index], index, ids);
    if ((index + 1) % 1000 === 0) {
      postProgress(phase, index + 1, catalog.entries.length);
    }
  }
  postProgress(phase, catalog.entries.length, catalog.entries.length);
}

function createDefinitionLookup(definitions) {
  const lookup = new Map();
  const hidden = new Set();
  for (const definition of definitions || []) {
    const key = normalizeText(definition.id);
    lookup.set(key, {
      value: definition.id.trim(),
      label: definition.label.trim(),
    });
    if (definition.hidden) hidden.add(key);
  }
  return { lookup, hidden };
}

function isServiceEntry(entry) {
  if (normalizeText(entry.primary_category) === "service") return true;
  return entry.categories.some((category) => normalizeText(category) === "service");
}

function addProvider(providers, value, label) {
  if (typeof value !== "string" || value.trim() === "") return;
  const key = normalizeText(value);
  if (!providers.has(key)) {
    providers.set(key, {
      value: key,
      label: typeof label === "string" && label.trim() !== "" ? label.trim() : value.trim(),
    });
  }
}

function deriveProviders(entry) {
  const providers = new Map();
  for (const link of entry.links) {
    let host = typeof link.host === "string" ? link.host.trim() : "";
    if (host === "") {
      try {
        host = new URL(link.url).hostname;
      } catch {
        host = "";
      }
    }
    if (typeof link.provider === "string" && link.provider.trim() !== "") {
      addProvider(providers, link.provider, host || link.provider);
    } else {
      addProvider(providers, host, host);
    }
  }
  return providers;
}

function parseAddedAt(value) {
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? null : parsed;
}

function buildSearchText(entry, derived, categoryDefinitions, formatDefinitions) {
  const parts = [
    entry.id,
    entry.title,
    entry.author,
    entry.year,
    entry.year_range && entry.year_range.from,
    entry.year_range && entry.year_range.to,
    entry.first_added_at,
    entry.last_added_at,
    ...entry.origins,
    ...entry.availability,
    ...entry.categories,
    entry.primary_category,
    ...entry.formats,
    entry.primary_format,
    ...entry.format_sources,
    ...entry.passwords,
    ...entry.notes,
  ];

  for (const category of derived.categories) {
    const definition = categoryDefinitions.lookup.get(category);
    parts.push(definition ? definition.label : category);
  }
  for (const format of derived.formats) {
    const definition = formatDefinitions.lookup.get(format);
    parts.push(definition ? definition.label : format);
  }
  for (const provider of derived.providers.values()) {
    parts.push(provider.value, provider.label);
  }
  for (const link of entry.links) {
    parts.push(
      link.url,
      link.label,
      link.host,
      link.provider,
      link.kind,
      link.role,
    );
  }
  for (const source of entry.sources) {
    parts.push(
      source.entry_id,
      source.message_id,
      source.telegram_message_id,
      source.message_url,
      ...source.source_message_ids,
      source.added_at,
      source.origin,
      source.availability,
    );
  }

  return parts
    .filter((part) => part !== null && part !== undefined && String(part).trim() !== "")
    .map((part) => String(part).trim())
    .join(" ");
}

function incrementCount(counts, key) {
  counts.set(key, (counts.get(key) || 0) + 1);
}

function buildFacetRecords(counts, definitions, labels) {
  const records = [];
  for (const [key, count] of counts) {
    if (definitions.hidden.has(key)) continue;
    const definition = definitions.lookup.get(key);
    const providerLabel = labels && labels.get(key);
    records.push({
      value: definition ? definition.value : key,
      label: definition ? definition.label : providerLabel || key,
      count,
    });
  }
  records.sort((left, right) => collator.compare(left.label, right.label));
  return records;
}

function hydrateCatalog(catalog, phase) {
  const categoryDefinitions = createDefinitionLookup(catalog.categories);
  const formatDefinitions = createDefinitionLookup(catalog.formats);
  const visibleEntries = [];
  const byId = new Map();
  const derivedById = new Map();
  const categoryCounts = new Map();
  const formatCounts = new Map();
  const providerCounts = new Map();
  const providerLabels = new Map();
  const yearCounts = new Map();
  let withPassword = 0;

  for (let index = 0; index < catalog.entries.length; index += 1) {
    const entry = catalog.entries[index];
    if (isServiceEntry(entry)) continue;
    const externalEntry = Object.assign({}, entry, {
      added_at: entry.last_added_at,
    });

    const categories = new Set(entry.categories.map(normalizeText));
    const formats = new Set(entry.formats.map(normalizeText));
    const providers = deriveProviders(entry);
    const derived = {
      categories,
      formats,
      providers,
      hasPassword: entry.passwords.length > 0,
      title: normalizeText(entry.title),
      author: normalizeText(entry.author || ""),
      year: entry.year == null ? null : entry.year,
      addedAt: parseAddedAt(entry.last_added_at),
    };

    visibleEntries.push(externalEntry);
    byId.set(entry.id, externalEntry);
    derivedById.set(entry.id, derived);
    for (const category of categories) incrementCount(categoryCounts, category);
    for (const format of formats) incrementCount(formatCounts, format);
    for (const [provider, metadata] of providers) {
      incrementCount(providerCounts, provider);
      if (!providerLabels.has(provider)) providerLabels.set(provider, metadata.label);
    }
    if (entry.year != null) incrementCount(yearCounts, entry.year);
    if (derived.hasPassword) withPassword += 1;

    if ((index + 1) % 1000 === 0) {
      postProgress(phase, index + 1, catalog.entries.length);
    }
  }
  postProgress(phase, catalog.entries.length, catalog.entries.length);

  const years = Array.from(yearCounts, ([value, count]) => ({ value, count }));
  years.sort((left, right) => right.value - left.value);

  return {
    catalog,
    visibleEntries,
    byId,
    derivedById,
    categoryDefinitions,
    formatDefinitions,
    facets: {
      categories: buildFacetRecords(categoryCounts, categoryDefinitions),
      formats: buildFacetRecords(formatCounts, formatDefinitions),
      providers: buildFacetRecords(
        providerCounts,
        { lookup: new Map(), hidden: new Set() },
        providerLabels,
      ),
      years,
      hasPassword: {
        withPassword,
        withoutPassword: visibleEntries.length - withPassword,
      },
    },
  };
}

function yieldToWorker() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function buildIndex(runtime) {
  const index = new MiniSearch(INDEX_OPTIONS);
  const entries = runtime.visibleEntries;
  for (let offset = 0; offset < entries.length; offset += INDEX_BATCH_SIZE) {
    const batch = entries
      .slice(offset, offset + INDEX_BATCH_SIZE)
      .map((entry) => {
        const derived = runtime.derivedById.get(entry.id);
        return {
          id: entry.id,
          title: entry.title.trim(),
          author: (entry.author || "").trim(),
          search_text: buildSearchText(
            entry,
            derived,
            runtime.categoryDefinitions,
            runtime.formatDefinitions,
          ),
        };
      });
    index.addAll(batch);
    postProgress("import:index", Math.min(offset + batch.length, entries.length), entries.length);
    await yieldToWorker();
  }
  return index;
}

function requestToPromise(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("IndexedDB request failed"));
  });
}

function transactionToPromise(transaction) {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error || new Error("IndexedDB transaction failed"));
    transaction.onabort = () => reject(transaction.error || new Error("IndexedDB transaction aborted"));
  });
}

function openDatabase() {
  if (databasePromise) return databasePromise;
  if (typeof indexedDB === "undefined") {
    throw new WorkerError("STORAGE_UNAVAILABLE", "IndexedDB is unavailable");
  }

  databasePromise = new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => {
      const database = request.result;
      if (!database.objectStoreNames.contains(STATE_STORE)) {
        database.createObjectStore(STATE_STORE, { keyPath: "key" });
      }
      if (!database.objectStoreNames.contains(CATALOG_STORE)) {
        database.createObjectStore(CATALOG_STORE, { keyPath: "version" });
      }
      if (!database.objectStoreNames.contains(INDEX_STORE)) {
        database.createObjectStore(INDEX_STORE, { keyPath: "version" });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("open IndexedDB failed"));
    request.onblocked = () => reject(new WorkerError("STORAGE_BLOCKED", "IndexedDB upgrade is blocked"));
  }).catch((error) => {
    databasePromise = undefined;
    throw error;
  });
  return databasePromise;
}

async function readActiveRecords(database) {
  const stateTransaction = database.transaction(STATE_STORE, "readonly");
  const stateRequest = stateTransaction.objectStore(STATE_STORE).get(ACTIVE_VERSION_KEY);
  const stateDone = transactionToPromise(stateTransaction);
  const state = await requestToPromise(stateRequest);
  await stateDone;
  if (!state || typeof state.version !== "string") return null;

  const dataTransaction = database.transaction([CATALOG_STORE, INDEX_STORE], "readonly");
  const catalogRequest = dataTransaction.objectStore(CATALOG_STORE).get(state.version);
  const indexRequest = dataTransaction.objectStore(INDEX_STORE).get(state.version);
  const dataDone = transactionToPromise(dataTransaction);
  const [catalogRecord, indexRecord] = await Promise.all([
    requestToPromise(catalogRequest),
    requestToPromise(indexRequest),
  ]);
  await dataDone;
  if (!catalogRecord || !indexRecord) {
    throw new WorkerError("CACHE_CORRUPT", "active catalog or index is missing");
  }
  return { catalogRecord, indexRecord };
}

async function persistVersion(database, version, catalog, meta, facets, serializedIndex) {
  const transaction = database.transaction(
    [CATALOG_STORE, INDEX_STORE, STATE_STORE],
    "readwrite",
  );
  const completion = transactionToPromise(transaction);
  const storedAt = new Date().toISOString();
  transaction.objectStore(CATALOG_STORE).put({
    version,
    catalog,
    meta,
    facets,
    storedAt,
  });
  transaction.objectStore(INDEX_STORE).put({
    version,
    serializedIndex,
    storedAt,
  });
  transaction.objectStore(STATE_STORE).put({
    key: ACTIVE_VERSION_KEY,
    version,
  });
  await completion;
}

function deleteInactiveRecords(store, activeVersion) {
  const request = store.openKeyCursor();
  request.onsuccess = () => {
    const cursor = request.result;
    if (!cursor) return;
    if (cursor.primaryKey !== activeVersion) store.delete(cursor.primaryKey);
    cursor.continue();
  };
}

async function pruneInactiveVersions(database, activeVersion) {
  const transaction = database.transaction([CATALOG_STORE, INDEX_STORE], "readwrite");
  const completion = transactionToPromise(transaction);
  deleteInactiveRecords(transaction.objectStore(CATALOG_STORE), activeVersion);
  deleteInactiveRecords(transaction.objectStore(INDEX_STORE), activeVersion);
  await completion;
}

function normalizeVersion(payload) {
  const candidate =
    payload.version != null
      ? payload.version
      : isObject(payload.meta)
        ? payload.meta.version
        : null;
  if (typeof candidate !== "string" || candidate.trim() === "") {
    invalidRequest("import version must be a non-empty string");
  }
  if (candidate.length > 512) invalidRequest("import version is too long");
  return candidate.trim();
}

function buildMeta(catalog, suppliedMeta, version, entryCount) {
  const source = isObject(suppliedMeta) ? suppliedMeta : {};
  return {
    available: true,
    schema: catalog.schema_version,
    version,
    bytes: Number.isInteger(source.bytes) ? source.bytes : undefined,
    updated_at: typeof source.updated_at === "string" ? source.updated_at : undefined,
    source_schema:
      typeof catalog.source_schema === "string" ? catalog.source_schema : undefined,
    exported_at: typeof catalog.exported_at === "string" ? catalog.exported_at : undefined,
    source: isObject(catalog.source) ? catalog.source : undefined,
    stats: isObject(catalog.stats) ? catalog.stats : undefined,
    entry_count: entryCount,
    imported_at: new Date().toISOString(),
  };
}

async function handleBoot() {
  postStatus("loading", { cached: false });
  postProgress("boot:open", 0, 1);
  const database = await openDatabase();
  postProgress("boot:open", 1, 1);
  const records = await readActiveRecords(database);
  if (!records) {
    activeRuntime = null;
    const facets = emptyFacets();
    postStatus("empty", { cached: false });
    return { cached: false, meta: null, facets };
  }

  const { catalogRecord, indexRecord } = records;
  validateCatalog(catalogRecord.catalog, "boot:validate");
  const runtime = hydrateCatalog(catalogRecord.catalog, "boot:hydrate");
  postProgress("boot:index", 0, 1);
  let index;
  try {
    index = MiniSearch.loadJSON(indexRecord.serializedIndex, INDEX_OPTIONS);
  } catch {
    throw new WorkerError("CACHE_CORRUPT", "cached search index cannot be loaded");
  }
  if (index.documentCount !== runtime.visibleEntries.length) {
    throw new WorkerError("CACHE_CORRUPT", "cached search index is incomplete");
  }
  runtime.index = index;
  runtime.meta = catalogRecord.meta;
  activeRuntime = runtime;
  postProgress("boot:index", 1, 1);
  try {
    await pruneInactiveVersions(database, catalogRecord.version);
  } catch {
    postStatus("storage-warning", { cached: true });
  }
  postStatus("ready", { cached: true, meta: runtime.meta });
  return { cached: true, meta: runtime.meta, facets: runtime.facets };
}

async function handleImport(payload) {
  const version = normalizeVersion(payload);
  postStatus("importing", { version });
  validateCatalog(payload.catalog, "import:validate");
  const runtime = hydrateCatalog(payload.catalog, "import:hydrate");
  const index = await buildIndex(runtime);

  postProgress("import:serialize", 0, 1);
  const serializedIndex = JSON.stringify(index);
  postProgress("import:serialize", 1, 1);
  const meta = buildMeta(payload.catalog, payload.meta, version, runtime.visibleEntries.length);

  postProgress("import:persist", 0, 1);
  const database = await openDatabase();
  await persistVersion(
    database,
    version,
    payload.catalog,
    meta,
    runtime.facets,
    serializedIndex,
  );
  postProgress("import:persist", 1, 1);

  runtime.index = index;
  runtime.meta = meta;
  activeRuntime = runtime;
  postProgress("import:prune", 0, 1);
  try {
    await pruneInactiveVersions(database, version);
  } catch {
    postStatus("storage-warning", { cached: true });
  }
  postProgress("import:prune", 1, 1);
  postStatus("ready", { cached: true, meta });
  return { cached: true, meta, facets: runtime.facets };
}

function requestStringArray(value, name) {
  if (value == null) return [];
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string")) {
    invalidRequest(name + " must be an array of strings");
  }
  return value.map(normalizeText).filter(Boolean);
}

function requestYears(value) {
  if (value == null) return [];
  if (!Array.isArray(value)) invalidRequest("filters.years must be an array");
  return value.map((year) => {
    const parsed = typeof year === "string" && year.trim() !== "" ? Number(year) : year;
    if (!Number.isInteger(parsed)) invalidRequest("filters.years must contain integers");
    return parsed;
  });
}

function normalizeFilters(filters) {
  const source = filters == null ? {} : filters;
  if (!isObject(source)) invalidRequest("filters must be an object");
  if (
    source.hasPassword !== undefined &&
    source.hasPassword !== null &&
    typeof source.hasPassword !== "boolean"
  ) {
    invalidRequest("filters.hasPassword must be true, false, or null");
  }
  return {
    categories: new Set(requestStringArray(source.categories, "filters.categories")),
    formats: new Set(requestStringArray(source.formats, "filters.formats")),
    providers: new Set(requestStringArray(source.providers, "filters.providers")),
    years: new Set(requestYears(source.years)),
    hasPassword: source.hasPassword == null ? null : source.hasPassword,
  };
}

function intersects(left, right) {
  if (right.size === 0) return true;
  for (const item of left) {
    if (right.has(item)) return true;
  }
  return false;
}

function matchesFilters(derived, filters) {
  if (!intersects(derived.categories, filters.categories)) return false;
  if (!intersects(derived.formats, filters.formats)) return false;
  if (!intersects(new Set(derived.providers.keys()), filters.providers)) return false;
  if (filters.years.size > 0 && !filters.years.has(derived.year)) return false;
  if (filters.hasPassword !== null && filters.hasPassword !== derived.hasPassword) return false;
  return true;
}

function normalizeSort(sort) {
  const source = sort == null ? {} : sort;
  if (!isObject(source)) invalidRequest("sort must be an object");
  const field = source.field == null ? "relevance" : source.field;
  const direction = source.direction == null ? "desc" : source.direction;
  if (!SORT_FIELDS.has(field)) invalidRequest("sort.field is unsupported");
  if (direction !== "asc" && direction !== "desc") {
    invalidRequest('sort.direction must be "asc" or "desc"');
  }
  return { field, direction };
}

function compareNullable(left, right, direction, compare) {
  const leftMissing = left === null || left === "";
  const rightMissing = right === null || right === "";
  if (leftMissing && rightMissing) return 0;
  if (leftMissing) return 1;
  if (rightMissing) return -1;
  return compare(left, right) * direction;
}

function compareCandidates(left, right, sort) {
  const direction = sort.direction === "asc" ? 1 : -1;
  const leftDerived = activeRuntime.derivedById.get(left.entry.id);
  const rightDerived = activeRuntime.derivedById.get(right.entry.id);
  let result = 0;

  switch (sort.field) {
    case "relevance":
      result = (left.score - right.score) * direction;
      break;
    case "title":
      result = compareNullable(
        leftDerived.title,
        rightDerived.title,
        direction,
        collator.compare.bind(collator),
      );
      break;
    case "author":
      result = compareNullable(
        leftDerived.author,
        rightDerived.author,
        direction,
        collator.compare.bind(collator),
      );
      break;
    case "year":
      result = compareNullable(
        leftDerived.year,
        rightDerived.year,
        direction,
        (leftYear, rightYear) => leftYear - rightYear,
      );
      break;
    case "added_at":
      result = compareNullable(
        leftDerived.addedAt,
        rightDerived.addedAt,
        direction,
        (leftDate, rightDate) => leftDate - rightDate,
      );
      break;
  }

  if (result !== 0) return result;
  result = collator.compare(leftDerived.title, rightDerived.title);
  if (result !== 0) return result;
  return collator.compare(left.entry.id, right.entry.id);
}

function hasFuzzyEligibleTerm(query) {
  return query
    .split(/[\s\p{P}]+/u)
    .some((term) => Array.from(term).length >= 5);
}

function primarySearch(query) {
  return activeRuntime.index.search(query, {
    combineWith: "AND",
    prefix: true,
    boost: { title: 4, author: 2, search_text: 1 },
  });
}

function fuzzySearch(query) {
  return activeRuntime.index.search(query, {
    combineWith: "AND",
    prefix: true,
    fuzzy: (term) => (Array.from(term).length >= 5 ? 1 : false),
    boost: { title: 4, author: 2, search_text: 1 },
  });
}

function handleSearch(payload) {
  if (!activeRuntime) throw new WorkerError("NOT_READY", "no catalog is loaded");
  const query = payload.query == null ? "" : payload.query;
  if (typeof query !== "string") invalidRequest("query must be a string");
  const normalizedQuery = normalizeText(query);
  const filters = normalizeFilters(payload.filters);
  const sort = normalizeSort(payload.sort);
  const offset = payload.offset == null ? 0 : payload.offset;
  const limit = payload.limit == null ? 24 : payload.limit;
  if (!Number.isInteger(offset) || offset < 0) invalidRequest("offset must be a non-negative integer");
  if (!Number.isInteger(limit) || limit < 0 || limit > MAX_RESULT_LIMIT) {
    invalidRequest("limit must be an integer between 0 and " + MAX_RESULT_LIMIT);
  }

  let candidates;
  if (normalizedQuery === "") {
    candidates = activeRuntime.visibleEntries.map((entry) => ({ entry, score: 0 }));
  } else {
    let results = primarySearch(normalizedQuery);
    if (results.length === 0 && hasFuzzyEligibleTerm(normalizedQuery)) {
      results = fuzzySearch(normalizedQuery);
    }
    candidates = results
      .map((result) => ({
        entry: activeRuntime.byId.get(result.id),
        score: result.score,
      }))
      .filter((candidate) => candidate.entry);
  }

  candidates = candidates.filter((candidate) =>
    matchesFilters(activeRuntime.derivedById.get(candidate.entry.id), filters),
  );
  candidates.sort((left, right) => compareCandidates(left, right, sort));
  const total = candidates.length;
  const entries = candidates.slice(offset, offset + limit).map((candidate) => candidate.entry);
  return { total, entries, offset, limit };
}

function handleGet(payload) {
  if (!activeRuntime) throw new WorkerError("NOT_READY", "no catalog is loaded");
  if (typeof payload.id !== "string" || payload.id.trim() === "") {
    invalidRequest("id must be a non-empty string");
  }
  return { entry: activeRuntime.byId.get(payload.id) || null };
}

async function handleForget() {
  postStatus("clearing");
  const database = await openDatabase();
  const transaction = database.transaction(
    [CATALOG_STORE, INDEX_STORE, STATE_STORE],
    "readwrite",
  );
  const completion = transactionToPromise(transaction);
  transaction.objectStore(CATALOG_STORE).clear();
  transaction.objectStore(INDEX_STORE).clear();
  transaction.objectStore(STATE_STORE).clear();
  await completion;
  activeRuntime = null;
  postStatus("empty", { cached: false });
  return { cleared: true };
}

function getPayload(message) {
  if (isObject(message.data)) return Object.assign({}, message, message.data);
  return message;
}

async function dispatchRequest(message) {
  const payload = getPayload(message);
  switch (message.type) {
    case "boot":
      return handleBoot();
    case "import":
      return handleImport(payload);
    case "search":
      return handleSearch(payload);
    case "get":
      return handleGet(payload);
    case "forget":
      return handleForget();
    default:
      throw new WorkerError("UNKNOWN_REQUEST", "unsupported worker request");
  }
}

function responseError(error) {
  if (error instanceof WorkerError) {
    return { code: error.code, message: error.message };
  }
  if (error && error.name === "QuotaExceededError") {
    return { code: "STORAGE_QUOTA", message: "not enough local storage for the catalog" };
  }
  return { code: "INTERNAL_ERROR", message: "worker operation failed" };
}

async function processMessage(message) {
  const requestId =
    isObject(message) && (typeof message.requestId === "string" || typeof message.requestId === "number")
      ? message.requestId
      : null;
  const type = isObject(message) && typeof message.type === "string" ? message.type : "unknown";
  try {
    if (requestId === null) invalidRequest("requestId must be a string or number");
    const data = await dispatchRequest(message);
    self.postMessage({ requestId, type, ok: true, data });
  } catch (error) {
    self.postMessage({
      requestId,
      type,
      ok: false,
      error: responseError(error),
    });
  }
}

self.addEventListener("message", (event) => {
  const message = event.data;
  const operation = mutationQueue.then(() => processMessage(message));
  if (isObject(message) && MUTATING_REQUESTS.has(message.type)) {
    mutationQueue = operation.catch(() => undefined);
  }
});
