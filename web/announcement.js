/*
 * gitGost announcement banner — fetched once per page load from
 * /api/announcement. The server keeps exactly one active announcement (fed
 * by a private ntfy/UnifiedPush topic); the browser only ever sees the
 * sanitized payload. Dismissal is stored in localStorage per announcement
 * id: no cookies, no accounts, no tracking.
 *
 * Supported types: note | tip | important | warning | caution
 */
(function () {
    'use strict';

    var STORAGE_PREFIX = 'gitgost_announcement_dismissed_';
    var EXPIRED_PREFIX = 'gitgost_announcement_dismissed:';
    var VALID_TYPES = ['note', 'tip', 'important', 'warning', 'caution'];

    /* Visual treatments per announcement type, mirroring the
     * .suspension-banner design (monospace, tinted background + colored
     * border, inline icon). Injected once, idempotent. */
    function injectStyles() {
        if (document.getElementById('announcement-style')) return;
        var css = ''
            + '.announcement-banner{display:flex;align-items:center;gap:.75rem;flex-wrap:wrap;'
            + 'padding:.75rem 1rem;margin-bottom:1rem;border-radius:0;'
            + 'font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;'
            + 'font-size:.78rem;color:#155eef;border:1px solid #3b82f6;background:#e8f0fe;}'
            + '.announcement-banner .announcement-icon{flex-shrink:0;font-size:1.2rem;}'
            + '.announcement-banner .announcement-content{flex:1;}'
            + '.announcement-banner .announcement-content strong{font-weight:600;display:block;margin-bottom:.25rem;}'
            + '.announcement-banner .announcement-link{color:inherit;font-weight:600;text-decoration:underline;white-space:nowrap;}'
            + '.announcement-banner .announcement-close{flex-shrink:0;background:transparent;border:0;'
            + 'font:inherit;font-size:1rem;line-height:1;cursor:pointer;color:inherit;opacity:.7;padding:.25rem .5rem;}'
            + '.announcement-banner .announcement-close:hover,.announcement-banner .announcement-close:focus-visible{opacity:1;}'
            + '.announcement-tip{color:#14602d;border-color:#2eb75b;background:#eaf6ee;}'
            + '.announcement-important{color:#5b21b6;border-color:#8b5cf6;background:#f2ecfd;}'
            + '.announcement-warning{color:#8a5303;border-color:#ffc107;background:#fff3cd;}'
            + '.announcement-caution{color:#95261f;border-color:#e2524a;background:#fdeceb;}'
            + ':root[data-theme="dark"] .announcement-banner{color:#cfe0ff;border-color:#6d9eeb;background:#1d2b47;}'
            + ':root[data-theme="dark"] .announcement-tip{color:#c9ecd1;border-color:#57c079;background:#15301e;}'
            + ':root[data-theme="dark"] .announcement-important{color:#e2d3fb;border-color:#a678f7;background:#2b1e46;}'
            + ':root[data-theme="dark"] .announcement-warning{color:#ffe6a7;border-color:#f76f53;background:#663d03;}'
            + ':root[data-theme="dark"] .announcement-caution{color:#ffd0cb;border-color:#ef7c74;background:#471a16;}'
            + '@media (prefers-color-scheme:dark){'
            + ':root:not([data-theme]) .announcement-banner{color:#cfe0ff;border-color:#6d9eeb;background:#1d2b47;}'
            + ':root:not([data-theme]) .announcement-tip{color:#c9ecd1;border-color:#57c079;background:#15301e;}'
            + ':root:not([data-theme]) .announcement-important{color:#e2d3fb;border-color:#a678f7;background:#2b1e46;}'
            + ':root:not([data-theme]) .announcement-warning{color:#ffe6a7;border-color:#f76f53;background:#663d03;}'
            + ':root:not([data-theme]) .announcement-caution{color:#ffd0cb;border-color:#ef7c74;background:#471a16;}}'
            + '@media (max-width:768px){.announcement-banner{flex-direction:column;align-items:flex-start;gap:.4rem;}'
            + '.announcement-banner .announcement-close{position:absolute;top:.5rem;right:.5rem;}}';
        var style = document.createElement('style');
        style.id = 'announcement-style';
        style.textContent = css;
        document.head.appendChild(style);
    }

    /* Icon per announcement type, glyph-first like the suspension banner's
     * inline SVG: simple, no network, inherits color via currentColor. */
    var TYPE_ICONS = {
        note: '&#9432;',       /* ⓘ information circle */
        tip: '&#128161;',      /* 💡 lightbulb */
        important: '&#10067;', /* ❓ question mark, crucial to read */
        warning: '&#9888;',    /* ⚠ warning triangle */
        caution: '&#9889;'     /* ⚡ zap / hazard */
    };

    function safeGet(key) {
        try { return localStorage.getItem(key); } catch (e) { return null; }
    }
    function safeSet(key, value) {
        try { localStorage.setItem(key, value); } catch (e) { /* private mode */ }
    }

    /* Lazily drop stale dismissal entries (id:... kept for legacy keys). */
    function pruneOldDismissals(activeId) {
        try {
            for (var i = localStorage.length - 1; i >= 0; i--) {
                var k = localStorage.key(i);
                if (k && k.indexOf(STORAGE_PREFIX) === 0 && k !== STORAGE_PREFIX + activeId) {
                    localStorage.removeItem(k);
                }
            }
        } catch (e) { /* ignore */ }
    }

    function render(a) {
        if (!a || !a.title || !a.message) return;
        if (!a.id) return;
        if (safeGet(STORAGE_PREFIX + a.id) === '1') return;

        pruneOldDismissals(a.id);

        var type = VALID_TYPES.indexOf(a.type) !== -1 ? a.type : 'note';

        var banner = document.createElement('div');
        banner.className = 'announcement-banner announcement-' + type;
        banner.setAttribute('role', 'status');

        var icon = document.createElement('span');
        icon.className = 'announcement-icon';
        icon.setAttribute('aria-hidden', 'true');
        icon.innerHTML = TYPE_ICONS[type] || TYPE_ICONS.note;
        banner.appendChild(icon);

        var content = document.createElement('div');
        content.className = 'announcement-content';
        var title = document.createElement('strong');
        title.textContent = a.title;
        content.appendChild(title);
        content.appendChild(document.createTextNode(a.message));

        banner.appendChild(content);

        if (a.link && typeof a.link === 'string' && a.link.indexOf('https://github.com/') === 0) {
            var link = document.createElement('a');
            link.href = a.link;
            link.rel = 'noopener noreferrer';
            link.target = '_blank';
            link.className = 'announcement-link';
            link.textContent = 'View issue \u2192';
            banner.appendChild(link);
        }

        var close = document.createElement('button');
        close.className = 'announcement-close';
        close.type = 'button';
        close.setAttribute('aria-label', 'Dismiss announcement');
        close.textContent = '\u00d7';
        close.addEventListener('click', function () {
            safeSet(STORAGE_PREFIX + a.id, '1');
            banner.remove();
        });
        banner.appendChild(close);

        var first = document.body.firstChild;
        if (first) {
            document.body.insertBefore(banner, first);
        } else {
            document.body.appendChild(banner);
        }
    }

    function init() {
        injectStyles();
        fetch('/api/announcement', { credentials: 'omit' })
            .then(function (res) {
                if (!res.ok || res.status === 204) return null;
                return res.json();
            })
            .then(render)
            .catch(function () { /* silent: banner is optional */ });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
