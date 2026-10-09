/*
 * PinPoint's review script. It runs inside the review frame, in the page
 * being reviewed, ahead of the page's own scripts, and talks to the editor
 * around the frame with postMessage.
 *
 * It does four things:
 *   - keeps the page working although PinPoint serves it: links go through
 *     the frame, the page's own data requests through PinPoint, and the
 *     address the page sees is its real one;
 *   - draws the annotations on the page, attached to the elements they are
 *     about, so they follow the layout;
 *   - lets the reviewer mark the page: pins, highlights, boxes, drawings;
 *   - describes what was marked (a CSS selector, the text, the HTML) and
 *     takes a screenshot of it.
 *
 * The page has an origin of its own that matches nothing (the frame is
 * sandboxed), so none of this can reach PinPoint's pages, and the page's
 * scripts can see this script's work but nothing of the editor's.
 */
(function () {
  'use strict';
  var cfg = window.__pinpoint;
  if (!cfg || window.__pinpointAgent) return;
  window.__pinpointAgent = true;

  var ORIGIN = cfg.origin;
  var realPage = new URL(cfg.page);
  var nativeFetch = window.fetch ? window.fetch.bind(window) : null;
  var COLORS = { change: '#206bc4', bug: '#d63939', question: '#f76707', like: '#2fb344' };

  function tell(type, data) {
    var m = { pp: type };
    for (var k in data || {}) m[k] = data[k];
    try { parent.postMessage(m, ORIGIN); } catch (e) { /* the editor is gone */ }
  }

  // ---- What the sandbox takes away --------------------------------------
  // A sandboxed page may not use storage or cookies, and many scripts stop
  // dead when they try. Give them a memory that lasts as long as the page.

  function memoryStorage() {
    var m = new Map();
    return {
      get length() { return m.size; },
      key: function (i) { return Array.from(m.keys())[i] || null; },
      getItem: function (k) { k = String(k); return m.has(k) ? m.get(k) : null; },
      setItem: function (k, v) { m.set(String(k), String(v)); },
      removeItem: function (k) { m.delete(String(k)); },
      clear: function () { m.clear(); }
    };
  }
  ['localStorage', 'sessionStorage'].forEach(function (name) {
    try { window[name].length; } catch (e) {
      try { Object.defineProperty(window, name, { value: memoryStorage(), configurable: true }); } catch (e2) {}
    }
  });
  try { document.cookie; } catch (e) {
    var jar = {};
    try {
      Object.defineProperty(document, 'cookie', {
        configurable: true,
        get: function () { return Object.keys(jar).map(function (k) { return k + '=' + jar[k]; }).join('; '); },
        set: function (v) {
          var pair = String(v).split(';')[0], i = pair.indexOf('=');
          if (i > 0) jar[pair.slice(0, i).trim()] = pair.slice(i + 1).trim();
        }
      });
    } catch (e2) {}
  }

  // ---- Addresses ----------------------------------------------------------

  function siteKey(host) { return String(host).toLowerCase().replace(/^www\./, ''); }
  function sameSite(u) { return siteKey(u.host) === siteKey(cfg.site); }
  function frameURL(u) { return ORIGIN + '/f/' + cfg.frame + '?url=' + encodeURIComponent(u); }
  function resourceURL(u) { return ORIGIN + '/x/' + cfg.frame + '?url=' + encodeURIComponent(u); }
  function ownURL(u) { return u.origin === ORIGIN && /^\/(f|x|static)\//.test(u.pathname); }

  // toSite is where an address on the page really leads. Relative ones
  // resolve against the <base>, which is the real site. One on PinPoint's
  // origin, which is what the page believes it is on, means the same path on
  // the real site.
  function toSite(input) {
    var u = new URL(String(input), document.baseURI);
    if (u.origin === ORIGIN && !ownURL(u)) u = new URL(u.pathname + u.search + u.hash, realPage.origin);
    return u;
  }

  // pageKey is what makes two addresses the same page: no fragment, no
  // trailing slash, and www. or not.
  function pageKey(raw) {
    try {
      var u = new URL(raw);
      var path = u.pathname.length > 1 ? u.pathname.replace(/\/+$/, '') : u.pathname;
      return siteKey(u.host) + path + u.search;
    } catch (e) { return String(raw); }
  }

  // The page is served at /f/<key>?url=..., which a router would not
  // recognise. Relabel the address as the real path, on PinPoint's origin
  // (history may not leave it), so location.pathname is what the page
  // expects.
  var current = realPage.href;
  var relabelled = false;
  var H = history, nativePush = H.pushState, nativeReplace = H.replaceState;
  try {
    nativeReplace.call(H, H.state, '', ORIGIN + realPage.pathname + realPage.search + realPage.hash);
    relabelled = true;
  } catch (e) { /* the page keeps the frame's address */ }

  function locationNow() {
    return relabelled ? new URL(location.pathname + location.search + location.hash, realPage.origin).href : current;
  }

  function moved(before) {
    if (pageKey(current) !== pageKey(before)) {
      setTimeout(function () { tell('nav', pageInfo()); }, 0);
    }
    layoutSoon();
  }

  function historyCall(fn, args) {
    if (args.length < 3 || args[2] === undefined || args[2] === null) return fn.apply(H, args);
    var url;
    try { url = toSite(args[2]); } catch (e) { return fn.apply(H, args); }
    if (!sameSite(url)) return fn.call(H, args[0], args[1]);
    var r = relabelled ? fn.call(H, args[0], args[1], ORIGIN + url.pathname + url.search + url.hash) : fn.call(H, args[0], args[1]);
    var before = current;
    current = url.href;
    moved(before);
    return r;
  }
  H.pushState = function () { return historyCall(nativePush, arguments); };
  H.replaceState = function () { return historyCall(nativeReplace, arguments); };
  function followLocation() {
    var before = current;
    current = locationNow();
    moved(before);
  }
  window.addEventListener('popstate', followLocation);
  window.addEventListener('hashchange', followLocation);

  // ---- Leaving the page ---------------------------------------------------
  // Links lead through the frame, or out to a new tab if they leave the
  // site. Left to themselves they would go to the real site, which refuses
  // to be framed.

  function go(url) {
    tell('leaving', { url: url });
    location.href = frameURL(url);
  }

  function samePage(u) { return pageKey(u.href) === pageKey(current); }

  window.addEventListener('click', function (e) {
    if (e.defaultPrevented || e.button !== 0 || mode === 'annotate') return;
    var a = e.target && e.target.closest && e.target.closest('a[href], area[href]');
    if (!a) return;
    var href = a.getAttribute('href');
    if (!href || /^\s*javascript:/i.test(href)) return;
    var url;
    try { url = toSite(href); } catch (err) { return; }
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return; // mailto:, tel:
    e.preventDefault();
    if (a.hasAttribute('download')) { tell('notice', { text: 'Downloads are switched off while reviewing.' }); return; }
    if (!sameSite(url) || e.ctrlKey || e.metaKey || e.shiftKey) {
      window.open(url.href, '_blank', 'noopener');
      return;
    }
    if (samePage(url)) {
      if (url.hash) location.hash = url.hash;
      else go(url.href);
      return;
    }
    go(url.href);
  });

  // Nothing typed into a form is ever sent to the site: the sandbox stops
  // it before the page even hears of it, so say why nothing happened. A
  // search form (GET) only goes to another page, so that one is followed.
  function submitted(form, e) {
    e.preventDefault();
    var method = (form.getAttribute('method') || 'get').toLowerCase();
    var action;
    try { action = toSite(form.getAttribute('action') || current); } catch (err) { return; }
    if (method === 'get' && sameSite(action)) {
      action.search = new URLSearchParams(new FormData(form)).toString();
      go(action.href);
      return;
    }
    tell('notice', { text: "Forms aren't sent while reviewing, so nothing reached the site." });
  }
  window.addEventListener('click', function (e) {
    if (e.defaultPrevented || mode === 'annotate') return;
    var b = e.target && e.target.closest && e.target.closest('button, input[type=submit], input[type=image]');
    if (!b || !b.form || (b.localName === 'button' && (b.getAttribute('type') || 'submit').toLowerCase() !== 'submit')) return;
    submitted(b.form, e);
  });
  window.addEventListener('submit', function (e) {
    if (!e.defaultPrevented && mode !== 'annotate') submitted(e.target, e);
  });

  // The page's own requests for data from its site go through PinPoint: from
  // the sandbox the site would refuse them. Only reads (GET) do; anything that
  // would change something on the site stays as it was and fails.
  function proxied(method, url) {
    return String(method || 'GET').toUpperCase() === 'GET' && (url.protocol === 'http:' || url.protocol === 'https:') && sameSite(url);
  }
  if (nativeFetch) {
    window.fetch = function (input, init) {
      try {
        var isRequest = typeof Request !== 'undefined' && input instanceof Request;
        var method = (init && init.method) || (isRequest ? input.method : 'GET');
        var url = toSite(isRequest ? input.url : input);
        if (proxied(method, url)) {
          return nativeFetch(resourceURL(url.href), {
            credentials: 'omit',
            signal: (init && init.signal) || (isRequest ? input.signal : undefined)
          });
        }
      } catch (e) { /* not something to reroute */ }
      return nativeFetch.apply(window, arguments);
    };
  }
  if (window.XMLHttpRequest) {
    var X = XMLHttpRequest.prototype, xOpen = X.open, xSend = X.send, xHeader = X.setRequestHeader;
    X.open = function (method, url) {
      var args = Array.prototype.slice.call(arguments);
      this.__pinpoint = false;
      try {
        var u = toSite(url);
        if (proxied(method, u)) { args[1] = resourceURL(u.href); this.__pinpoint = true; }
      } catch (e) {}
      return xOpen.apply(this, args);
    };
    // Extra headers would make the browser ask PinPoint first, which it
    // does not answer.
    X.setRequestHeader = function () { if (!this.__pinpoint) return xHeader.apply(this, arguments); };
    X.send = function () {
      if (this.__pinpoint) { try { this.withCredentials = false; } catch (e) {} }
      return xSend.apply(this, arguments);
    };
  }

  window.addEventListener('pagehide', function () { tell('leaving', {}); });

  // ---- Describing an element ---------------------------------------------

  function stableID(id) {
    return id && id.length < 50 && !/\d{3,}|^[:\d]|^(react|ember|radix|headlessui|mui|rc|ng)[-_]/i.test(id);
  }
  function stableClass(c) {
    if (!c || c.length > 40 || /[:/[\]@!]/.test(c)) return false;
    if (/^(css|sc|jsx|svelte|emotion|tw|astro|chakra|ng)-/i.test(c)) return false;
    if (/__[A-Za-z0-9_-]{5,}$/.test(c) && /\d/.test(c.split('__').pop())) return false;
    if (/^(is|has)-|^(active|open|opened|show|shown|visible|hidden|current|selected|focus|focused|hover|loaded|collapsed|expanded|disabled|in|fade|aos-init|aos-animate)$/i.test(c)) return false;
    var digits = (c.match(/\d/g) || []).length;
    return !(digits >= 4 && digits / c.length > 0.25);
  }
  var LANDMARK = /^(header|nav|main|footer|section|article|aside|form|dialog|table|ul|ol)$/;

  function unique(sel, el) {
    try {
      var m = document.querySelectorAll(sel);
      return m.length === 1 && m[0] === el;
    } catch (e) { return false; }
  }
  function classesOf(el) { return Array.from(el.classList || []).filter(stableClass); }

  // own lists the ways to name el by itself, simplest first.
  function own(el) {
    var tag = el.localName, out = [];
    if (el.id && stableID(el.id)) out.push('#' + CSS.escape(el.id));
    var cls = classesOf(el).slice(0, 3).map(function (c) { return '.' + CSS.escape(c); });
    for (var i = 1; i <= cls.length; i++) out.push((LANDMARK.test(tag) ? tag : '') + cls.slice(0, i).join(''));
    out.push(tag + cls.join(''));
    return out;
  }
  // step names an ancestor in a path, or nothing for an anonymous wrapper.
  function step(el) {
    if (el.id && stableID(el.id)) return '#' + CSS.escape(el.id);
    var cls = classesOf(el);
    if (LANDMARK.test(el.localName)) return el.localName + (cls.length ? '.' + CSS.escape(cls[0]) : '');
    return cls.length ? '.' + CSS.escape(cls[0]) : '';
  }
  function nth(el) {
    var tag = el.localName, n = 1, same = 0;
    for (var s = el.parentElement && el.parentElement.firstElementChild; s; s = s.nextElementSibling) {
      if (s.localName === tag) { same++; if (s === el) n = same; }
    }
    return same > 1 ? tag + ':nth-of-type(' + n + ')' : tag;
  }

  // selectorFor names el as a person would read it, and as a coding AI can
  // find it in the source: classes and landmarks before position.
  function selectorFor(el) {
    if (!el || el === document.body || el === document.documentElement) return 'body';
    var names = own(el);
    for (var i = 0; i < names.length; i++) if (unique(names[i], el)) return names[i];
    var self = names[names.length - 1];
    var chain = [self];
    for (var p = el.parentElement; p && p !== document.body && p !== document.documentElement; p = p.parentElement) {
      var s = step(p);
      if (!s) continue;
      chain.unshift(s);
      var sel = chain.join(' ');
      if (unique(sel, el)) return sel;
      if (s.charAt(0) === '#') break;
    }
    // Still more than one: say which by position, from the nearest ancestor
    // with an id, or from the body.
    var path = [];
    for (var q = el; q && q !== document.body && q !== document.documentElement; q = q.parentElement) {
      if (q !== el && q.id && stableID(q.id)) { path.unshift('#' + CSS.escape(q.id)); break; }
      path.unshift(nth(q));
    }
    if (path[0] && path[0].charAt(0) !== '#') path.unshift('body');
    var full = path.join(' > ');
    return unique(full, el) ? full : self;
  }

  function textOf(el) {
    var t;
    if (el.localName === 'img') t = el.alt || '';
    else if (/^(input|textarea|select)$/.test(el.localName)) t = el.placeholder || el.value || el.getAttribute('aria-label') || '';
    else t = el.innerText || el.textContent || el.getAttribute('aria-label') || el.getAttribute('title') || '';
    t = t.replace(/\s+/g, ' ').trim();
    return t.length > 160 ? t.slice(0, 157) + '…' : t;
  }

  // htmlOf is the element's markup, kept short enough to read: long
  // attributes cut, scripts, styles and icon paths left out, and a large
  // element reduced to its own tag.
  function htmlOf(el) {
    var clone = el.cloneNode(true);
    clone.querySelectorAll('script, style, noscript, template, pinpoint-layer').forEach(function (n) { n.remove(); });
    clone.querySelectorAll('svg').forEach(function (s) { s.innerHTML = '…'; });
    [clone].concat(Array.from(clone.querySelectorAll('*'))).forEach(function (n) {
      Array.from(n.attributes).forEach(function (a) {
        if (a.value.length > 80) n.setAttribute(a.name, a.value.slice(0, 77) + '…');
      });
    });
    var html = clone.outerHTML.replace(/\n\s*\n/g, '\n');
    if (html.length <= 700) return html;
    var close = '</' + el.localName + '>';
    var inner = clone.innerHTML;
    var open = html.slice(0, html.length - inner.length - close.length);
    if (!html.endsWith(close) || !open.startsWith('<')) open = html.slice(0, html.indexOf('>') + 1);
    return open + '…' + close;
  }

  function describe(el) {
    return { selector: selectorFor(el), elTag: el.localName, elText: textOf(el), elHtml: htmlOf(el) };
  }

  // ---- Which element is meant ---------------------------------------------

  function under(x, y) {
    var list = document.elementsFromPoint(x, y);
    for (var i = 0; i < list.length; i++) if (list[i] !== host) return list[i];
    return document.body;
  }
  var INLINE = /^(span|strong|em|b|i|small|mark|sup|sub|u|s|abbr|code|br|wbr|font|time|bdi|q|cite)$/;
  var CONTROL = 'a[href], button, label, input, select, textarea, summary, [role=button], [role=link], [role=tab], [role=menuitem]';

  // meaningful is the element a click on el is about: the link or button it
  // is in, the whole picture rather than a path in it, and the paragraph
  // rather than a word in bold.
  function meaningful(el) {
    if (!el || el.nodeType !== 1) return document.body;
    while (el.ownerSVGElement) el = el.ownerSVGElement;
    var control = el.closest(CONTROL);
    if (control) return control;
    while (el.parentElement && INLINE.test(el.localName)) el = el.parentElement;
    return el;
  }

  // anchorFor is the element a shape is about. A box drawn around a button
  // is about the button; a scribble across a section is about the section.
  function anchorFor(r) {
    var tol = 6, inner = null, el = under(r.x + r.w / 2, r.y + r.h / 2);
    for (var e = el; e && e !== document.documentElement; e = e.parentElement) {
      var b = e.getBoundingClientRect();
      var inside = b.left >= r.x - tol && b.top >= r.y - tol && b.right <= r.x + r.w + tol && b.bottom <= r.y + r.h + tol;
      var covers = b.left <= r.x + tol && b.top <= r.y + tol && b.right >= r.x + r.w - tol && b.bottom >= r.y + r.h - tol;
      if (inside) inner = e;
      if (covers) {
        if (inner && area(inner.getBoundingClientRect()) >= 0.3 * r.w * r.h) return meaningful(inner);
        return meaningful(e);
      }
    }
    return inner ? meaningful(inner) : document.body;
  }
  function area(b) { return b.width * b.height; }

  // ---- Shapes ---------------------------------------------------------------
  // A shape is stored twice: as fractions of its element's box (rel), which
  // follows the element wherever the layout puts it, and in page pixels
  // (abs), for when the element cannot be found any more.

  function scrollXY() { return { x: window.scrollX || 0, y: window.scrollY || 0 }; }

  function toRel(v, b) {
    return v.length === 2
      ? [round((v[0] - b.left) / (b.width || 1)), round((v[1] - b.top) / (b.height || 1))]
      : [round((v[0] - b.left) / (b.width || 1)), round((v[1] - b.top) / (b.height || 1)), round(v[2] / (b.width || 1)), round(v[3] / (b.height || 1))];
  }
  function fromRel(v, b) {
    return v.length === 2
      ? [b.left + v[0] * b.width, b.top + v[1] * b.height]
      : [b.left + v[0] * b.width, b.top + v[1] * b.height, v[2] * b.width, v[3] * b.height];
  }
  function round(n) { return Math.round(n * 10000) / 10000; }
  function bboxOf(shape, vals) {
    var x1 = Infinity, y1 = Infinity, x2 = -Infinity, y2 = -Infinity;
    vals.forEach(function (v) {
      x1 = Math.min(x1, v[0]); y1 = Math.min(y1, v[1]);
      x2 = Math.max(x2, v[0] + (v[2] || 0)); y2 = Math.max(y2, v[1] + (v[3] || 0));
    });
    if (shape === 'point') { x1 -= 4; y1 -= 30; x2 += 26; y2 += 4; }
    return { x: x1, y: y1, w: Math.max(1, x2 - x1), h: Math.max(1, y2 - y1) };
  }

  // makeGeometry turns client coordinates into the stored shape.
  function makeGeometry(shape, vals, el) {
    var b = el.getBoundingClientRect(), s = scrollXY();
    return {
      shape: shape,
      rel: vals.map(function (v) { return toRel(v, b); }),
      abs: vals.map(function (v) { return v.length === 2 ? [Math.round(v[0] + s.x), Math.round(v[1] + s.y)] : [Math.round(v[0] + s.x), Math.round(v[1] + s.y), Math.round(v[2]), Math.round(v[3])]; })
    };
  }

  var found = new Map(); // annotation id -> { el, at }
  function elementFor(a) {
    var hit = found.get(a.id), now = Date.now();
    if (hit && hit.el && hit.el.isConnected && now - hit.at < 1500) return hit.el;
    var el = null;
    if (a.selector) { try { el = document.querySelector(a.selector); } catch (e) {} }
    found.set(a.id, { el: el, at: now });
    return el;
  }

  // place is where an annotation is right now, in the frame's viewport.
  function place(a) {
    var g = a.geometry || {}, el = elementFor(a), vals, isMoved = false;
    var b = el && el.getBoundingClientRect();
    if (a.kind === 'highlight' && a.quote && el) {
      var rects = textRects(el, a.quote);
      if (rects) return { shape: 'rects', vals: rects, moved: false };
    }
    if (b && (b.width > 0 || b.height > 0) && g.rel && g.rel.length) {
      vals = g.rel.map(function (v) { return fromRel(v, b); });
    } else if (g.abs && g.abs.length) {
      var s = scrollXY();
      vals = g.abs.map(function (v) { return v.length === 2 ? [v[0] - s.x, v[1] - s.y] : [v[0] - s.x, v[1] - s.y, v[2], v[3]]; });
      isMoved = !!a.selector && a.selector !== 'body';
    } else {
      return null;
    }
    return { shape: g.shape || 'point', vals: vals, moved: isMoved };
  }

  // textRects finds quote in el's text and returns the boxes it covers now,
  // which keeps a highlight on its words when the text reflows.
  function textRects(el, quote) {
    var want = quote.replace(/\s+/g, ' ').trim();
    if (!want) return null;
    var map = [], text = '', space = true;
    var walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (var n = walk.nextNode(); n; n = walk.nextNode()) {
      var v = n.nodeValue;
      for (var i = 0; i < v.length; i++) {
        var ws = /\s/.test(v[i]);
        if (ws && space) continue;
        text += ws ? ' ' : v[i];
        map.push([n, i]);
        space = ws;
      }
    }
    var at = text.indexOf(want);
    if (at < 0) return null;
    var start = map[at], end = map[at + want.length - 1];
    var range = document.createRange();
    range.setStart(start[0], start[1]);
    range.setEnd(end[0], end[1] + 1);
    var out = [];
    Array.from(range.getClientRects()).forEach(function (r) {
      if (r.width > 0 && r.height > 0) out.push([r.left, r.top, r.width, r.height]);
    });
    return out.length ? mergeLines(out) : null;
  }
  // mergeLines joins the boxes on each line into one.
  function mergeLines(rects) {
    var lines = [];
    rects.forEach(function (r) {
      var line = lines.find(function (l) { return Math.abs(l[1] - r[1]) < r[3] / 2 && Math.abs(l[3] - r[3]) < r[3]; });
      if (!line) { lines.push(r.slice()); return; }
      var x2 = Math.max(line[0] + line[2], r[0] + r[2]);
      line[0] = Math.min(line[0], r[0]);
      line[2] = x2 - line[0];
    });
    return lines;
  }

  // ---- The layer the marks are drawn on -------------------------------------

  var host, shadow, surface, svg, marks, hover, hint;
  var mode = 'browse', tool = 'pin';
  var shown = [], selected = 0, pending = null, drag = null;

  var CSS_TEXT = [
    ':host { all: initial; }',
    '.surface { position: fixed; inset: 0; pointer-events: none; cursor: crosshair; touch-action: none; }',
    '.annotate .surface { pointer-events: auto; }',
    '.annotate.highlight .surface, .annotate.waiting .surface { pointer-events: none; }',
    'svg { position: fixed; inset: 0; width: 100%; height: 100%; overflow: visible; pointer-events: none; }',
    '.marks { position: fixed; inset: 0; pointer-events: none; }',
    '.badge { position: absolute; min-width: 24px; height: 24px; padding: 0 6px; box-sizing: border-box; border-radius: 12px;',
    '  color: #fff; font: 700 12px/24px system-ui, -apple-system, "Segoe UI", sans-serif; text-align: center;',
    '  box-shadow: 0 0 0 2px #fff, 0 2px 8px rgba(0,0,0,.35); pointer-events: auto; cursor: pointer;',
    '  transform: translate(-50%, -50%); user-select: none; transition: transform .15s; }',
    '.badge.point { border-radius: 12px 12px 12px 2px; transform: translate(0, -100%); }',
    '.badge:hover { z-index: 2; }',
    '.badge.moved { opacity: .8; box-shadow: 0 0 0 2px #fff, 0 0 0 4px #f59f00, 0 2px 8px rgba(0,0,0,.35); }',
    '.badge.selected { z-index: 3; animation: pulse 1s ease-out 2; }',
    '.badge.pending { box-shadow: 0 0 0 2px #fff, 0 0 0 5px rgba(32,107,196,.35), 0 2px 8px rgba(0,0,0,.35); }',
    '@keyframes pulse { 0% { box-shadow: 0 0 0 2px #fff, 0 0 0 2px currentColor; } 70% { box-shadow: 0 0 0 2px #fff, 0 0 0 16px transparent; } 100% { box-shadow: 0 0 0 2px #fff, 0 0 0 2px transparent; } }',
    '.hover { position: fixed; pointer-events: none; border: 2px solid #206bc4; background: rgba(32,107,196,.08); border-radius: 3px; display: none; box-sizing: border-box; }',
    '.tip { position: fixed; left: 50%; bottom: 14px; transform: translateX(-50%); background: rgba(24,36,51,.9); color: #fff; padding: 6px 12px;',
    '  border-radius: 999px; font: 500 13px/1.3 system-ui, sans-serif; pointer-events: none; display: none; white-space: nowrap; }'
  ].join('\n');

  function build() {
    host = document.createElement('pinpoint-layer');
    host.style.cssText = 'all: initial; position: fixed; inset: 0; z-index: 2147483647; pointer-events: none; display: block;';
    shadow = host.attachShadow({ mode: 'closed' });
    shadow.innerHTML = '<style>' + CSS_TEXT + '</style><div class="root"><div class="surface"></div>' +
      '<svg xmlns="http://www.w3.org/2000/svg"></svg><div class="marks"></div><div class="hover"></div><div class="tip"></div></div>';
    var root = shadow.querySelector('.root');
    surface = shadow.querySelector('.surface');
    svg = shadow.querySelector('svg');
    marks = shadow.querySelector('.marks');
    hover = shadow.querySelector('.hover');
    hint = shadow.querySelector('.tip');
    host.__root = root;
    document.documentElement.appendChild(host);

    surface.addEventListener('pointerdown', down);
    surface.addEventListener('pointermove', moveOver);
    surface.addEventListener('pointerleave', function () { hover.style.display = 'none'; });
    window.addEventListener('pointermove', dragMove, true);
    window.addEventListener('pointerup', up, true);
    window.addEventListener('scroll', layoutSoon, true);
    window.addEventListener('resize', layoutSoon);
    if (window.ResizeObserver) new ResizeObserver(layoutSoon).observe(document.documentElement);
    setInterval(layout, 1000);
  }

  function setMode(m, t) {
    mode = m === 'annotate' ? 'annotate' : 'browse';
    if (t) tool = t;
    var root = host.__root;
    root.className = 'root ' + mode + ' ' + tool + (pending ? ' waiting' : '');
    hover.style.display = 'none';
    if (mode !== 'annotate' || tool !== 'highlight') clearSelection();
  }

  function clearSelection() {
    var sel = window.getSelection && window.getSelection();
    if (sel && !sel.isCollapsed) sel.removeAllRanges();
  }

  function svgEl(name, attrs) {
    var e = document.createElementNS('http://www.w3.org/2000/svg', name);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    return e;
  }

  var layoutQueued = false;
  function layoutSoon() {
    if (layoutQueued) return;
    layoutQueued = true;
    requestAnimationFrame(function () { layoutQueued = false; layout(); });
  }

  // layout draws every annotation on this page where it is now.
  function layout() {
    if (!host) return;
    if (!host.isConnected) document.documentElement.appendChild(host);
    while (svg.firstChild) svg.removeChild(svg.firstChild);
    while (marks.firstChild) marks.removeChild(marks.firstChild);
    var all = shown.slice();
    if (pending) all.push(pending);
    var movedIDs = [];
    all.forEach(function (a) {
      var p = a === pending ? pendingPlace() : place(a);
      if (!p) return;
      if (p.moved) movedIDs.push(a.id);
      drawShape(a, p);
    });
    if (movedIDs.join() !== lastMoved) {
      lastMoved = movedIDs.join();
      tell('moved', { ids: movedIDs });
    }
  }
  var lastMoved = '';

  function drawShape(a, p) {
    var color = COLORS[a.label] || COLORS.change;
    var isSel = a.id === selected, badgeAt;
    if (p.shape === 'path') {
      var pts = p.vals.map(function (v) { return v[0].toFixed(1) + ',' + v[1].toFixed(1); }).join(' ');
      svg.appendChild(svgEl('polyline', { points: pts, fill: 'none', stroke: '#fff', 'stroke-width': isSel ? 8 : 6, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', opacity: .8 }));
      svg.appendChild(svgEl('polyline', { points: pts, fill: 'none', stroke: color, 'stroke-width': isSel ? 4.5 : 3, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'stroke-dasharray': p.moved ? '6 5' : '' }));
      badgeAt = p.vals[0];
    } else if (p.shape === 'rects') {
      var fill = a.kind === 'highlight';
      p.vals.forEach(function (r) {
        if (fill) {
          svg.appendChild(svgEl('rect', { x: r[0], y: r[1], width: r[2], height: r[3], rx: 2, fill: color, 'fill-opacity': isSel ? .38 : .26 }));
        } else {
          svg.appendChild(svgEl('rect', { x: r[0], y: r[1], width: r[2], height: r[3], rx: 4, fill: 'none', stroke: '#fff', 'stroke-width': isSel ? 7 : 5, opacity: .8 }));
          svg.appendChild(svgEl('rect', { x: r[0], y: r[1], width: r[2], height: r[3], rx: 4, fill: color, 'fill-opacity': .06, stroke: color, 'stroke-width': isSel ? 4 : 2.5, 'stroke-dasharray': p.moved ? '7 5' : '' }));
        }
      });
      badgeAt = [p.vals[0][0], p.vals[0][1]];
    } else {
      badgeAt = p.vals[0];
    }
    var badge = document.createElement('div');
    badge.className = 'badge' + (p.shape === 'point' ? ' point' : '') + (p.moved ? ' moved' : '') + (isSel ? ' selected' : '') + (a === pending ? ' pending' : '');
    badge.style.background = color;
    badge.style.color = '#fff';
    badge.style.left = badgeAt[0] + 'px';
    badge.style.top = badgeAt[1] + 'px';
    badge.textContent = a === pending ? '+' : String(a.number || '');
    if (a.comment) badge.title = a.comment;
    if (p.moved) badge.title = (a.comment ? a.comment + '\n\n' : '') + 'The element this was on has moved or is gone.';
    if (a !== pending) {
      badge.addEventListener('click', function (e) { e.stopPropagation(); e.preventDefault(); tell('select', { id: a.id }); });
      badge.addEventListener('pointerdown', function (e) { e.stopPropagation(); });
    }
    marks.appendChild(badge);
  }

  // ---- Marking the page -----------------------------------------------------

  function showHint(text) {
    hint.textContent = text;
    hint.style.display = text ? 'block' : 'none';
  }

  function moveOver(e) {
    if (mode !== 'annotate' || pending || drag) return;
    if (tool !== 'pin' && tool !== 'box') { hover.style.display = 'none'; return; }
    var el = meaningful(under(e.clientX, e.clientY));
    var b = el.getBoundingClientRect();
    hover.style.display = 'block';
    hover.style.left = b.left + 'px';
    hover.style.top = b.top + 'px';
    hover.style.width = b.width + 'px';
    hover.style.height = b.height + 'px';
  }

  function down(e) {
    if (mode !== 'annotate' || pending || e.button !== 0) return;
    e.preventDefault();
    surface.setPointerCapture && surface.setPointerCapture(e.pointerId);
    drag = { tool: tool, x: e.clientX, y: e.clientY, pts: [[e.clientX, e.clientY]], moved: false };
    hover.style.display = 'none';
  }

  function dragMove(e) {
    if (!drag) return;
    var dx = e.clientX - drag.x, dy = e.clientY - drag.y;
    if (Math.abs(dx) + Math.abs(dy) > 5) drag.moved = true;
    if (drag.tool === 'draw') {
      var last = drag.pts[drag.pts.length - 1];
      if (Math.abs(e.clientX - last[0]) + Math.abs(e.clientY - last[1]) >= 3) drag.pts.push([e.clientX, e.clientY]);
    } else {
      drag.pts[1] = [e.clientX, e.clientY];
    }
    drawDrag();
  }

  var dragShape = null;
  function drawDrag() {
    if (dragShape) dragShape.remove();
    dragShape = null;
    if (!drag || !drag.moved) return;
    var color = COLORS.change;
    if (drag.tool === 'draw') {
      dragShape = svgEl('polyline', { points: drag.pts.map(function (v) { return v.join(','); }).join(' '), fill: 'none', stroke: color, 'stroke-width': 3, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' });
    } else if (drag.tool === 'box' || drag.tool === 'highlight') {
      var r = rectOf(drag);
      dragShape = svgEl('rect', drag.tool === 'box'
        ? { x: r.x, y: r.y, width: r.w, height: r.h, rx: 4, fill: color, 'fill-opacity': .06, stroke: color, 'stroke-width': 2.5 }
        : { x: r.x, y: r.y, width: r.w, height: r.h, rx: 2, fill: color, 'fill-opacity': .26 });
    }
    if (dragShape) svg.appendChild(dragShape);
  }

  function rectOf(d) {
    var a = d.pts[0], b = d.pts[1] || a;
    return { x: Math.min(a[0], b[0]), y: Math.min(a[1], b[1]), w: Math.abs(a[0] - b[0]), h: Math.abs(a[1] - b[1]) };
  }

  function up(e) {
    // The highlight tool lets the page select text, and looks at what was
    // selected once the button comes up.
    if (mode === 'annotate' && tool === 'highlight' && !pending && hlStart) {
      var start = hlStart;
      hlStart = null;
      setTimeout(function () { finishHighlight(start, e); }, 0);
      return;
    }
    if (!drag) return;
    var d = drag;
    drag = null;
    drawDrag();
    if (d.tool === 'pin') {
      var el = meaningful(under(d.x, d.y));
      startDraft('pin', 'point', [[d.x, d.y]], el);
    } else if (d.tool === 'box') {
      if (d.moved) {
        var r = rectOf(d);
        if (r.w > 6 && r.h > 6) startDraft('box', 'rects', [[r.x, r.y, r.w, r.h]], anchorFor(r));
      } else {
        // A click with the box tool frames the element under it.
        var t = meaningful(under(d.x, d.y)), b = t.getBoundingClientRect();
        startDraft('box', 'rects', [[b.left - 4, b.top - 4, b.width + 8, b.height + 8]], t);
      }
    } else if (d.tool === 'draw' && d.moved && d.pts.length > 2) {
      var pts = simplify(d.pts);
      var bb = bboxOf('path', pts);
      startDraft('draw', 'path', pts, anchorFor(bb));
    }
  }

  // simplify drops points that add nothing to a drawn line.
  function simplify(pts) {
    if (pts.length < 3) return pts;
    var out = [pts[0]];
    for (var i = 1; i < pts.length - 1; i++) {
      var p = out[out.length - 1], q = pts[i];
      if (Math.hypot(q[0] - p[0], q[1] - p[1]) >= 4) out.push(q);
    }
    out.push(pts[pts.length - 1]);
    return out.map(function (v) { return [Math.round(v[0] * 10) / 10, Math.round(v[1] * 10) / 10]; });
  }

  var hlStart = null;
  window.addEventListener('pointerdown', function (e) {
    if (mode === 'annotate' && tool === 'highlight' && !pending && e.button === 0 && !(host && e.composedPath && e.composedPath().indexOf(host) >= 0)) {
      hlStart = { x: e.clientX, y: e.clientY };
    }
  }, true);
  // While annotating, the page's own buttons and links do nothing.
  ['click', 'auxclick', 'dblclick', 'contextmenu'].forEach(function (type) {
    window.addEventListener(type, function (e) {
      if (mode !== 'annotate') return;
      if (host && e.composedPath && e.composedPath().indexOf(host) >= 0) return;
      e.preventDefault();
      e.stopPropagation();
    }, true);
  });
  window.addEventListener('dragstart', function (e) { if (mode === 'annotate') e.preventDefault(); }, true);

  function finishHighlight(start, e) {
    var sel = window.getSelection();
    var text = sel && !sel.isCollapsed ? sel.toString().replace(/\s+/g, ' ').trim() : '';
    if (text) {
      var range = sel.getRangeAt(0);
      var rects = mergeLines(Array.from(range.getClientRects()).filter(function (r) { return r.width > 0 && r.height > 0; })
        .map(function (r) { return [r.left, r.top, r.width, r.height]; }));
      var node = range.commonAncestorContainer;
      var el = meaningful(node.nodeType === 1 ? node : node.parentElement);
      sel.removeAllRanges();
      if (rects.length) startDraft('highlight', 'rects', rects, el, { quote: text.slice(0, 1000) });
      return;
    }
    var r = { x: Math.min(start.x, e.clientX), y: Math.min(start.y, e.clientY), w: Math.abs(start.x - e.clientX), h: Math.abs(start.y - e.clientY) };
    if (r.w > 8 && r.h > 8) {
      startDraft('highlight', 'rects', [[r.x, r.y, r.w, r.h]], anchorFor(r));
    } else {
      tell('notice', { text: 'Select some text to highlight it, or drag across an area.' });
    }
  }

  var draftSeq = 0;
  function startDraft(kind, shape, vals, el, extra) {
    if (!el || !el.getBoundingClientRect) el = document.body;
    var g = makeGeometry(shape, vals, el);
    var b = el.getBoundingClientRect();
    var box = bboxOf(shape, vals);
    var cx = box.x + box.w / 2, cy = box.y + box.h / 2;
    if (shape === 'point') { cx = vals[0][0]; cy = vals[0][1]; }
    var info = describe(el);
    pending = {
      id: 'draft-' + (++draftSeq),
      kind: kind, label: 'change', comment: '',
      pageUrl: current, pageTitle: document.title, viewport: window.innerWidth,
      selector: info.selector, elTag: info.elTag, elText: info.elText, elHtml: info.elHtml,
      quote: (extra && extra.quote) || '',
      posX: clamp01((cx - b.left) / (b.width || 1)), posY: clamp01((cy - b.top) / (b.height || 1)),
      geometry: g
    };
    pending.__vals = vals;
    pending.__scroll = scrollXY();
    setMode(mode, tool);
    layout();
    var data = {};
    for (var k in pending) if (k.indexOf('__') !== 0 && k !== 'id') data[k] = pending[k];
    tell('draft', { id: pending.id, draft: data, box: box });
    var mine = pending;
    capture(mine).then(function (png) {
      tell('shot', { id: mine.id, png: png });
    }, function (err) {
      tell('shot', { id: mine.id, png: null, error: String(err && err.message || err) });
    });
  }
  function clamp01(n) { return isFinite(n) ? Math.max(0, Math.min(1, n)) : 0; }

  // pendingPlace keeps the shape being described where it was drawn, on the
  // page, while the reviewer types.
  function pendingPlace() {
    var s = scrollXY(), was = pending.__scroll, dx = was.x - s.x, dy = was.y - s.y;
    return {
      shape: pending.geometry.shape,
      vals: pending.__vals.map(function (v) { return v.length === 2 ? [v[0] + dx, v[1] + dy] : [v[0] + dx, v[1] + dy, v[2], v[3]]; }),
      moved: false
    };
  }

  // ---- Screenshots ----------------------------------------------------------

  var screenshotLib = null;
  function loadScreenshotLib() {
    if (!screenshotLib) {
      // Fetched and run in a scope of its own, so that a page with its own
      // module loader does not take it.
      screenshotLib = nativeFetch(ORIGIN + '/static/vendor/modern-screenshot.js?v=' + (cfg.v || ''), { credentials: 'omit' })
        .then(function (r) { if (!r.ok) throw new Error('could not load the screenshot code'); return r.text(); })
        .then(function (src) {
          var mod = { exports: {} };
          (new Function('exports', 'module', 'define', src))(mod.exports, mod, undefined);
          return mod.exports;
        });
      screenshotLib.catch(function () { screenshotLib = null; });
    }
    return screenshotLib;
  }

  function blobToDataURL(blob) {
    return new Promise(function (ok, fail) {
      var r = new FileReader();
      r.onload = function () { ok(r.result); };
      r.onerror = fail;
      r.readAsDataURL(blob);
    });
  }

  // viaPinPoint fetches an image or font for the screenshot. The browser lets
  // this page see the pixels of only what it fetched itself with permission,
  // which PinPoint's copy gives.
  function viaPinPoint(raw) {
    if (/^(data|blob):/.test(raw)) return Promise.resolve(false);
    var u;
    try { u = toSite(raw); } catch (e) { return Promise.resolve(false); }
    if (u.origin === ORIGIN) return Promise.resolve(false);
    return nativeFetch(resourceURL(u.href), { credentials: 'omit' })
      .then(function (r) { return r.ok ? r.blob() : null; })
      .then(function (b) { return b ? blobToDataURL(b) : false; })
      .catch(function () { return false; });
  }

  // defaultStyles stands in for the frame the screenshot code would make to
  // learn the browser's default styles: a frame made in a sandboxed page has
  // an origin of its own, which the code may not look into. A shadow root
  // with every style reset gives the same answers, as the page's own styles
  // do not reach into it.
  var defaultsBody = null;
  function defaultStyles() {
    if (!defaultsBody || !defaultsBody.isConnected) {
      var holder = document.createElement('div');
      holder.style.cssText = 'all: initial; position: fixed; left: -10000px; top: 0; width: 0; height: 0; overflow: hidden;';
      shadow.appendChild(holder);
      var root = holder.attachShadow({ mode: 'open' });
      defaultsBody = document.createElement('div');
      defaultsBody.style.cssText = 'all: initial; display: block;';
      root.appendChild(defaultsBody);
    }
    var doc = {
      body: defaultsBody,
      createElement: document.createElement.bind(document),
      createElementNS: document.createElementNS.bind(document)
    };
    return { contentWindow: { document: doc, getComputedStyle: window.getComputedStyle.bind(window) }, remove: function () {} };
  }

  function background() {
    var els = [document.body, document.documentElement];
    for (var i = 0; i < els.length; i++) {
      if (!els[i]) continue;
      var c = getComputedStyle(els[i]).backgroundColor;
      if (c && c !== 'transparent' && !/rgba\(.*,\s*0\)$/.test(c)) return c;
    }
    return '#ffffff';
  }

  // capture photographs the part of the page around a new mark, at the size
  // it is on screen, and draws the mark on it.
  function capture(d) {
    if (!nativeFetch) return Promise.reject(new Error('this browser cannot take screenshots'));
    var s = d.__scroll, box = bboxOf(d.geometry.shape, d.__vals);
    var docEl = document.documentElement;
    var docW = Math.max(docEl.scrollWidth, docEl.clientWidth), docH = Math.max(docEl.scrollHeight, docEl.clientHeight);
    var pad = 56;
    var w = Math.min(docW, Math.max(box.w + pad * 2, Math.min(720, docW)));
    var h = Math.min(docH, Math.max(box.h + pad * 2, 400), 1400);
    var x = Math.max(0, Math.min(docW - w, box.x + s.x + box.w / 2 - w / 2));
    var y = Math.max(0, Math.min(docH - h, box.y + s.y + box.h / 2 - h / 2));
    return loadScreenshotLib().then(function (ms) {
      return ms.createContext(docEl, {
        width: docW,
        height: docH,
        backgroundColor: background(),
        filter: function (n) { return n !== host; },
        fetchFn: viaPinPoint,
        timeout: 6000,
        scale: 1
      }).then(function (context) {
        context.sandbox = defaultStyles();
        return ms.domToCanvas(context);
      });
    }).then(function (full) {
      var out = document.createElement('canvas');
      var scale = Math.min(1, 1000 / w);
      out.width = Math.round(w * scale);
      out.height = Math.round(h * scale);
      out.getContext('2d').drawImage(full, x, y, w, h, 0, 0, out.width, out.height);
      // Where the mark goes in the picture, kept for painting it on.
      d.__photo = {
        canvas: out, scale: scale,
        vals: d.__vals.map(function (v) {
          return v.length === 2 ? [v[0] + s.x - x, v[1] + s.y - y] : [v[0] + s.x - x, v[1] + s.y - y, v[2], v[3]];
        })
      };
      return withMark(d);
    });
  }

  // withMark is the screenshot with the mark painted on in the colour of
  // its label, which the reviewer may change after the photo is taken.
  function withMark(d) {
    var ph = d.__photo, out = document.createElement('canvas');
    out.width = ph.canvas.width;
    out.height = ph.canvas.height;
    var ctx = out.getContext('2d');
    ctx.drawImage(ph.canvas, 0, 0);
    ctx.scale(ph.scale, ph.scale);
    paintMark(ctx, d, ph.vals);
    return out.toDataURL('image/png');
  }

  function paintMark(ctx, d, vals) {
    var color = COLORS[d.label] || COLORS.change;
    ctx.lineJoin = ctx.lineCap = 'round';
    function stroke(width, style, alpha) { ctx.globalAlpha = alpha; ctx.lineWidth = width; ctx.strokeStyle = style; ctx.stroke(); ctx.globalAlpha = 1; }
    if (d.geometry.shape === 'path') {
      ctx.beginPath();
      vals.forEach(function (v, i) { if (i) ctx.lineTo(v[0], v[1]); else ctx.moveTo(v[0], v[1]); });
      stroke(7, '#fff', .85);
      stroke(3.5, color, 1);
    } else if (d.geometry.shape === 'rects') {
      vals.forEach(function (r) {
        if (d.kind === 'highlight') {
          ctx.globalAlpha = .3; ctx.fillStyle = color; ctx.fillRect(r[0], r[1], r[2], r[3]); ctx.globalAlpha = 1;
        } else {
          ctx.beginPath(); ctx.rect(r[0], r[1], r[2], r[3]);
          stroke(7, '#fff', .85);
          stroke(3, color, 1);
        }
      });
    } else {
      // A pin: a round marker whose corner touches the point.
      var p = vals[0];
      ctx.beginPath();
      ctx.moveTo(p[0], p[1]);
      ctx.lineTo(p[0], p[1] - 14);
      ctx.arc(p[0] + 14, p[1] - 14, 14, Math.PI, Math.PI / 2, false);
      ctx.closePath();
      ctx.fillStyle = color;
      ctx.shadowColor = 'rgba(0,0,0,.35)'; ctx.shadowBlur = 6;
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.lineWidth = 2.5; ctx.strokeStyle = '#fff'; ctx.stroke();
      ctx.beginPath(); ctx.arc(p[0] + 14, p[1] - 14, 4.5, 0, Math.PI * 2); ctx.fillStyle = '#fff'; ctx.fill();
    }
  }

  // ---- Talking to the editor --------------------------------------------------

  function pageInfo() {
    return { url: current, title: document.title, width: window.innerWidth, status: cfg.status };
  }

  function focusOn(id) {
    selected = id;
    var a = shown.find(function (x) { return x.id === id; });
    layout();
    if (!a) return;
    var p = place(a);
    if (!p) return;
    var box = bboxOf(p.shape, p.vals);
    var vh = window.innerHeight;
    if (box.y < 40 || box.y + box.h > vh - 40) {
      var before = window.scrollY;
      window.scrollBy({ top: box.y + box.h / 2 - vh / 2, behavior: 'smooth' });
      // Pages that scroll an inner element rather than the window.
      setTimeout(function () {
        if (window.scrollY === before) {
          var el = elementFor(a);
          if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
        }
      }, 120);
    }
  }

  window.addEventListener('message', function (e) {
    if (e.source !== parent) return;
    var m = e.data;
    if (!m || typeof m !== 'object' || typeof m.pp !== 'string') return;
    switch (m.pp) {
      case 'mode':
        setMode(m.mode, m.tool);
        showHint('');
        break;
      case 'show':
        shown = Array.isArray(m.list) ? m.list : [];
        found.clear();
        if (m.selected !== undefined) selected = m.selected;
        layout();
        break;
      case 'focus':
        focusOn(m.id);
        break;
      case 'select':
        selected = m.id;
        layout();
        break;
      case 'draft-label':
        if (pending) {
          pending.label = m.label;
          layout();
          if (pending.__photo) tell('shot', { id: pending.id, png: withMark(pending) });
        }
        break;
      case 'draft-done':
        pending = null;
        setMode(mode, tool);
        layout();
        break;
      case 'back':
        history.back();
        break;
    }
  });

  document.addEventListener('keydown', function (e) {
    var typing = e.target && (e.target.isContentEditable || /^(input|textarea|select)$/i.test(e.target.localName));
    if (e.key === 'Escape') {
      if (drag) { drag = null; drawDrag(); }
      tell('key', { key: 'Escape' });
    } else if (!typing && !e.ctrlKey && !e.metaKey && !e.altKey && /^[ab]$/i.test(e.key)) {
      tell('key', { key: e.key.toLowerCase() });
    }
  }, true);

  function start() {
    build();
    tell('hello', pageInfo());
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
