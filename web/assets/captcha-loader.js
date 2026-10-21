"use strict";
(function () {
    function apiBase() {
        try {
            if (typeof window.Capacitor !== "undefined" &&
                window.Capacitor.isNativePlatform &&
                window.Capacitor.isNativePlatform()) {
                return "https://gitgost.livrasand.com";
            }
        } catch (e) {}
        return window.location.origin;
    }
    function originOf(url) {
        if (!url) return "";
        try { return new URL(url, window.location.href).origin; } catch (e) { return ""; }
    }
    var state = { enabled: false, api: "", proxy: "", error: null };
    window.gitgostCaptcha = state;

    // Menta solo admite peticiones de navegador desde origenes registrados en
    // su instancia; para el resto responde 403 y sin cabeceras CORS. gitGost
    // no esta registrado alli, asi que el navegador NUNCA habla con Menta
    // directamente: el widget y el flujo /shield van siempre por el proxy de
    // mismo origen (/api/captcha), que reenvia la llamada servidor-a-servidor.
    function proxyEndpoint() {
        return apiBase().replace(/\/+$/, "") + "/api/captcha";
    }
    state.proxy = proxyEndpoint();
    window.gitgostCaptchaEndpoint = function () { return state.proxy; };

    // El widget lee data-cap-api-endpoint al crear su sesion, asi que el
    // atributo tiene que estar puesto ANTES de que el elemento se conecte al
    // documento (o antes de que el SDK lo defina).
    function applyEndpoint(root) {
        var scope = (root && root.querySelectorAll) ? root : document;
        var widgets = scope.querySelectorAll("menta-widget");
        for (var i = 0; i < widgets.length; i++) {
            widgets[i].setAttribute("data-cap-api-endpoint", state.proxy);
        }
    }
    state.applyEndpoint = applyEndpoint;

    function watchWidgets() {
        if (!window.MutationObserver || state.observer) return;
        state.observer = new MutationObserver(function (mutations) {
            for (var i = 0; i < mutations.length; i++) {
                var added = mutations[i].addedNodes;
                for (var j = 0; j < added.length; j++) {
                    var node = added[j];
                    if (!node || node.nodeType !== 1) continue;
                    if (node.tagName === "MENTA-WIDGET") {
                        node.setAttribute("data-cap-api-endpoint", state.proxy);
                    }
                    if (node.querySelectorAll) applyEndpoint(node);
                }
            }
        });
        state.observer.observe(document.documentElement || document, { childList: true, subtree: true });
    }

    // El SDK declara sus clases en el ambito global: cargarlo dos veces en la
    // misma pagina lanza "Can't create duplicate variable: 'CapWidget'". Se
    // busca cualquier copia ya presente antes de insertar una nueva.
    function existingSdkScript(src) {
        var scripts = document.querySelectorAll("script[src]");
        for (var i = 0; i < scripts.length; i++) {
            var s = scripts[i].getAttribute("src") || "";
            if (!s) continue;
            if (originOf(s) === originOf(src) && /menta-captcha\.js(\?|#|$)/.test(s)) return scripts[i];
        }
        return null;
    }

    function loadSdk(src) {
        return new Promise(function (resolve, reject) {
            if (window.customElements && window.customElements.get("menta-widget")) { resolve(); return; }
            var existing = document.getElementById("menta-captcha-sdk") || existingSdkScript(src);
            if (existing) {
                var loaded = existing.getAttribute("data-loaded") === "1" || existing.readyState === "complete";
                if (loaded) {
                    waitForWidget().then(resolve, reject);
                    return;
                }
                existing.addEventListener("load", function () { resolve(); }, { once: true });
                existing.addEventListener("error", function () { reject(new Error("sdk-load-failed")); }, { once: true });
                return;
            }
            var s = document.createElement("script");
            s.id = "menta-captcha-sdk";
            s.src = src;
            s.onload = function () { s.setAttribute("data-loaded", "1"); resolve(); };
            s.onerror = function () { reject(new Error("sdk-load-failed")); };
            (document.head || document.documentElement).appendChild(s);
        });
    }

    function waitForWidget() {
        if (!window.customElements || !window.customElements.whenDefined) return Promise.reject(new Error("no-custom-elements"));
        return Promise.race([
            window.customElements.whenDefined("menta-widget"),
            new Promise(function (resolve, reject) { window.setTimeout(function () { reject(new Error("widget-timeout")); }, 15000); })
        ]);
    }

    var readyPromise = null;
    function init() {
        if (readyPromise) return readyPromise;
        state.error = null;
        state.proxy = proxyEndpoint();
        readyPromise = fetch(apiBase() + "/api/config", { credentials: "same-origin" })
            .then(function (r) { if (!r.ok) throw new Error("config-http-" + r.status); return r.json(); })
            .then(function (cfg) {
                state.enabled = Boolean(cfg && cfg.captcha_gate);
                if (!state.enabled) {
                    var style = document.createElement("style");
                    style.id = "gitgost-captcha-hide";
                    style.textContent = "menta-widget{ display: none !important; }";
                    document.head.appendChild(style);
                    return false;
                }
                var cfgBase = originOf(cfg && cfg.menta_api);
                if (!cfgBase) throw new Error("menta-api-unknown");
                // El endpoint del proxy se fija antes de cargar el SDK: al
                // definirse el elemento, el widget ya abre su sesion contra
                // el proxy (nunca contra Menta, que rechazaria el origen).
                applyEndpoint(document);
                watchWidgets();
                return loadSdk(cfgBase + "/menta-captcha.js")
                    .then(waitForWidget)
                    .then(function () {
                        var sdk = document.getElementById("menta-captcha-sdk");
                        state.api = originOf(sdk && sdk.getAttribute("src")) || cfgBase;
                        applyEndpoint(document);
                        patchWidget();
                        return true;
                    });
            })
            .catch(function (err) { state.error = err; readyPromise = null; return false; });
        return readyPromise;
    }
    state.init = init;
    window.gitgostCaptchaReady = init();

    async function sha256Hex(message) {
        var bytes = new TextEncoder().encode(message);
        var digest = await crypto.subtle.digest("SHA-256", bytes);
        var view = new Uint8Array(digest);
        var out = "";
        for (var i = 0; i < view.length; i++) out += view[i].toString(16).padStart(2, "0");
        return out;
    }
    // Coste esperado del reto: 16^dificultad intentos. Menta sube la
    // dificultad cuando la IP acumula fallos, y un reto de 6 ceros (16
    // millones de hashes) no se resuelve en un navegador: en ese caso se
    // cambia al flujo /shield, que devuelve un reto mas suave.
    var POW_MAX_COST = 2000000;
    var POW_BUDGET_MS = 20000;

    function powCost(target) {
        return Math.pow(16, String(target || "").length);
    }
    // El PoW se resuelve en lotes: WebCrypto digiere en paralelo (hilo del
    // navegador) y así un reto de 65k hashes tarda milisegundos en lugar de
    // los varios segundos que costaría resolverlos de uno en uno.
    // Menta descarta los retos resueltos en menos de 0,2 s (no es humano) y
    // marca como bots las series de tiempos idénticos, así que tras encontrar
    // el nonce se espera a que el reto tenga una duración humana y con algo
    // de dispersion.
    var POW_MIN_SOLVE_MS = 300;
    var POW_JITTER_MS = 400;
    async function solvePow(salt, target, budgetMs) {
        var cost = powCost(target);
        if (cost > POW_MAX_COST) throw new Error("pow-too-hard");
        var started = Date.now();
        var deadline = started + (budgetMs || POW_BUDGET_MS);
        var nonce = 0;
        var batch = 256;
        while (Date.now() < deadline) {
            var jobs = new Array(batch);
            for (var i = 0; i < batch; i++) jobs[i] = sha256Hex(salt + (nonce + i));
            var hashes = await Promise.all(jobs);
            for (var j = 0; j < hashes.length; j++) {
                if (hashes[j].indexOf(target) === 0) {
                    var elapsed = Date.now() - started;
                    var wait = POW_MIN_SOLVE_MS - elapsed + Math.floor(Math.random() * POW_JITTER_MS);
                    if (wait > 0) await new Promise(function (resolve) { setTimeout(resolve, wait); });
                    return nonce + j;
                }
            }
            nonce += batch;
        }
        throw new Error("pow-timeout");
    }
    async function postJson(url, payload) {
        var response = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
        var data = null;
        try { data = await response.json(); } catch (e) { data = null; }
        return { ok: response.ok, status: response.status, data: data };
    }
    async function solveWithShield(base, path) {
        var check = await postJson(base + "/shield/check", { path: path || "/", force: true });
        if (!check.ok || !check.data || !check.data.challenge) throw new Error("shield-check-failed:" + check.status);
        var challenge = check.data.challenge;
        var nonce = await solvePow(challenge.salt, challenge.target);
        var redeem = await postJson(base + "/shield/redeem", { token: check.data.token, solutions: nonce, challenge_signature: challenge.challenge_signature });
        if (!redeem.ok || !redeem.data || !redeem.data.clearance_token) throw new Error("shield-redeem-failed:" + redeem.status);
        return redeem.data.clearance_token;
    }
    // Flujo completo de Menta contra el proxy: /init -> /challenge -> PoW ->
    // /redeem. Se hace aqui (y no solo con el widget) porque Menta exige al
    // menos 0,5 s entre /init y /challenge: el widget abriria su sesion y
    // pediria el reto nada mas tocarlo, lo que Menta rechaza con 403.
    async function solveWithSession(base) {
        var init = await postJson(base + "/init", {});
        if (!init.ok || !init.data || !init.data.session_id) throw new Error("init-failed:" + init.status);
        var sessionId = init.data.session_id;
        var signature = init.data.session_signature;
        await new Promise(function (resolve) { setTimeout(resolve, 700); });
        var challenge = await postJson(base + "/challenge", { session_id: sessionId, session_signature: signature });
        if (!challenge.ok || !challenge.data || !challenge.data.challenge) throw new Error("challenge-failed:" + challenge.status);
        var payload = challenge.data;
        var nonce = await solvePow(payload.challenge.salt, payload.challenge.target);
        var redeem = await postJson(base + "/redeem", {
            session_id: sessionId,
            token: payload.token,
            solutions: nonce,
            challenge_signature: payload.challenge.challenge_signature
        });
        if (!redeem.ok || !redeem.data || !redeem.data.token) throw new Error("redeem-failed:" + redeem.status);
        return redeem.data.token;
    }
    // Resuelve el reto contra el proxy local (mismo origen: sin CORS).
    // El flujo universal /shield de Menta va primero: esta pensado para
    // paginas que no estan registradas en su instancia (nuestro caso) y su
    // reto es mas barato. Si Menta responde con un reto demasiado caro
    // para el navegador, se prueba el flujo de sesion (/init -> /challenge).
    async function solveProofOfWork(path, allowShield) {
        var attempts = [];
        if (allowShield) {
            try {
                var clearance = await solveWithShield(state.proxy, path);
                return { token: clearance, kind: "shield", via: "proxy" };
            } catch (e) { attempts.push("shield:" + (e && e.message)); }
        }
        try {
            var token = await solveWithSession(state.proxy);
            return { token: token, kind: "captcha", via: "proxy" };
        } catch (e) { attempts.push("session:" + (e && e.message)); }
        var error = new Error("captcha-failed");
        error.attempts = attempts;
        throw error;
    }

    state.solve = async function (options) {
        options = options || {};
        var widget = options.widget || (options.widgetId ? document.getElementById(options.widgetId) : null);
        var path = options.path || (window.location.pathname + window.location.search);
        // Un token ya resuelto (el visitante pulso el widget) se reutiliza.
        if (widget && widget.token) return { token: widget.token, kind: "captcha", via: "widget" };
        return solveProofOfWork(path, options.allowShield !== false);
    };

    // El widget de Menta resuelve el reto por su cuenta, sin limite de tiempo
    // y con el PoW secuencial: con la dificultad que Menta aplica a las IP con
    // fallos recientes, el navegador puede quedarse minutos calculando. Se
    // sustituye su metodo solve() por el flujo con presupuesto de tiempo de
    // este cargador y se anula su sesion (el widget no necesita abrirla: la
    // abre solveWithSession).
    function setWidgetLabel(widget, text, solved) {
        var shadow = widget && widget.shadowRoot;
        if (!shadow) return;
        var label = shadow.getElementById("label");
        if (label) label.innerText = text;
        var spin = shadow.getElementById("spin");
        if (spin) spin.style.display = "none";
        var box = shadow.getElementById("box");
        if (box) {
            box.setAttribute("aria-label", text);
            box.setAttribute("aria-busy", "false");
            if (solved) box.classList.add("solved");
        }
    }

    function patchWidget() {
        var ctor = window.customElements && window.customElements.get("menta-widget");
        if (!ctor || !ctor.prototype || ctor.prototype.__gitgostPatched) return;
        ctor.prototype.__gitgostPatched = true;
        ctor.prototype.initSession = async function () { return null; };
        ctor.prototype.solve = async function () {
            var self = this;
            self.token = null;
            try {
                var result = await solveProofOfWork(window.location.pathname + window.location.search, true);
                self.token = result.token;
                self.sessionId = null;
                self.sessionSignature = null;
                setWidgetLabel(self, "You're human", true);
                self.dispatchEvent(new CustomEvent("solve", { detail: { token: self.token }, bubbles: true, composed: true }));
            } catch (e) {
                setWidgetLabel(self, "Error. Try again.", false);
                self.dispatchEvent(new CustomEvent("solve-error", { detail: { error: String((e && e.message) || e) }, bubbles: true, composed: true }));
            }
        };
    }
    window.gitgostCaptchaToken = function (widgetId) {
        return window.gitgostCaptchaReady.then(function (enabled) {
            if (!enabled) return "";
            var widget = widgetId ? document.getElementById(widgetId) : null;
            return widget && widget.token ? widget.token : null;
        });
    };
})();
