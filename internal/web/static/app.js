/*
 * The editor and the shared review: the site in a frame, a list of
 * annotations beside it, and (in the editor) the tools. The frame runs
 * agent.js, and the two talk with postMessage. The frame's origin is
 * nobody's, so everything it says is checked to come from that frame.
 */
(function () {
  'use strict';
  var cfg = JSON.parse(document.getElementById('pp-config').textContent);
  var EDIT = cfg.mode === 'edit';
  var LABELS = {
    change: { title: 'Change', icon: 'pencil' },
    bug: { title: 'Bug', icon: 'bug' },
    question: { title: 'Question', icon: 'help' },
    like: { title: 'Like it', icon: 'heart' }
  };
  var HINTS = {
    pin: 'Click anything to comment on it',
    highlight: 'Select text to highlight it, or drag across an area',
    box: 'Drag a box, or click something to frame it',
    draw: 'Draw on the page'
  };

  var frame = document.getElementById('site');
  var loading = document.getElementById('loading');
  var banner = document.getElementById('banner');
  var list = cfg.annotations || [];
  var page = null;          // the page the frame is showing: { url, title }
  var lastGood = null;      // the last page that loaded properly
  var helloSinceLoad = false;
  var mode = EDIT ? 'annotate' : 'browse', tool = 'pin';
  var selected = 0;
  var movedIDs = [];
  var pendingFocus = 0;

  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }

  // ---- Addresses ----------------------------------------------------------

  function siteKey(host) { return String(host).toLowerCase().replace(/^www\./, ''); }
  function pageKey(raw) {
    try {
      var u = new URL(raw);
      var path = u.pathname.length > 1 ? u.pathname.replace(/\/+$/, '') : u.pathname;
      return siteKey(u.host) + path + u.search;
    } catch (e) { return String(raw); }
  }
  function onThisPage(a) { return page && pageKey(a.pageUrl) === pageKey(page.url); }
  function numberOf(a) { return list.indexOf(a) + 1; }
  function pageLabel(raw) {
    try {
      var u = new URL(raw);
      return (u.pathname === '/' && !u.search) ? 'Home page' : decodeURI(u.pathname) + u.search;
    } catch (e) { return raw; }
  }

  // ---- The frame ------------------------------------------------------------

  function send(msg) {
    if (frame.contentWindow) frame.contentWindow.postMessage(msg, '*');
  }

  function load(url) {
    loading.hidden = false;
    banner.hidden = true;
    helloSinceLoad = false;
    frame.src = cfg.frame + encodeURIComponent(url);
  }

  frame.addEventListener('load', function () {
    loading.hidden = true;
    if (!helloSinceLoad && !frameError) {
      // The page went somewhere the review script did not come with it:
      // usually a script that sent it straight to the real site.
      showBanner('This page left the review, so it can\'t be shown or marked here.');
    }
    frameError = false;
  });

  var frameError = false;
  function showBanner(text) {
    $('[data-text]', banner).textContent = text;
    banner.hidden = false;
  }

  function showMarks() {
    var mine = list.filter(onThisPage).map(function (a) {
      return {
        id: a.id, number: numberOf(a), kind: a.kind, label: a.label, comment: a.comment, selector: a.selector,
        quote: a.quote, geometry: a.geometry
      };
    });
    send({ pp: 'show', list: mine, selected: selected });
  }

  function sendMode() { send({ pp: 'mode', mode: mode, tool: tool }); }

  window.addEventListener('message', function (e) {
    if (e.source !== frame.contentWindow) return;
    var m = e.data;
    if (!m || typeof m !== 'object' || typeof m.pp !== 'string') return;
    switch (m.pp) {
      case 'hello':
        helloSinceLoad = true;
        loading.hidden = true;
        banner.hidden = true;
        arrived(m);
        sendMode();
        break;
      case 'nav':
        arrived(m);
        break;
      case 'leaving':
        cancelDraft(true);
        loading.hidden = false;
        break;
      case 'lost':
        if (lastGood) load(lastGood);
        break;
      case 'error':
        frameError = true;
        loading.hidden = true;
        break;
      case 'select':
        select(m.id, false);
        break;
      case 'moved':
        movedIDs = Array.isArray(m.ids) ? m.ids : [];
        markMoved();
        break;
      case 'notice':
        toast(String(m.text || ''), 'info');
        break;
      case 'key':
        key(m.key);
        break;
      case 'draft':
        if (EDIT) openDraft(m);
        break;
      case 'shot':
        if (EDIT) gotShot(m);
        break;
    }
  });

  function arrived(m) {
    page = { url: String(m.url || ''), title: String(m.title || '') };
    lastGood = page.url;
    var addr = $('#address');
    if (addr && document.activeElement !== addr) addr.value = page.url;
    try { if (EDIT) localStorage.setItem('pinpoint.last.' + cfg.api, page.url); } catch (e) {}
    showMarks();
    if (pendingFocus) {
      var id = pendingFocus;
      pendingFocus = 0;
      setTimeout(function () { send({ pp: 'focus', id: id }); }, 300);
    }
    markMoved();
  }

  // ---- Choosing an annotation ---------------------------------------------------

  function select(id, focus) {
    selected = id;
    $$('.note').forEach(function (n) { n.classList.toggle('selected', Number(n.dataset.id) === id); });
    var item = $('.note[data-id="' + id + '"]');
    if (item) item.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    var a = list.find(function (x) { return x.id === id; });
    if (!a) return;
    if (focus === false) { send({ pp: 'select', id: id }); return; }
    if (onThisPage(a)) {
      send({ pp: 'focus', id: id });
    } else {
      pendingFocus = id;
      load(a.pageUrl);
    }
    if (window.innerWidth < 992 && EDIT) $('#notes').classList.remove('open');
  }

  function markMoved() {
    $$('.note').forEach(function (n) {
      var flag = $('[data-moved]', n);
      if (flag) flag.hidden = movedIDs.indexOf(Number(n.dataset.id)) < 0;
    });
  }

  $('#notes-list').addEventListener('click', function (e) {
    if (e.target.closest('[data-no-select], button, a, form, textarea')) return;
    var n = e.target.closest('.note');
    if (n) select(Number(n.dataset.id));
  });
  $('#notes-list').addEventListener('keydown', function (e) {
    if (e.key !== 'Enter' || !e.target.classList.contains('note')) return;
    select(Number(e.target.dataset.id));
  });

  // ---- Little helpers -----------------------------------------------------------

  function toast(text, kind) {
    if (!text) return;
    var box = $('#toasts');
    var t = document.createElement('div');
    t.className = 'alert alert-' + (kind === 'error' ? 'danger' : kind === 'ok' ? 'success' : 'info') + ' py-2 px-3';
    t.setAttribute('role', 'status');
    t.textContent = text;
    box.append(t);
    setTimeout(function () { t.remove(); }, kind === 'error' ? 6000 : 3500);
  }

  function copy(text, done) {
    var ok = function () { toast(done || 'Copied', 'ok'); };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(ok, function () { fallbackCopy(text) ? ok() : toast('Couldn\'t copy. Select the text and copy it yourself.', 'error'); });
    } else if (fallbackCopy(text)) {
      ok();
    } else {
      toast('Couldn\'t copy. Select the text and copy it yourself.', 'error');
    }
  }
  function fallbackCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;top:-100px;opacity:0';
    document.body.append(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e) {}
    ta.remove();
    return ok;
  }

  // copyForAI copies the review as text, fetched fresh so it has every change.
  function copyForAI() {
    fetch(cfg.share + '.md', { headers: { Accept: 'text/markdown' } })
      .then(function (r) { if (!r.ok) throw new Error(); return r.text(); })
      .then(function (md) { copy(md, 'Copied. Paste it into your coding AI.'); })
      .catch(function () {
        var pre = $('#ai-text');
        if (pre) copy(pre.textContent, 'Copied. Paste it into your coding AI.');
        else toast('Couldn\'t get the review text. Try again.', 'error');
      });
  }

  function key(k) {
    if (k === 'Escape') {
      if (!$('#draft') || $('#draft').hidden) return;
      cancelDraft();
    } else if (EDIT && k === 'a') {
      setMode('annotate');
    } else if (EDIT && k === 'b') {
      setMode('browse');
    }
  }

  $$('[data-act="copy-ai"]').forEach(function (b) { b.addEventListener('click', copyForAI); });
  $$('[data-act="back-to-last"]').forEach(function (b) {
    b.addEventListener('click', function () { load(lastGood || cfg.site); });
  });

  // ---- The editor -------------------------------------------------------------------

  var draft = null; // { id, data, png, saved: annotation, wantShot }
  var shotsWaiting = {}; // draft id -> annotation id, for shots that arrive after saving

  function api(path, body, raw) {
    return fetch(cfg.api + path, {
      method: 'POST',
      headers: raw ? { 'Content-Type': 'image/png' } : { 'Content-Type': 'application/json' },
      body: raw ? body : JSON.stringify(body)
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (data) {
        if (!r.ok) throw new Error(data.error || 'Something went wrong. Try again.');
        return data;
      });
    });
  }

  function setMode(m) {
    mode = m;
    $$('[data-mode]').forEach(function (b) { b.setAttribute('aria-pressed', String(b.dataset.mode === mode)); });
    $('.tools').setAttribute('aria-disabled', String(mode !== 'annotate'));
    $('#stage').classList.toggle('annotating', mode === 'annotate');
    if (mode !== 'annotate') cancelDraft();
    hint();
    sendMode();
  }

  function setTool(t) {
    tool = t;
    $$('[data-tool]').forEach(function (b) { b.setAttribute('aria-pressed', String(b.dataset.tool === tool)); });
    if (mode !== 'annotate') setMode('annotate');
    else { hint(); sendMode(); }
  }

  var hintTimer = 0;
  function hint() {
    var h = $('#hint');
    clearTimeout(hintTimer);
    if (mode !== 'annotate') { h.hidden = true; return; }
    h.textContent = HINTS[tool];
    h.hidden = false;
    hintTimer = setTimeout(function () { h.hidden = true; }, 4000);
  }

  function shotData(png) {
    var bin = atob(png.split(',')[1]), bytes = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return new Blob([bytes], { type: 'image/png' });
  }

  function uploadShot(annotationID, png) {
    return api('/annotations/' + annotationID + '/shot', shotData(png), true).then(function () {
      var a = list.find(function (x) { return x.id === annotationID; });
      if (a) { a.hasShot = true; renderList(); }
    }).catch(function () { /* the review works without it */ });
  }

  function openDraft(m) {
    var form = $('#draft');
    draft = { id: m.id, data: m.draft || {}, png: undefined };
    form.comment.value = '';
    pickLabel('change');
    shotState('Taking a screenshot…');
    form.hidden = false;
    // Beside the mark, inside the window.
    var f = frame.getBoundingClientRect(), b = m.box || { x: 0, y: 0, w: 0, h: 0 };
    var w = form.offsetWidth, h = form.offsetHeight, gap = 12;
    var x = f.left + b.x + b.w + gap;
    if (x + w > window.innerWidth - 8) x = f.left + b.x - w - gap;
    if (x < 8) x = Math.min(window.innerWidth - w - 8, Math.max(8, f.left + b.x));
    var y = f.top + b.y;
    if (x === f.left + b.x || x < f.left + b.x + b.w && x + w > f.left + b.x) y = f.top + b.y + b.h + gap;
    y = Math.max(f.top + 8, Math.min(window.innerHeight - h - 8, y));
    form.style.left = Math.round(x) + 'px';
    form.style.top = Math.round(y) + 'px';
    form.comment.focus();
  }

  function shotState(text) { var s = $('#draft [data-shot]'); if (s) s.textContent = text; }

  function gotShot(m) {
    if (shotsWaiting[m.id]) {
      if (m.png) uploadShot(shotsWaiting[m.id], m.png);
      delete shotsWaiting[m.id];
      return;
    }
    if (!draft || draft.id !== m.id) return;
    if (!m.png && draft.png) return; // a repaint that failed: keep the one we have
    draft.png = m.png || null;
    shotState(m.png ? 'Screenshot taken' : 'No screenshot: this page couldn\'t be photographed');
    if (!m.png && m.error) console.warn('PinPoint screenshot:', m.error);
  }

  function pickLabel(label) {
    $$('#draft [data-label]').forEach(function (b) { b.setAttribute('aria-pressed', String(b.dataset.label === label)); });
    if (draft) draft.data.label = label;
    send({ pp: 'draft-label', label: label });
  }

  function cancelDraft(silent) {
    var form = $('#draft');
    if (!form || form.hidden) return;
    form.hidden = true;
    draft = null;
    if (!silent) send({ pp: 'draft-done' });
  }

  function saveDraft(e) {
    e.preventDefault();
    if (!draft) return;
    var d = draft, form = $('#draft');
    d.data.comment = form.comment.value;
    var btn = $('button[type=submit]', form);
    btn.disabled = true;
    api('/annotations', d.data).then(function (a) {
      list.push(a);
      if (d.png) uploadShot(a.id, d.png);
      else if (d.png === undefined) shotsWaiting[d.id] = a.id;
      form.hidden = true;
      draft = null;
      send({ pp: 'draft-done' });
      renderList();
      showMarks();
      remember();
    }).catch(function (err) {
      toast(err.message, 'error');
    }).then(function () { btn.disabled = false; });
  }

  // ---- The list, in the editor ------------------------------------------------------

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function icon(name, cls) { return el('i', 'ti ti-' + name + (cls ? ' ' + cls : '')); }

  function renderList() {
    var box = $('#notes-list');
    var keep = box.scrollTop;
    box.textContent = '';
    $$('[data-count]').forEach(function (c) { c.textContent = list.length; });
    if (!list.length) {
      var empty = el('div', 'note-empty');
      empty.append(icon('message-circle'), el('div', 'fw-bold mb-1', 'Nothing marked yet'),
        el('div', 'small', 'Pick a tool above and click, select, box or draw on the page. Each mark gets a comment.'));
      box.append(empty);
      return;
    }
    var lastPage = '';
    list.forEach(function (a, i) {
      if (pageKey(a.pageUrl) !== lastPage) {
        lastPage = pageKey(a.pageUrl);
        var g = el('div', 'page-group');
        g.append(icon('file'), document.createTextNode(pageLabel(a.pageUrl)), el('span', '', a.pageTitle || ''));
        box.append(g);
      }
      box.append(noteItem(a, i + 1));
    });
    box.scrollTop = keep;
    markMoved();
  }

  function noteItem(a, n) {
    var item = el('article', 'note' + (a.id === selected ? ' selected' : ''));
    item.dataset.id = a.id;
    item.tabIndex = 0;
    item.setAttribute('aria-label', 'Annotation ' + n);
    var body = el('div', 'body');
    var meta = el('div', 'meta');
    meta.append(el('span', 'fw-bold text-' + a.label, LABELS[a.label].title));
    var moved = el('span', 'moved');
    moved.dataset.moved = '';
    moved.hidden = true;
    moved.title = 'The element this was attached to has moved or is gone';
    moved.append(icon('alert-triangle'), document.createTextNode(' moved'));
    meta.append(moved);
    body.append(meta);
    if (a.quote) body.append(el('div', 'quote', '“' + a.quote + '”'));
    body.append(a.comment ? el('p', 'comment', a.comment) : el('p', 'comment empty', 'No comment; the mark says it.'));
    var actions = el('div', 'actions');
    var edit = el('button', 'btn btn-icon btn-sm btn-ghost-secondary');
    edit.type = 'button';
    edit.title = 'Edit';
    edit.setAttribute('aria-label', 'Edit annotation ' + n);
    edit.append(icon('pencil'));
    edit.addEventListener('click', function () { editItem(item, a); });
    var del = el('button', 'btn btn-icon btn-sm btn-ghost-danger');
    del.type = 'button';
    del.title = 'Delete';
    del.setAttribute('aria-label', 'Delete annotation ' + n);
    del.append(icon('trash'));
    del.addEventListener('click', function () { removeItem(a); });
    actions.append(edit, del);
    var num = el('span', 'num ' + a.label, String(n));
    item.append(num, body, actions);
    return item;
  }

  function editItem(item, a) {
    var body = $('.body', item);
    var form = el('form', 'mt-1');
    var pick = el('div', 'label-pick mb-2');
    var label = a.label;
    Object.keys(LABELS).forEach(function (k) {
      var b = el('button');
      b.type = 'button';
      b.dataset.label = k;
      b.setAttribute('aria-pressed', String(k === label));
      b.append(icon(LABELS[k].icon), document.createTextNode(LABELS[k].title));
      b.addEventListener('click', function () {
        label = k;
        $$('button', pick).forEach(function (x) { x.setAttribute('aria-pressed', String(x.dataset.label === k)); });
      });
      pick.append(b);
    });
    var ta = el('textarea', 'form-control mb-2');
    ta.rows = 3;
    ta.maxLength = 4000;
    ta.value = a.comment;
    ta.setAttribute('aria-label', 'Comment');
    var row = el('div', 'd-flex justify-content-end gap-2');
    var cancel = el('button', 'btn btn-sm', 'Cancel');
    cancel.type = 'button';
    cancel.addEventListener('click', renderList);
    var save = el('button', 'btn btn-sm btn-primary', 'Save');
    save.type = 'submit';
    row.append(cancel, save);
    form.append(pick, ta, row);
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      save.disabled = true;
      api('/annotations/' + a.id, { label: label, comment: ta.value }).then(function (updated) {
        a.label = updated.label;
        a.comment = updated.comment;
        renderList();
        showMarks();
      }).catch(function (err) { toast(err.message, 'error'); save.disabled = false; });
    });
    form.addEventListener('keydown', function (e) { if (e.key === 'Escape') renderList(); });
    body.textContent = '';
    body.append(form);
    $('.actions', item).hidden = true;
    ta.focus();
  }

  function removeItem(a) {
    if (!confirm('Delete annotation ' + numberOf(a) + '?')) return;
    api('/annotations/' + a.id + '/delete', {}).then(function () {
      list = list.filter(function (x) { return x !== a; });
      if (selected === a.id) selected = 0;
      renderList();
      showMarks();
      remember();
    }).catch(function (err) { toast(err.message, 'error'); });
  }

  // remember keeps this review in the browser's list on the start page, as
  // there is no account to keep it in.
  function remember() {
    try {
      var all = JSON.parse(localStorage.getItem('pinpoint.reviews') || '[]').filter(function (r) { return r.edit !== location.pathname; });
      all.unshift({ edit: location.pathname, host: new URL(cfg.site).host, title: cfg.title, count: list.length, sent: cfg.applied, at: Date.now() });
      localStorage.setItem('pinpoint.reviews', JSON.stringify(all.slice(0, 30)));
    } catch (e) {}
  }

  // ---- Applying ---------------------------------------------------------------------

  function openApply() {
    cancelDraft();
    var dlg = $('#apply-dialog');
    if (!list.length) {
      toast('Mark at least one thing on the page first.', 'info');
      return;
    }
    var pages = {};
    list.forEach(function (a) { pages[pageKey(a.pageUrl)] = true; });
    var n = Object.keys(pages).length;
    $('[data-summary]', dlg).textContent = list.length + (list.length === 1 ? ' annotation' : ' annotations') +
      (n > 1 ? ' on ' + n + ' pages' : '') + '. You get a link to send to whoever builds the site.';
    try { if (!dlg.querySelector('#reviewer').value) dlg.querySelector('#reviewer').value = localStorage.getItem('pinpoint.name') || ''; } catch (e) {}
    $('[data-error]', dlg).hidden = true;
    dlg.showModal();
  }

  function apply(e) {
    e.preventDefault();
    var dlg = $('#apply-dialog'), name = $('#reviewer').value.trim();
    var btn = $('button[type=submit]', dlg);
    btn.disabled = true;
    api('/apply', { name: name }).then(function (res) {
      try { localStorage.setItem('pinpoint.name', name); } catch (err) {}
      cfg.applied = true;
      $('#sent-note').hidden = false;
      remember();
      dlg.close();
      $('#share-url').value = res.url;
      $('#share-dialog').showModal();
    }).catch(function (err) {
      var box = $('[data-error]', dlg);
      box.textContent = err.message;
      box.hidden = false;
    }).then(function () { btn.disabled = false; });
  }

  function address(e) {
    e.preventDefault();
    var raw = $('#address').value.trim();
    if (!raw) return;
    var site = new URL(cfg.site), url;
    try {
      url = raw.charAt(0) === '/' ? new URL(raw, site.origin) : new URL(/^[a-z]+:\/\//i.test(raw) ? raw : 'https://' + raw);
    } catch (err) {
      toast('That isn\'t a web address.', 'error');
      return;
    }
    if (siteKey(url.host) !== siteKey(cfg.host)) {
      toast('This review is for ' + cfg.host + '. Other sites open in a new tab.', 'info');
      window.open(url.href, '_blank', 'noopener');
      return;
    }
    $('#address').blur();
    load(url.href);
  }

  // ---- Start ----------------------------------------------------------------------------

  if (EDIT) {
    $$('[data-mode]').forEach(function (b) { b.addEventListener('click', function () { setMode(b.dataset.mode); }); });
    $$('[data-tool]').forEach(function (b) { b.addEventListener('click', function () { setTool(b.dataset.tool); }); });
    $$('#draft [data-label]').forEach(function (b) { b.addEventListener('click', function () { pickLabel(b.dataset.label); }); });
    $('#draft').addEventListener('submit', saveDraft);
    $('#draft').addEventListener('keydown', function (e) {
      if (e.key === 'Escape') cancelDraft();
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) saveDraft(e);
    });
    $('[data-act="cancel-draft"]').addEventListener('click', function () { cancelDraft(); });
    $$('[data-act="apply"]').forEach(function (b) { b.addEventListener('click', openApply); });
    $('#apply-form').addEventListener('submit', apply);
    $$('[data-act="close-dialog"]').forEach(function (b) { b.addEventListener('click', function () { b.closest('dialog').close(); }); });
    $('[data-act="copy-link"]').addEventListener('click', function () { copy($('#share-url').value, 'Link copied'); });
    $$('[data-act="notes"]').forEach(function (b) { b.addEventListener('click', function () { $('#notes').classList.toggle('open'); }); });
    $('[data-act="back"]').addEventListener('click', function () { send({ pp: 'back' }); });
    $('[data-act="reload"]').addEventListener('click', function () { load(lastGood || cfg.site); });
    $('#address-form').addEventListener('submit', address);
    document.addEventListener('keydown', function (e) {
      var t = e.target;
      if (t.isContentEditable || /^(input|textarea|select)$/i.test(t.localName) || e.ctrlKey || e.metaKey || e.altKey) {
        if (e.key === 'Escape' && t.closest('#draft')) cancelDraft();
        return;
      }
      if (e.key === 'Escape' || e.key === 'a' || e.key === 'b') key(e.key);
    });
    renderList();
    remember();
    setMode('annotate');
    var start = cfg.site;
    try { start = localStorage.getItem('pinpoint.last.' + cfg.api) || cfg.site; } catch (e) {}
    load(start);
  } else {
    load(list.length ? list[0].pageUrl : cfg.site);
  }
})();
