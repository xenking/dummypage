(() => {
    "use strict";

    const SEARCH_BATCH = 60;
    const SEARCH_DELAY_MS = 90;
    const RPC_TIMEOUT_MS = 120_000;
    const MOBILE_BREAKPOINT = 1120;
    const CONTENT_ITEM_PREVIEW_LIMIT = 3;
    const ALLOWED_PROTOCOLS = new Set(["http:", "https:", "magnet:"]);
    const assetVersion = new URL(document.currentScript.src).searchParams.get("v");
    const MATERIAL_TYPE_LABELS = Object.freeze({
        archive: "архив",
        audio: "аудио",
        document: "документы",
        image: "изображения",
        torrent: "торрент",
        video: "видео",
    });
    const PHASE_LABELS = {
        opening: "Открываем локальную базу…",
        reading: "Читаем snapshot…",
        validating: "Проверяем данные…",
        indexing: "Строим поисковый индекс…",
        storing: "Сохраняем на этом устройстве…",
        ready: "База готова",
        forgetting: "Удаляем локальную базу…",
        "boot:open": "Открываем локальную базу…",
        "boot:index": "Восстанавливаем поисковый индекс…",
        "import:validate": "Проверяем данные…",
        "import:hydrate": "Готовим локальные записи…",
        "import:index": "Строим поисковый индекс…",
        "import:serialize": "Упаковываем локальный индекс…",
        "import:persist": "Сохраняем на этом устройстве…",
    };

    const dom = {
        app: document.querySelector("#catalog-app"),
        catalogCount: document.querySelector("#catalog-count"),
        connectionDot: document.querySelector("#connection-dot"),
        connectionStatus: document.querySelector("#connection-status"),
        versionStatus: document.querySelector("#version-status"),
        updatedStatus: document.querySelector("#updated-status"),
        storageStatus: document.querySelector("#storage-status"),
        updateButton: document.querySelector("#update-button"),
        forgetButton: document.querySelector("#forget-button"),
        settingsMenu: document.querySelector("#settings-menu"),
        settingsButton: document.querySelector("#settings-button"),
        searchInput: document.querySelector("#search-input"),
        filtersButton: document.querySelector("#filters-button"),
        filterCount: document.querySelector("#filter-count"),
        filtersDialog: document.querySelector("#filters-dialog"),
        filtersClose: document.querySelector("#filters-close"),
        applyFilters: document.querySelector("#apply-filters"),
        resetFilters: document.querySelector("#reset-filters"),
        formatFacet: document.querySelector("#format-facet"),
        formatOptions: document.querySelector("#format-options"),
        categoryOptions: document.querySelector("#category-options"),
        providerSelect: document.querySelector("#provider-select"),
        yearSelect: document.querySelector("#year-select"),
        passwordFilter: document.querySelector("#password-filter"),
        passwordCount: document.querySelector("#password-count"),
        sortSelect: document.querySelector("#sort-select"),
        sortDirection: document.querySelector("#sort-direction"),
        activeFilters: document.querySelector("#active-filters"),
        resultsList: document.querySelector("#results-list"),
        resultsCount: document.querySelector("#results-count"),
        resultsState: document.querySelector("#results-state"),
        stateTitle: document.querySelector("#state-title"),
        stateMessage: document.querySelector("#state-message"),
        stateAction: document.querySelector("#state-action"),
        resultsSentinel: document.querySelector("#results-sentinel"),
        batchProgress: document.querySelector("#batch-progress"),
        desktopDetail: document.querySelector("#desktop-detail"),
        detailDialog: document.querySelector("#detail-dialog"),
        detailDragHandle: document.querySelector("#detail-drag-handle"),
        mobileDetail: document.querySelector("#mobile-detail"),
        unlockDialog: document.querySelector("#unlock-dialog"),
        unlockForm: document.querySelector("#unlock-form"),
        unlockCopy: document.querySelector("#unlock-copy"),
        passwordInput: document.querySelector("#catalog-password"),
        passwordToggle: document.querySelector("#password-toggle"),
        unlockError: document.querySelector("#unlock-error"),
        importProgress: document.querySelector("#import-progress"),
        progressLabel: document.querySelector("#progress-label"),
        progressValue: document.querySelector("#progress-value"),
        progressBar: document.querySelector("#progress-bar"),
        unlockCancel: document.querySelector("#unlock-cancel"),
        unlockSubmit: document.querySelector("#unlock-submit"),
        toast: document.querySelector("#toast"),
    };

    const state = {
        rpc: null,
        ready: false,
        cached: false,
        meta: null,
        remoteMeta: null,
        facets: {
            formats: [],
            categories: [],
            providers: [],
            years: [],
            hasPassword: null,
        },
        filters: {
            formats: new Set(),
            categories: new Set(),
            provider: "",
            year: "",
            hasPassword: null,
        },
        sort: {
            field: "added_at",
            direction: "desc",
            explicit: false,
        },
        query: "",
        total: 0,
        offset: 0,
        searchGeneration: 0,
        loadingSearch: false,
        selectedId: null,
        stateAction: null,
        unlockMode: "initial",
        searchTimer: 0,
        toastTimer: 0,
        updateAvailable: false,
        filtersModal: false,
        detailReturnFocus: null,
    };

    class RPCError extends Error {
        constructor(code, message) {
            super(message || code || "Worker error");
            this.name = "RPCError";
            this.code = code || "WORKER_ERROR";
        }
    }

    class WorkerRPC {
        constructor(url, onProgress) {
            this.worker = new Worker(url);
            this.onProgress = onProgress;
            this.pending = new Map();
            this.sequence = 0;
            this.worker.addEventListener("message", (event) => this.handleMessage(event.data));
            this.worker.addEventListener("error", (event) => this.handleFatal(event));
            this.worker.addEventListener("messageerror", () => {
                this.rejectAll(new RPCError("WORKER_MESSAGE_ERROR", "Браузер не смог прочитать ответ поискового потока."));
            });
        }

        call(type, data = {}, transfer = []) {
            const requestId = `courses-${Date.now()}-${++this.sequence}`;
            const timeout = window.setTimeout(() => {
                const pending = this.pending.get(requestId);
                if (!pending) {
                    return;
                }
                this.pending.delete(requestId);
                pending.reject(new RPCError("WORKER_TIMEOUT", `Операция «${type}» не ответила вовремя.`));
            }, RPC_TIMEOUT_MS);

            const promise = new Promise((resolve, reject) => {
                this.pending.set(requestId, { resolve, reject, timeout });
            });

            this.worker.postMessage({ requestId, type, data, ...data }, transfer);
            return promise;
        }

        handleMessage(message) {
            if (!message || typeof message !== "object") {
                return;
            }
            if (!message.requestId) {
                if (message.type === "progress" && typeof this.onProgress === "function") {
                    this.onProgress(message);
                }
                return;
            }

            const pending = this.pending.get(message.requestId);
            if (!pending) {
                return;
            }
            window.clearTimeout(pending.timeout);
            this.pending.delete(message.requestId);

            if (message.ok === false || message.error) {
                const error = message.error || {};
                pending.reject(new RPCError(error.code, error.message));
                return;
            }
            pending.resolve(message.data ?? message.result ?? message.payload ?? message);
        }

        handleFatal(event) {
            const message = event && event.message
                ? event.message
                : "Поисковый поток завершился с ошибкой.";
            this.rejectAll(new RPCError("WORKER_CRASH", message));
        }

        rejectAll(error) {
            for (const pending of this.pending.values()) {
                window.clearTimeout(pending.timeout);
                pending.reject(error);
            }
            this.pending.clear();
        }
    }

    function createElement(tag, className, text) {
        const element = document.createElement(tag);
        if (className) {
            element.className = className;
        }
        if (text !== undefined && text !== null) {
            element.textContent = String(text);
        }
        return element;
    }

    function clearNode(node) {
        node.replaceChildren();
    }

    function russianNumber(value) {
        const number = Number(value);
        return Number.isFinite(number) ? new Intl.NumberFormat("ru-RU").format(number) : "—";
    }

    function formatBytes(value) {
        const bytes = Number(value);
        if (!Number.isFinite(bytes) || bytes < 0) {
            return "—";
        }
        const units = ["Б", "КБ", "МБ", "ГБ"];
        let amount = bytes;
        let unit = 0;
        while (amount >= 1024 && unit < units.length - 1) {
            amount /= 1024;
            unit += 1;
        }
        const digits = unit === 0 || amount >= 10 ? 0 : 1;
        return `${amount.toLocaleString("ru-RU", { maximumFractionDigits: digits })} ${units[unit]}`;
    }

    function russianCount(value, forms) {
        const number = Number(value);
        const absolute = Math.abs(number);
        const lastTwo = absolute % 100;
        const last = absolute % 10;
        let form = forms.many;
        if (lastTwo < 11 || lastTwo > 14) {
            if (last === 1) {
                form = forms.one;
            } else if (last >= 2 && last <= 4) {
                form = forms.few;
            }
        }
        return `${russianNumber(number)} ${form}`;
    }

    function formatLinkContentSummary(content) {
        if (!content || typeof content !== "object" || Array.isArray(content)) {
            return "";
        }
        const parts = [];
        const name = typeof content.name === "string" && content.name
            ? content.name
            : typeof content.kind === "string"
                ? content.kind
                : "";
        if (name) {
            parts.push(name);
        }
        if (Number.isSafeInteger(content.size_bytes) && content.size_bytes >= 0) {
            parts.push(formatBytes(content.size_bytes));
        }
        if (Number.isSafeInteger(content.file_count) && content.file_count >= 0) {
            parts.push(russianCount(content.file_count, {
                one: "файл",
                few: "файла",
                many: "файлов",
            }));
        }
        if (Number.isSafeInteger(content.folder_count) && content.folder_count >= 0) {
            parts.push(russianCount(content.folder_count, {
                one: "папка",
                few: "папки",
                many: "папок",
            }));
        }

        const materialLabels = Array.isArray(content.material_types)
            ? content.material_types
                .map((materialType) => MATERIAL_TYPE_LABELS[materialType])
                .filter(Boolean)
            : [];
        if (materialLabels.length) {
            parts.push(materialLabels.join(", "));
        }

        const itemNames = Array.isArray(content.items)
            ? content.items
                .map((item) => typeof item?.name === "string" ? item.name : "")
                .filter(Boolean)
            : [];
        if (itemNames.length) {
            const preview = itemNames.slice(0, CONTENT_ITEM_PREVIEW_LIMIT).join(", ");
            const remaining = itemNames.length - CONTENT_ITEM_PREVIEW_LIMIT;
            parts.push(remaining > 0 ? `${preview} +${remaining}` : preview);
        }
        return parts.join(" · ");
    }

    function formatDate(value, includeTime = false) {
        if (!value) {
            return "—";
        }
        const date = new Date(value);
        if (Number.isNaN(date.valueOf())) {
            return String(value);
        }
        return new Intl.DateTimeFormat("ru-RU", {
            dateStyle: "medium",
            ...(includeTime ? { timeStyle: "short" } : {}),
        }).format(date);
    }

    function shortVersion(value) {
        const version = typeof value === "string" ? value.trim() : "";
        if (!version) {
            return "—";
        }
        if (version.length <= 12) {
            return version;
        }
        return version.slice(0, 8);
    }

    function normalizedFacet(items) {
        if (!Array.isArray(items)) {
            return [];
        }
        return items
            .map((item) => {
                if (item === null || item === undefined) {
                    return null;
                }
                if (typeof item !== "object") {
                    return { value: String(item), label: String(item), count: null };
                }
                const rawValue = item.value ?? item.id ?? item.provider ?? item.year ?? item.name;
                if (rawValue === null || rawValue === undefined || rawValue === "") {
                    return null;
                }
                return {
                    value: String(rawValue),
                    label: String(item.label ?? item.name ?? rawValue),
                    count: Number.isFinite(Number(item.count)) ? Number(item.count) : null,
                };
            })
            .filter(Boolean);
    }

    function safeExternalURL(rawURL) {
        if (typeof rawURL !== "string" || !rawURL.trim()) {
            return null;
        }
        try {
            const parsed = new URL(rawURL.trim(), window.location.href);
            if (!ALLOWED_PROTOCOLS.has(parsed.protocol)) {
                return null;
            }
            if ((parsed.protocol === "http:" || parsed.protocol === "https:") && !parsed.hostname) {
                return null;
            }
            if (parsed.protocol === "magnet:" && !parsed.search) {
                return null;
            }
            return parsed.href;
        } catch {
            return null;
        }
    }

    function setConnection(text, tone) {
        dom.connectionStatus.textContent = text;
        dom.connectionDot.dataset.tone = tone;
        dom.settingsButton.dataset.tone = tone;
        dom.settingsButton.setAttribute("aria-label", `Настройки локальной базы: ${text}`);
    }

    function showToast(message, tone = "ready") {
        window.clearTimeout(state.toastTimer);
        dom.toast.textContent = message;
        dom.toast.dataset.tone = tone;
        dom.toast.hidden = false;
        state.toastTimer = window.setTimeout(() => {
            dom.toast.hidden = true;
        }, 2600);
    }

    function setResultsState(title, message, actionLabel, action) {
        dom.stateTitle.textContent = title;
        dom.stateMessage.textContent = message;
        state.stateAction = typeof action === "function" ? action : null;
        if (actionLabel && state.stateAction) {
            dom.stateAction.textContent = actionLabel;
            dom.stateAction.hidden = false;
        } else {
            dom.stateAction.hidden = true;
        }
        dom.resultsState.hidden = false;
    }

    function hideResultsState() {
        dom.resultsState.hidden = true;
        state.stateAction = null;
    }

    function setControlsEnabled(enabled) {
        dom.searchInput.disabled = !enabled;
        dom.filtersButton.disabled = !enabled;
        dom.sortSelect.disabled = !enabled;
        dom.sortDirection.disabled = !enabled;
        dom.providerSelect.disabled = !enabled;
        dom.yearSelect.disabled = !enabled;
        dom.passwordFilter.disabled = !enabled;
        dom.resetFilters.disabled = !enabled;
        for (const input of dom.formatOptions.querySelectorAll("input")) {
            input.disabled = !enabled;
        }
        for (const input of dom.categoryOptions.querySelectorAll("input")) {
            input.disabled = !enabled;
        }
    }

    function updateStatusStrip() {
        const meta = state.meta || {};
        const remoteMeta = state.remoteMeta || {};
        const count = meta.entry_count ?? meta.stats?.entries;
        dom.catalogCount.textContent = Number.isFinite(Number(count)) ? russianNumber(count) : "—";
        dom.versionStatus.textContent = shortVersion(meta.version);
        dom.updatedStatus.textContent = formatDate(meta.updated_at ?? meta.exported_at, true);
        dom.storageStatus.textContent = state.cached
            ? formatBytes(remoteMeta.bytes ?? meta.bytes)
            : "Не загружена";
        dom.updateButton.disabled = !state.ready || !remoteMeta.available;
        dom.forgetButton.disabled = !state.cached;
        dom.updateButton.textContent = state.updateAvailable ? "Доступно обновление" : "Обновить базу";

        if (!navigator.onLine) {
            setConnection(state.cached ? "Офлайн · поиск работает локально" : "Нет сети", "offline");
        } else if (state.ready) {
            setConnection(state.updateAvailable ? "Локальная база · есть обновление" : "Локальная база готова", "ready");
        }
    }

    function updateOnlineStatus() {
        updateStatusStrip();
        if (navigator.onLine && state.ready) {
            void checkRemoteMeta(true);
        }
    }

    function updateImportProgress(progress) {
        const phase = String(progress.phase || "");
        const current = Number(progress.current);
        const total = Number(progress.total);
        dom.importProgress.hidden = false;
        dom.progressLabel.textContent = PHASE_LABELS[phase] || "Обрабатываем базу…";

        if (Number.isFinite(current) && Number.isFinite(total) && total > 0) {
            const percent = Math.min(100, Math.max(0, Math.round((current / total) * 100)));
            dom.progressBar.value = percent;
            dom.progressValue.textContent = `${percent}%`;
        } else {
            dom.progressBar.removeAttribute("value");
            dom.progressValue.textContent = Number.isFinite(current) && current > 0
                ? russianNumber(current)
                : "";
        }

        if (phase && state.unlockMode !== "idle") {
            setConnection(dom.progressLabel.textContent, "working");
        }
    }

    function isDesktop() {
        return window.innerWidth >= MOBILE_BREAKPOINT;
    }

    function syncFiltersDialog() {
        if (isDesktop()) {
            if (state.filtersModal && dom.filtersDialog.open) {
                dom.filtersDialog.close();
            }
            state.filtersModal = false;
            if (!dom.filtersDialog.open) {
                dom.filtersDialog.show();
            }
            if (dom.detailDialog.open) {
                dom.detailDialog.close();
            }
            return;
        }
        if (dom.filtersDialog.open && !state.filtersModal) {
            dom.filtersDialog.close();
        }
    }

    function openFilters() {
        if (!isDesktop() && !dom.filtersDialog.open) {
            state.filtersModal = true;
            dom.filtersDialog.showModal();
        }
    }

    function closeFilters() {
        if (!isDesktop() && dom.filtersDialog.open) {
            dom.filtersDialog.close();
            state.filtersModal = false;
            dom.filtersButton.focus();
        }
    }

    function applyWorkerState(payload) {
        const data = payload && typeof payload === "object" ? payload : {};
        state.cached = Boolean(data.cached);
        state.ready = state.cached;
        state.meta = data.meta || state.meta;
        state.facets = {
            formats: normalizedFacet(data.facets?.formats),
            categories: normalizedFacet(data.facets?.categories),
            providers: normalizedFacet(data.facets?.providers),
            years: normalizedFacet(data.facets?.years),
            hasPassword: data.facets?.hasPassword || null,
        };
        renderFacets();
        setControlsEnabled(state.ready);
        dom.app.setAttribute("aria-busy", state.ready ? "false" : "true");
        updateStatusStrip();
    }

    function renderCheckFacet(container, items, selected, groupName) {
        clearNode(container);
        if (!items.length) {
            container.append(createElement("p", "facet-placeholder", "Нет доступных значений."));
            return;
        }
        const fragment = document.createDocumentFragment();
        for (const item of items) {
            const label = createElement("label", "check-row");
            const input = document.createElement("input");
            input.type = "checkbox";
            input.name = groupName;
            input.value = item.value;
            input.checked = selected.has(item.value);
            input.disabled = !state.ready;
            const title = createElement("span", "", item.label);
            const count = createElement("span", "facet-count", item.count === null ? "" : russianNumber(item.count));
            label.append(input, title, count);
            fragment.append(label);
        }
        container.append(fragment);
    }

    function renderSelectFacet(select, items, allLabel, selectedValue) {
        clearNode(select);
        const all = document.createElement("option");
        all.value = "";
        all.textContent = allLabel;
        select.append(all);
        for (const item of items) {
            const option = document.createElement("option");
            option.value = item.value;
            option.textContent = item.count === null
                ? item.label
                : `${item.label} · ${russianNumber(item.count)}`;
            select.append(option);
        }
        select.value = items.some((item) => item.value === selectedValue) ? selectedValue : "";
    }

    function renderFacets() {
        renderCheckFacet(dom.formatOptions, state.facets.formats, state.filters.formats, "format");
        dom.formatFacet.hidden = state.facets.formats.length === 0;
        renderCheckFacet(dom.categoryOptions, state.facets.categories, state.filters.categories, "category");
        renderSelectFacet(dom.providerSelect, state.facets.providers, "Все источники", state.filters.provider);
        renderSelectFacet(dom.yearSelect, state.facets.years, "Все годы", state.filters.year);
        dom.passwordFilter.checked = state.filters.hasPassword === true;

        const withPassword = Number(state.facets.hasPassword?.withPassword);
        dom.passwordCount.textContent = Number.isFinite(withPassword)
            ? russianNumber(withPassword)
            : "";
        renderActiveFilters();
    }

    function facetLabel(kind, value) {
        const source = state.facets[kind] || [];
        return source.find((item) => item.value === String(value))?.label || String(value);
    }

    function activeFilterDescriptors() {
        const filters = [];
        for (const value of state.filters.formats) {
            filters.push({
                key: `format:${value}`,
                label: `Тип: ${facetLabel("formats", value)}`,
                remove: () => state.filters.formats.delete(value),
            });
        }
        for (const value of state.filters.categories) {
            filters.push({
                key: `category:${value}`,
                label: facetLabel("categories", value),
                remove: () => state.filters.categories.delete(value),
            });
        }
        if (state.filters.provider) {
            filters.push({
                key: "provider",
                label: facetLabel("providers", state.filters.provider),
                remove: () => {
                    state.filters.provider = "";
                },
            });
        }
        if (state.filters.year) {
            filters.push({
                key: "year",
                label: state.filters.year,
                remove: () => {
                    state.filters.year = "";
                },
            });
        }
        if (state.filters.hasPassword === true) {
            filters.push({
                key: "password",
                label: "С паролем",
                remove: () => {
                    state.filters.hasPassword = null;
                },
            });
        }
        return filters;
    }

    function renderActiveFilters() {
        clearNode(dom.activeFilters);
        const descriptors = activeFilterDescriptors();
        const fragment = document.createDocumentFragment();
        for (const descriptor of descriptors) {
            const item = createElement("li", "filter-chip");
            const button = createElement("button", "", descriptor.label);
            button.type = "button";
            button.setAttribute("aria-label", `Убрать фильтр: ${descriptor.label}`);
            button.addEventListener("click", () => {
                descriptor.remove();
                renderFacets();
                void runSearch(true);
            });
            item.dataset.key = descriptor.key;
            item.append(button);
            fragment.append(item);
        }
        dom.activeFilters.append(fragment);
        dom.filterCount.textContent = String(descriptors.length);
        dom.filterCount.hidden = descriptors.length === 0;
    }

    function resetFilters() {
        state.filters.formats.clear();
        state.filters.categories.clear();
        state.filters.provider = "";
        state.filters.year = "";
        state.filters.hasPassword = null;
        renderFacets();
        void runSearch(true);
    }

    function buildSearchRequest(offset) {
        const yearNumber = Number(state.filters.year);
        return {
            query: state.query,
            filters: {
                formats: [...state.filters.formats],
                categories: [...state.filters.categories],
                providers: state.filters.provider ? [state.filters.provider] : [],
                years: state.filters.year && Number.isFinite(yearNumber) ? [yearNumber] : [],
                hasPassword: state.filters.hasPassword,
            },
            sort: {
                field: state.sort.field,
                direction: state.sort.direction,
            },
            offset,
            limit: SEARCH_BATCH,
        };
    }

    function providerForEntry(entry) {
        if (entry.provider_label) {
            return String(entry.provider_label);
        }
        if (entry.provider) {
            return String(entry.provider);
        }
        if (entry.primary_provider) {
            return String(entry.primary_provider);
        }
        const link = Array.isArray(entry.links) ? entry.links[0] : null;
        return String(link?.host || link?.provider || "Источник не указан");
    }

    function yearForEntry(entry) {
        if (entry.year !== null && entry.year !== undefined && entry.year !== "") {
            return String(entry.year);
        }
        if (entry.year_range && typeof entry.year_range === "object") {
            const start = entry.year_range.start ?? entry.year_range.from;
            const end = entry.year_range.end ?? entry.year_range.to;
            if (start && end && start !== end) {
                return `${start}–${end}`;
            }
            if (start || end) {
                return String(start || end);
            }
        }
        return "—";
    }

    function renderResult(entry) {
        const item = createElement("li", "result-item");
        item.dataset.entryId = String(entry.id);
        const selected = state.selectedId === String(entry.id);
        item.dataset.selected = String(selected);

        const button = createElement("button", "result-select");
        button.type = "button";
        button.dataset.entryId = String(entry.id);
        button.setAttribute("aria-label", `Открыть информацию: ${entry.title || "Курс без названия"}`);
        if (selected) {
            button.setAttribute("aria-current", "true");
        }

        const title = createElement("span", "result-title", entry.title || "Курс без названия");
        const arrow = createElement("span", "result-arrow", "›");
        arrow.setAttribute("aria-hidden", "true");

        const meta = createElement("span", "result-meta");
        meta.append(
            createElement("span", "result-author", entry.author || "Автор не указан"),
            createElement("span", "result-year", yearForEntry(entry)),
            createElement("span", "result-provider", providerForEntry(entry)),
        );

        button.append(title, arrow, meta);
        button.addEventListener("click", () => void selectEntry(entry));
        item.append(button);
        return item;
    }

    function updateSelectedResult() {
        for (const item of dom.resultsList.querySelectorAll(".result-item")) {
            const selected = item.dataset.entryId === state.selectedId;
            item.dataset.selected = String(selected);
            const button = item.querySelector(".result-select");
            if (button) {
                if (selected) {
                    button.setAttribute("aria-current", "true");
                } else {
                    button.removeAttribute("aria-current");
                }
            }
        }
    }

    function updateResultsCount(visibleCount) {
        if (state.total === 0) {
            dom.resultsCount.textContent = "Ничего не найдено";
            return;
        }
        dom.resultsCount.textContent = `Найдено: ${russianNumber(state.total)} · показано ${russianNumber(visibleCount)}`;
    }

    async function runSearch(reset) {
        if (!state.ready || !state.rpc) {
            return;
        }
        if (!reset && (state.loadingSearch || state.offset >= state.total)) {
            return;
        }

        syncAutomaticSort();
        const generation = reset ? ++state.searchGeneration : state.searchGeneration;
        const offset = reset ? 0 : state.offset;
        state.loadingSearch = true;
        dom.batchProgress.hidden = reset;
        if (reset) {
            setResultsState("Ищем в локальной базе", "Результаты появятся сразу после поиска.", "", null);
            dom.resultsCount.textContent = "Поиск…";
        }

        try {
            const response = await state.rpc.call("search", buildSearchRequest(offset));
            if (generation !== state.searchGeneration) {
                return;
            }
            const entries = Array.isArray(response?.entries) ? response.entries : [];
            state.total = Math.max(0, Number(response?.total) || 0);
            if (reset) {
                clearNode(dom.resultsList);
                state.offset = 0;
            }

            const fragment = document.createDocumentFragment();
            for (const entry of entries) {
                if (!entry || entry.id === null || entry.id === undefined) {
                    continue;
                }
                fragment.append(renderResult(entry));
            }
            dom.resultsList.append(fragment);
            state.offset = offset + entries.length;
            updateResultsCount(dom.resultsList.children.length);

            if (state.total === 0) {
                const filtered = Boolean(state.query.trim() || activeFilterDescriptors().length);
                setResultsState(
                    filtered ? "Совпадений нет" : "Каталог пуст",
                    filtered
                        ? "Измените запрос или снимите часть фильтров."
                        : "В этом snapshot нет доступных записей. Попробуйте обновить базу.",
                    filtered ? "Сбросить поиск и фильтры" : "Обновить базу",
                    filtered
                        ? () => {
                            dom.searchInput.value = "";
                            state.query = "";
                            resetFilters();
                        }
                        : () => openUnlock("update"),
                );
            } else {
                hideResultsState();
            }
        } catch (error) {
            if (generation !== state.searchGeneration) {
                return;
            }
            setResultsState(
                "Поиск остановлен",
                workerErrorMessage(error),
                "Повторить",
                () => void runSearch(true),
            );
            dom.resultsCount.textContent = "Ошибка поиска";
        } finally {
            if (generation === state.searchGeneration) {
                state.loadingSearch = false;
                dom.batchProgress.hidden = true;
            }
        }
    }

    function scheduleSearch() {
        window.clearTimeout(state.searchTimer);
        state.searchTimer = window.setTimeout(() => void runSearch(true), SEARCH_DELAY_MS);
    }

    function workerErrorMessage(error) {
        const code = error?.code;
        if (code === "CACHE_CORRUPT" || code === "INVALID_CACHE") {
            return "Локальная копия повреждена. Удалите её и загрузите snapshot заново.";
        }
        if (code === "WORKER_TIMEOUT") {
            return "Локальная обработка заняла слишком много времени. Повторите операцию.";
        }
        if (code === "UNSUPPORTED_SCHEMA") {
            return "Версия snapshot не поддерживается этим интерфейсом. Обновите страницу.";
        }
        if (code === "INVALID_CATALOG") {
            return "Snapshot повреждён или пуст. Локальная копия не изменена.";
        }
        return error?.message || "Не удалось обработать локальную базу.";
    }

    function detailLoading(target, mobile) {
        clearNode(target);
        const wrapper = createElement("div", "detail-content");
        const heading = createElement("h2", "detail-title", "Загрузка информации…");
        heading.id = mobile ? "mobile-detail-heading" : "detail-heading";
        wrapper.append(heading, createElement("p", "detail-notes", "Получаем полную запись из локальной базы."));
        target.append(wrapper);
    }

    async function selectEntry(summary) {
        const id = String(summary.id);
        state.selectedId = id;
        updateSelectedResult();

        const mobile = !isDesktop();
        detailLoading(dom.desktopDetail, false);
        detailLoading(dom.mobileDetail, true);
        if (mobile && !dom.detailDialog.open) {
            state.detailReturnFocus = document.activeElement;
            dom.detailDialog.showModal();
            dom.mobileDetail.focus({ preventScroll: true });
        }

        try {
            const response = await state.rpc.call("get", { id });
            if (state.selectedId !== id) {
                return;
            }
            const entry = response?.entry || (response?.id ? response : summary);
            renderDetail(entry, dom.desktopDetail, false);
            renderDetail(entry, dom.mobileDetail, true);
        } catch (error) {
            if (state.selectedId !== id) {
                return;
            }
            renderDetailError(error, dom.desktopDetail, false, summary);
            renderDetailError(error, dom.mobileDetail, true, summary);
        }
    }

    function detailMetaRow(list, label, value) {
        if (value === null || value === undefined || value === "") {
            return;
        }
        list.append(createElement("dt", "", label), createElement("dd", "", value));
    }

    function formatLabels(entry) {
        const values = Array.isArray(entry.formats)
            ? entry.formats
            : entry.primary_format
                ? [entry.primary_format]
                : [];
        return values.map((value) => facetLabel("formats", value)).join(", ");
    }

    function categoryLabels(entry) {
        const values = Array.isArray(entry.categories)
            ? entry.categories
            : entry.primary_category
                ? [entry.primary_category]
                : [];
        return values.map((value) => facetLabel("categories", value));
    }

    function renderDetail(entry, target, mobile) {
        clearNode(target);
        const wrapper = createElement("article", "detail-content");
        const title = createElement("h2", "detail-title", entry.title || "Курс без названия");
        title.id = mobile ? "mobile-detail-heading" : "detail-heading";
        wrapper.append(title);

        const metadata = createElement("dl", "detail-meta");
        detailMetaRow(metadata, "Автор", entry.author || "Не указан");
        detailMetaRow(metadata, "Год", yearForEntry(entry));
        detailMetaRow(metadata, "Материал", formatLabels(entry));
        detailMetaRow(metadata, "Источник", providerForEntry(entry));
        detailMetaRow(metadata, "Добавлен", formatDate(entry.last_added_at));
        wrapper.append(metadata);

        const labels = categoryLabels(entry);
        if (labels.length) {
            const tags = createElement("ul", "tag-list");
            for (const label of labels) {
                tags.append(createElement("li", "", label));
            }
            wrapper.append(tags);
        }

        const links = Array.isArray(entry.links)
            ? entry.links
                .map((link) => ({ ...link, safeURL: safeExternalURL(link?.url) }))
                .filter((link) => link.safeURL)
            : [];
        wrapper.append(renderLinksSection(links));

        const passwords = Array.isArray(entry.passwords)
            ? entry.passwords.filter((password) => typeof password === "string" && password.length > 0)
            : [];
        if (passwords.length) {
            wrapper.append(renderPasswordsSection(passwords));
        }

        const notesValues = Array.isArray(entry.notes)
            ? entry.notes.filter((note) => typeof note === "string" && note.trim())
            : [];
        if (notesValues.length) {
            const notes = createElement("section", "detail-section");
            const notesList = createElement("ul", "detail-notes-list");
            for (const note of notesValues) {
                notesList.append(createElement("li", "", note));
            }
            notes.append(createElement("h3", "", "Заметки"), notesList);
            wrapper.append(notes);
        }

        target.append(wrapper);
    }

    function renderLinksSection(links) {
        const section = createElement("section", "detail-section");
        const heading = createElement("div", "detail-section-heading");
        heading.append(createElement("h3", "", `Ссылки (${russianNumber(links.length)})`));

        if (links.length) {
            const copyAll = createElement("button", "button button--quiet", "Копировать все");
            copyAll.type = "button";
            copyAll.addEventListener("click", () => void copyText(
                links.map((link) => link.safeURL).join("\n"),
                "Все ссылки скопированы",
                copyAll,
            ));
            heading.append(copyAll);
        }
        section.append(heading);

        if (!links.length) {
            section.append(createElement("p", "detail-notes", "В записи нет безопасных внешних ссылок."));
            return section;
        }

        const list = createElement("ol", "detail-list");
        for (const [index, link] of links.entries()) {
            const item = createElement("li", "link-row");
            const label = link.label || link.host || link.provider || `Ссылка ${index + 1}`;
            const value = createElement("p", "link-value", `${index + 1}. ${label} · ${link.safeURL}`);
            value.title = link.safeURL;
            const copy = createElement("div", "link-copy");
            copy.append(value);
            const contentSummary = formatLinkContentSummary(link.content);
            if (contentSummary) {
                const summary = createElement(
                    "p",
                    "link-value link-content-summary",
                    contentSummary,
                );
                summary.title = contentSummary;
                copy.append(summary);
            }
            const actions = createElement("div", "row-actions");
            const open = createElement("button", "button button--quiet", "Открыть");
            open.type = "button";
            open.addEventListener("click", () => openExternal(link.safeURL));
            const copyButton = createElement("button", "button button--quiet", "Копировать");
            copyButton.type = "button";
            copyButton.addEventListener("click", () => void copyText(
                link.safeURL,
                "Ссылка скопирована",
                copyButton,
            ));
            actions.append(open, copyButton);
            item.append(copy, actions);
            list.append(item);
        }
        section.append(list);
        return section;
    }

    function renderPasswordsSection(passwords) {
        const section = createElement("section", "detail-section");
        section.append(createElement("h3", "", passwords.length > 1 ? "Пароли к архивам" : "Пароль к архиву"));
        const list = createElement("ul", "detail-list");
        for (const [index, password] of passwords.entries()) {
            const item = createElement("li", "password-row");
            const masked = createElement(
                "p",
                "password-value",
                `${passwords.length > 1 ? `${index + 1}. ` : ""}${"•".repeat(Math.min(Math.max(password.length, 8), 18))}`,
            );
            masked.setAttribute("aria-label", `Пароль ${index + 1} скрыт`);
            const actions = createElement("div", "row-actions");
            const reveal = createElement("button", "button button--quiet", "Показать");
            reveal.type = "button";
            let visible = false;
            reveal.addEventListener("click", () => {
                visible = !visible;
                masked.textContent = visible
                    ? `${passwords.length > 1 ? `${index + 1}. ` : ""}${password}`
                    : `${passwords.length > 1 ? `${index + 1}. ` : ""}${"•".repeat(Math.min(Math.max(password.length, 8), 18))}`;
                masked.setAttribute("aria-label", visible ? `Пароль ${index + 1}` : `Пароль ${index + 1} скрыт`);
                reveal.textContent = visible ? "Скрыть" : "Показать";
            });
            const copy = createElement("button", "button button--primary", "Копировать");
            copy.type = "button";
            copy.addEventListener("click", () => void copyText(password, "Пароль скопирован", copy));
            actions.append(reveal, copy);
            item.append(masked, actions);
            list.append(item);
        }
        section.append(list);
        return section;
    }

    function renderDetailError(error, target, mobile, summary) {
        clearNode(target);
        const wrapper = createElement("div", "detail-content");
        const heading = createElement("h2", "detail-title", summary.title || "Информация недоступна");
        heading.id = mobile ? "mobile-detail-heading" : "detail-heading";
        const message = createElement("p", "detail-notes", workerErrorMessage(error));
        const retry = createElement("button", "button button--quiet detail-retry", "Повторить");
        retry.type = "button";
        retry.addEventListener("click", () => void selectEntry(summary));
        wrapper.append(heading, message, retry);
        target.append(wrapper);
    }

    function openExternal(url) {
        const safeURL = safeExternalURL(url);
        if (!safeURL) {
            showToast("Ссылка использует неподдерживаемый протокол", "error");
            return;
        }
        const opened = window.open(safeURL, "_blank", "noopener,noreferrer");
        if (opened) {
            opened.opener = null;
        }
    }

    async function copyText(value, successMessage, button) {
        const originalLabel = button?.textContent;
        try {
            if (navigator.clipboard && window.isSecureContext) {
                await navigator.clipboard.writeText(value);
            } else {
                const textarea = document.createElement("textarea");
                textarea.value = value;
                textarea.readOnly = true;
                textarea.className = "copy-fallback";
                document.body.append(textarea);
                textarea.select();
                const copied = document.execCommand("copy");
                textarea.remove();
                if (!copied) {
                    throw new Error("copy failed");
                }
            }
            if (button) {
                button.textContent = "Скопировано";
                window.setTimeout(() => {
                    button.textContent = originalLabel;
                }, 1800);
            }
            showToast(successMessage);
        } catch {
            showToast("Не удалось скопировать. Выделите значение вручную.", "error");
        }
    }

    async function checkRemoteMeta(silent = false) {
        if (!navigator.onLine) {
            if (!state.cached && !silent) {
                setResultsState(
                    "Нет подключения",
                    "Для первой загрузки нужна сеть. После импорта каталог будет работать локально.",
                    "Повторить",
                    () => void checkRemoteMeta(),
                );
            }
            updateStatusStrip();
            return null;
        }

        try {
            const response = await fetch("/courses/api/meta", {
                method: "GET",
                headers: { Accept: "application/json" },
                cache: "no-store",
                credentials: "same-origin",
            });
            if (!response.ok) {
                throw new Error(`meta ${response.status}`);
            }
            const meta = await response.json();
            state.remoteMeta = meta && typeof meta === "object" ? meta : {};
            state.updateAvailable = Boolean(
                state.cached
                && state.remoteMeta.available
                && state.meta?.version
                && state.remoteMeta.version
                && state.meta.version !== state.remoteMeta.version,
            );
            updateStatusStrip();

            if (!state.remoteMeta.available && !state.cached) {
                setResultsState(
                    "Каталог временно недоступен",
                    "Сервер ещё не опубликовал snapshot. Попробуйте позже.",
                    "Проверить снова",
                    () => void checkRemoteMeta(),
                );
                return state.remoteMeta;
            }

            if (!state.cached && state.remoteMeta.available && !dom.unlockDialog.open) {
                openUnlock("initial");
            }
            return state.remoteMeta;
        } catch {
            if (!state.cached && !silent) {
                setResultsState(
                    "Не удалось проверить обновления",
                    "Проверьте соединение и повторите запрос. Локальная база не изменена.",
                    "Повторить",
                    () => void checkRemoteMeta(),
                );
            }
            updateStatusStrip();
            return null;
        }
    }

    function openUnlock(mode) {
        state.unlockMode = mode;
        dom.unlockCopy.textContent = mode === "update"
            ? "Введите общий пароль, чтобы заменить локальный snapshot. Пароль не будет сохранён."
            : "Пароль нужен только для загрузки snapshot и не сохраняется интерфейсом.";
        dom.unlockError.hidden = true;
        dom.importProgress.hidden = true;
        dom.progressBar.removeAttribute("value");
        dom.progressValue.textContent = "";
        dom.unlockCancel.hidden = !state.cached;
        dom.unlockCancel.disabled = false;
        dom.unlockSubmit.disabled = false;
        dom.unlockSubmit.textContent = mode === "update" ? "Обновить базу" : "Загрузить базу";
        dom.passwordInput.value = "";
        dom.passwordInput.type = "password";
        dom.passwordToggle.textContent = "Показать";
        dom.passwordToggle.setAttribute("aria-label", "Показать пароль");
        if (!dom.unlockDialog.open) {
            dom.unlockDialog.showModal();
        }
        if (!state.cached) {
            setConnection("Ожидаем пароль для первой загрузки", "working");
        }
        window.setTimeout(() => dom.passwordInput.focus(), 0);
    }

    function setUnlockError(message) {
        dom.unlockError.textContent = message;
        dom.unlockError.hidden = false;
        dom.importProgress.hidden = true;
        dom.unlockSubmit.disabled = false;
        dom.unlockCancel.disabled = false;
        dom.unlockSubmit.textContent = state.unlockMode === "update" ? "Обновить базу" : "Загрузить базу";
        if (state.cached) {
            updateStatusStrip();
        } else {
            setConnection("База не загружена", "error");
        }
        dom.passwordInput.focus();
    }

    async function importCatalog(password) {
        if (!navigator.onLine) {
            setUnlockError("Нет сети. Для загрузки snapshot подключитесь к интернету.");
            return;
        }
        dom.unlockError.hidden = true;
        dom.unlockSubmit.disabled = true;
        dom.unlockCancel.disabled = true;
        dom.unlockSubmit.textContent = "Загрузка…";
        updateImportProgress({ phase: "reading" });

        let catalog = null;
        try {
            const response = await fetch("/courses/api/catalog", {
                method: "POST",
                headers: {
                    Accept: "application/json",
                    "Content-Type": "application/json",
                },
                credentials: "same-origin",
                cache: "no-store",
                body: JSON.stringify({ password }),
            });

            dom.passwordInput.value = "";
            password = "";

            if (response.status === 401) {
                setUnlockError("Неверный пароль. Проверьте раскладку и попробуйте ещё раз.");
                return;
            }
            if (response.status === 429) {
                setUnlockError("Слишком много попыток. Подождите 15 минут и попробуйте снова.");
                return;
            }
            if (response.status === 503) {
                setUnlockError("Каталог временно недоступен на сервере. Локальная копия не изменена.");
                return;
            }
            if (!response.ok) {
                setUnlockError(`Сервер не отдал каталог (HTTP ${response.status}). Попробуйте позже.`);
                return;
            }

            updateImportProgress({ phase: "validating" });
            try {
                catalog = await response.json();
            } catch {
                setUnlockError("Получен повреждённый snapshot. Локальная копия не изменена.");
                return;
            }

            if (
                !catalog
                || catalog.schema_version !== "courses-catalog/v2"
                || !Array.isArray(catalog.entries)
            ) {
                setUnlockError("Формат snapshot не поддерживается. Обновите страницу и повторите загрузку.");
                return;
            }

            const remoteMeta = state.remoteMeta || await checkRemoteMeta(true) || {};
            const imported = await state.rpc.call("import", {
                catalog,
                meta: remoteMeta,
                version: remoteMeta.version,
            });
            catalog = null;
            applyWorkerState(imported);
            state.updateAvailable = false;
            state.unlockMode = "idle";
            if (dom.unlockDialog.open) {
                dom.unlockDialog.close();
            }
            setConnection("Локальная база готова", "ready");
            showToast("База загружена и готова к поиску");
            await runSearch(true);
            void checkRemoteMeta(true);
        } catch (error) {
            catalog = null;
            password = "";
            if (!navigator.onLine) {
                setUnlockError("Соединение прервалось. Повторите загрузку, когда сеть вернётся.");
            } else {
                setUnlockError(workerErrorMessage(error));
            }
        }
    }

    async function forgetCatalog() {
        if (!state.rpc) {
            return;
        }
        const confirmed = window.confirm(
            "Удалить локальную базу курсов с этого устройства? Её можно будет загрузить заново по общему паролю.",
        );
        if (!confirmed) {
            return;
        }

        dom.forgetButton.disabled = true;
        setConnection("Удаляем локальную базу…", "working");
        try {
            await state.rpc.call("forget");
            state.cached = false;
            state.ready = false;
            state.meta = null;
            state.facets = {
                formats: [],
                categories: [],
                providers: [],
                years: [],
                hasPassword: null,
            };
            state.selectedId = null;
            state.total = 0;
            state.offset = 0;
            clearNode(dom.resultsList);
            renderFacets();
            resetDetailPlaceholder();
            setControlsEnabled(false);
            updateStatusStrip();
            setResultsState(
                "Локальная база удалена",
                "Введите общий пароль, чтобы снова загрузить snapshot.",
                "Загрузить базу",
                () => openUnlock("initial"),
            );
            showToast("Локальная база удалена");
            if (state.remoteMeta?.available) {
                openUnlock("initial");
            }
        } catch (error) {
            showToast(workerErrorMessage(error), "error");
            updateStatusStrip();
        }
    }

    function resetDetailPlaceholder() {
        clearNode(dom.desktopDetail);
        const placeholder = createElement("div", "detail-placeholder");
        placeholder.append(
            createElement("p", "pane-kicker", "Инспектор"),
            createElement("h2", "", "Информация о курсе"),
            createElement("p", "", "Выберите запись — список останется на месте, а ссылки и пароли появятся здесь."),
        );
        placeholder.querySelector("h2").id = "detail-heading";
        dom.desktopDetail.append(placeholder);

        clearNode(dom.mobileDetail);
        const mobileHeading = createElement("h2", "", "Выберите курс");
        mobileHeading.id = "mobile-detail-heading";
        dom.mobileDetail.append(mobileHeading);
    }

    async function boot() {
        bindEvents();
        syncFiltersDialog();
        updateStatusStrip();

        if (!("Worker" in window)) {
            setResultsState(
                "Браузер не поддерживается",
                "Для локального поиска нужен Web Worker. Откройте каталог в актуальной версии браузера.",
                "",
                null,
            );
            setConnection("Поисковый поток недоступен", "error");
            dom.app.setAttribute("aria-busy", "false");
            return;
        }

        try {
            const workerURL = new URL("/js/courses-search-worker.js", window.location.origin);
            if (assetVersion) {
                workerURL.searchParams.set("v", assetVersion);
            }
            state.rpc = new WorkerRPC(workerURL.toString(), updateImportProgress);
            const result = await state.rpc.call("boot");
            applyWorkerState(result);
            if (state.ready) {
                hideResultsState();
                setConnection("Локальная база готова", "ready");
                await runSearch(true);
                void checkRemoteMeta(true);
            } else {
                setResultsState(
                    "Нужна локальная база",
                    "Введите общий пароль один раз. После импорта поиск будет работать без сети.",
                    "Загрузить базу",
                    () => openUnlock("initial"),
                );
                await checkRemoteMeta();
            }
        } catch (error) {
            const corrupt = error?.code === "CACHE_CORRUPT" || error?.code === "INVALID_CACHE";
            setResultsState(
                corrupt ? "Локальная копия повреждена" : "Не удалось открыть локальную базу",
                workerErrorMessage(error),
                corrupt ? "Удалить повреждённую копию" : "Повторить",
                corrupt ? () => void forgetCatalog() : () => window.location.reload(),
            );
            setConnection("Ошибка локальной базы", "error");
            dom.app.setAttribute("aria-busy", "false");
            void checkRemoteMeta(true);
        }
    }

    function closeDetailDialog() {
        if (dom.detailDialog.open) {
            dom.detailDialog.close();
        }
    }

    function bindDetailSheet() {
        let pointerId = null;
        let startX = 0;
        let startY = 0;
        let suppressHandleClick = false;

        dom.detailDialog.addEventListener("click", (event) => {
            if (event.target === dom.detailDialog) {
                closeDetailDialog();
            }
        });

        dom.detailDialog.addEventListener("cancel", (event) => {
            event.preventDefault();
            closeDetailDialog();
        });

        dom.detailDialog.addEventListener("keydown", (event) => {
            if (event.key === "Escape") {
                event.preventDefault();
                closeDetailDialog();
            }
        });

        dom.detailDialog.addEventListener("close", () => {
            const returnFocus = state.detailReturnFocus;
            state.detailReturnFocus = null;
            if (returnFocus instanceof HTMLElement && returnFocus.isConnected) {
                window.requestAnimationFrame(() => returnFocus.focus());
            }
        });

        dom.detailDragHandle.addEventListener("pointerdown", (event) => {
            pointerId = event.pointerId;
            startX = event.clientX;
            startY = event.clientY;
            suppressHandleClick = false;
            dom.detailDragHandle.setPointerCapture(pointerId);
        });

        dom.detailDragHandle.addEventListener("pointerup", (event) => {
            if (pointerId !== event.pointerId) {
                return;
            }
            const movedDown = event.clientY - startY;
            const movedSideways = Math.abs(event.clientX - startX);
            suppressHandleClick = Math.abs(movedDown) > 8 || movedSideways > 8;
            dom.detailDragHandle.releasePointerCapture(pointerId);
            pointerId = null;
            if (movedDown >= 64 && movedDown > movedSideways) {
                closeDetailDialog();
            }
        });

        dom.detailDragHandle.addEventListener("pointercancel", () => {
            pointerId = null;
        });

        dom.detailDragHandle.addEventListener("click", (event) => {
            if (suppressHandleClick) {
                event.preventDefault();
                suppressHandleClick = false;
                return;
            }
            closeDetailDialog();
        });
    }

    function bindEvents() {
        bindDetailSheet();

        dom.searchInput.addEventListener("input", () => {
            state.query = dom.searchInput.value;
            scheduleSearch();
        });

        dom.sortSelect.addEventListener("change", () => {
            state.sort.field = dom.sortSelect.value;
            state.sort.explicit = true;
            if (state.sort.field === "title" || state.sort.field === "author") {
                state.sort.direction = "asc";
            } else {
                state.sort.direction = "desc";
            }
            syncSortDirection();
            void runSearch(true);
        });

        dom.sortDirection.addEventListener("click", () => {
            state.sort.explicit = true;
            state.sort.direction = state.sort.direction === "asc" ? "desc" : "asc";
            syncSortDirection();
            void runSearch(true);
        });

        dom.formatOptions.addEventListener("change", (event) => {
            const input = event.target;
            if (!(input instanceof HTMLInputElement) || input.name !== "format") {
                return;
            }
            if (input.checked) {
                state.filters.formats.add(input.value);
            } else {
                state.filters.formats.delete(input.value);
            }
            renderActiveFilters();
            void runSearch(true);
        });

        dom.categoryOptions.addEventListener("change", (event) => {
            const input = event.target;
            if (!(input instanceof HTMLInputElement) || input.name !== "category") {
                return;
            }
            if (input.checked) {
                state.filters.categories.add(input.value);
            } else {
                state.filters.categories.delete(input.value);
            }
            renderActiveFilters();
            void runSearch(true);
        });

        dom.providerSelect.addEventListener("change", () => {
            state.filters.provider = dom.providerSelect.value;
            renderActiveFilters();
            void runSearch(true);
        });

        dom.yearSelect.addEventListener("change", () => {
            state.filters.year = dom.yearSelect.value;
            renderActiveFilters();
            void runSearch(true);
        });

        dom.passwordFilter.addEventListener("change", () => {
            state.filters.hasPassword = dom.passwordFilter.checked ? true : null;
            renderActiveFilters();
            void runSearch(true);
        });

        dom.resetFilters.addEventListener("click", resetFilters);
        dom.filtersButton.addEventListener("click", openFilters);
        dom.filtersClose.addEventListener("click", closeFilters);
        dom.applyFilters.addEventListener("click", closeFilters);
        dom.filtersDialog.addEventListener("click", (event) => {
            if (
                isDesktop()
                || !dom.filtersDialog.open
                || event.target !== dom.filtersDialog
            ) {
                return;
            }

            const bounds = dom.filtersDialog.getBoundingClientRect();
            const outsidePanel = event.clientX < bounds.left
                || event.clientX > bounds.right
                || event.clientY < bounds.top
                || event.clientY > bounds.bottom;
            if (outsidePanel) {
                event.preventDefault();
                closeFilters();
            }
        });
        dom.filtersDialog.addEventListener("cancel", (event) => {
            if (isDesktop()) {
                event.preventDefault();
            }
        });
        dom.filtersDialog.addEventListener("close", () => {
            state.filtersModal = false;
        });

        dom.updateButton.addEventListener("click", async () => {
            dom.settingsMenu.open = false;
            await checkRemoteMeta(true);
            openUnlock("update");
        });
        dom.forgetButton.addEventListener("click", () => {
            dom.settingsMenu.open = false;
            void forgetCatalog();
        });

        dom.stateAction.addEventListener("click", () => {
            if (state.stateAction) {
                state.stateAction();
            }
        });

        dom.passwordToggle.addEventListener("click", () => {
            const show = dom.passwordInput.type === "password";
            dom.passwordInput.type = show ? "text" : "password";
            dom.passwordToggle.textContent = show ? "Скрыть" : "Показать";
            dom.passwordToggle.setAttribute("aria-label", show ? "Скрыть пароль" : "Показать пароль");
            dom.passwordInput.focus();
        });

        dom.unlockCancel.addEventListener("click", () => {
            if (dom.unlockSubmit.disabled) {
                return;
            }
            dom.passwordInput.value = "";
            state.unlockMode = "idle";
            dom.unlockDialog.close();
        });

        dom.unlockDialog.addEventListener("cancel", (event) => {
            if (!state.cached || dom.unlockSubmit.disabled) {
                event.preventDefault();
                return;
            }
            dom.passwordInput.value = "";
            state.unlockMode = "idle";
        });

        dom.unlockForm.addEventListener("submit", (event) => {
            event.preventDefault();
            const password = dom.passwordInput.value;
            if (!password) {
                setUnlockError("Введите общий пароль.");
                return;
            }
            void importCatalog(password);
        });

        document.addEventListener("pointerdown", (event) => {
            if (dom.settingsMenu.open && !dom.settingsMenu.contains(event.target)) {
                dom.settingsMenu.open = false;
            }
        });

        document.addEventListener("keydown", (event) => {
            if (event.key === "Escape" && dom.settingsMenu.open) {
                event.preventDefault();
                dom.settingsMenu.open = false;
                dom.settingsButton.focus();
                return;
            }
            if (
                (event.metaKey || event.ctrlKey)
                && event.key.toLowerCase() === "k"
                && state.ready
            ) {
                event.preventDefault();
                dom.searchInput.focus();
                dom.searchInput.select();
            }
        });

        window.addEventListener("online", updateOnlineStatus);
        window.addEventListener("offline", updateOnlineStatus);
        window.addEventListener("resize", debounce(syncFiltersDialog, 120));

        const observer = new IntersectionObserver((entries) => {
            if (entries.some((entry) => entry.isIntersecting)) {
                void runSearch(false);
            }
        }, { rootMargin: "500px 0px" });
        observer.observe(dom.resultsSentinel);
    }

    function syncSortDirection() {
        const ascending = state.sort.direction === "asc";
        dom.sortDirection.querySelector("span").textContent = ascending ? "↑" : "↓";
        dom.sortDirection.setAttribute(
            "aria-label",
            `Направление сортировки: ${ascending ? "по возрастанию" : "по убыванию"}`,
        );
    }

    function syncAutomaticSort() {
        if (state.sort.explicit) {
            return;
        }
        state.sort.field = state.query.trim() ? "relevance" : "added_at";
        state.sort.direction = "desc";
        dom.sortSelect.value = state.sort.field;
        syncSortDirection();
    }

    function debounce(callback, delay) {
        let timer = 0;
        return (...args) => {
            window.clearTimeout(timer);
            timer = window.setTimeout(() => callback(...args), delay);
        };
    }

    void boot();
})();
