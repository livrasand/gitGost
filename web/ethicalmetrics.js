(function () {
    const script = document.currentScript;
    if (!script || !script.dataset.siteKey || navigator.doNotTrack === "1" || window.doNotTrack === "1") return;
    const agent = navigator.userAgent || "";
    const device = /Tablet|iPad/i.test(agent) ? "Tablet" : /Mobi|Android/i.test(agent) ? "Mobile" : "Desktop";
    const browser = /Edg\//i.test(agent) ? "Edge" : /Firefox\//i.test(agent) ? "Firefox" : /Chrome\//i.test(agent) ? "Chrome" : /Safari\//i.test(agent) ? "Safari" : "Other";
    const os = /Windows/i.test(agent) ? "Windows" : /Android/i.test(agent) ? "Android" : /iPhone|iPad|iPod/i.test(agent) ? "iOS" : /Mac OS X|Macintosh/i.test(agent) ? "macOS" : /Linux/i.test(agent) ? "Linux" : "Other";
    const body = JSON.stringify({ site_key: script.dataset.siteKey, path: window.location.pathname || "/", referrer: document.referrer || "", browser, os, device });
    fetch(script.dataset.endpoint || "/v1/pageviews", { method: "POST", headers: { "Content-Type": "application/json" }, body, keepalive: true }).catch(() => {});
})();
