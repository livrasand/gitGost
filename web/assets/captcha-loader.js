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
    function loadSdk(src) {
        return new Promise(function (resolve, reject) {
            var existing = document.getElementById("menta-captcha-sdk");
            if (existing) {
                if (existing.getAttribute("data-loaded") === "1") { resolve(); return; }
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
    function applyDirectEndpoint(base) {
        if (!base) return;
        var widgets = document.querySelectorAll("menta-widget[data-cap-direct]");
        for (var i = 0; i < widgets.length; i++) widgets[i].setAttribute("data-cap-api-endpoint", base);
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
        state.proxy = apiBase() + "/api/captcha";
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
                applyDirectEndpoint(cfgBase);
                return loadSdk(cfgBase + "/menta-captcha.js")
                    .then(waitForWidget)
                    .then(function () {
                        var sdk = document.getElementById("menta-captcha-sdk");
                        state.api = originOf(sdk && sdk.getAttribute("src")) || cfgBase;
                        applyDirectEndpoint(state.api);
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
    async function solvePow(salt, target) {
        var nonce = 0;
        for (;;) {
            var hash = await sha256Hex(salt + nonce);
            if (hash.indexOf(target) === 0) return nonce;
            nonce++;
            if (nonce > 200000000) throw new Error("pow-failed");
        }
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
    async function solveWithWidget(widget, base) {
        if (!widget || typeof widget.solve !== "function") throw new Error("no-widget");
        widget.setAttribute("data-cap-api-endpoint", base);
        widget.token = null;
        widget.sessionId = null;
        widget.sessionSignature = null;
        if (typeof widget.render === "function") { try { widget.render(); } catch (e) {} }
        await widget.solve();
        return widget.token || null;
    }
    state.solve = async function (options) {
        options = options || {};
        var widget = options.widget || (options.widgetId ? document.getElementById(options.widgetId) : null);
        var path = options.path || (window.location.pathname + window.location.search);
        var attempts = [];
        async function tryWidget(base, label) {
            try {
                var token = await solveWithWidget(widget, base);
                if (token) return { token: token, kind: "captcha", via: label };
                attempts.push(label + ":empty");
            } catch (e) { attempts.push(label + ":" + (e && e.message)); }
            return null;
        }
        var result = null;
        var direct = !!(widget && widget.hasAttribute && widget.hasAttribute("data-cap-direct"));
        if (direct && state.api) result = await tryWidget(state.api, "menta");
        if (!result && state.api && options.allowShield) {
            try {
                var clearance = await solveWithShield(state.api, path);
                result = { token: clearance, kind: "shield", via: "menta" };
            } catch (e) { attempts.push("shield/menta:" + (e && e.message)); }
        }
        if (!result) result = await tryWidget(state.proxy || (apiBase() + "/api/captcha"), "proxy");
        if (!result) { var error = new Error("captcha-failed"); error.attempts = attempts; throw error; }
        return result;
    };
    window.gitgostCaptchaToken = function (widgetId) {
        return window.gitgostCaptchaReady.then(function (enabled) {
            if (!enabled) return "";
            var widget = widgetId ? document.getElementById(widgetId) : null;
            return widget && widget.token ? widget.token : null;
        });
    };
})();
