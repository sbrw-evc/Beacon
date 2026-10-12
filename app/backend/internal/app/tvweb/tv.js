/* Umbrella TV wallboard: polls /api/public/tv/<slug> and renders the incidents.
   Plain ES2017 for old smart-TV and kiosk browsers: no modules, no optional chaining, no "??". */
(function () {
  'use strict';

  var PAGE_SECONDS = 15;
  var NEW_FLASH_MS = 10000;
  var CURSOR_HIDE_MS = 3000;
  var FETCH_TIMEOUT_MS = 15000;
  // Keys of the counters that are not levels of the alert scale.
  var COUNT_KEYS = { total: true, acknowledged: true, open: true };

  var STRINGS = {
    ru: {
      'cnt.acknowledged': 'Подтверждены',
      'st.open': 'Открыт',
      'st.acknowledged': 'Подтверждён',
      'st.resolved': 'Решён',
      'badge.suppressed': 'Обслуживание',
      'badge.fallback': 'Резервное оповещение',
      'badge.count': '×{n}',
      'conn.ok': 'В сети',
      'conn.bad': 'Нет связи',
      'conn.wait': 'Подключение',
      'updated': 'обновлено {t}',
      'lost': 'Связь потеряна · последнее обновление {t}',
      'lost.never': 'Нет связи с сервером',
      'lost.retry': 'Повторная попытка через {s} с',
      'empty': 'Нет активных инцидентов',
      'empty.sub': 'Всё работает штатно · {t}',
      'wait': 'Ожидание данных…',
      'wait.sub': 'Система мониторинга запускается',
      'connecting': 'Подключение…',
      'more': 'Показано {shown} из {total} · ещё +{n} не в списке',
      'opened.today': 'с {t}',
      'opened.date': 'с {d}, {t}',
      'resolved.at': 'решён в {t}',
      'dur.lt1': '<1 мин',
      'dur.m': '{m} мин',
      'dur.hm': '{h} ч {mm} мин',
      'dur.d': '{d} д',
      'dur.dh': '{d} д {h} ч',
      'aria.counts': 'Счётчики инцидентов',
      'page': 'Страница {p} из {n}'
    },
    en: {
      'cnt.acknowledged': 'Acknowledged',
      'st.open': 'Open',
      'st.acknowledged': 'Acknowledged',
      'st.resolved': 'Resolved',
      'badge.suppressed': 'Maintenance',
      'badge.fallback': 'Backup notification',
      'badge.count': '×{n}',
      'conn.ok': 'Online',
      'conn.bad': 'Offline',
      'conn.wait': 'Connecting',
      'updated': 'updated {t}',
      'lost': 'Connection lost · last update {t}',
      'lost.never': 'Cannot reach the server',
      'lost.retry': 'Retrying in {s} s',
      'empty': 'No active incidents',
      'empty.sub': 'All systems normal · {t}',
      'wait': 'Waiting for data…',
      'wait.sub': 'The monitoring engine is starting',
      'connecting': 'Connecting…',
      'more': 'Showing {shown} of {total} · +{n} more not listed',
      'opened.today': 'since {t}',
      'opened.date': 'since {d}, {t}',
      'resolved.at': 'resolved at {t}',
      'dur.lt1': '<1 min',
      'dur.m': '{m} min',
      'dur.hm': '{h} h {mm} min',
      'dur.d': '{d} d',
      'dur.dh': '{d} d {h} h',
      'aria.counts': 'Incident counters',
      'page': 'Page {p} of {n}'
    }
  };

  var DENIED = {
    ru: {
      title: 'Эта панель недоступна с этого адреса',
      text: 'Попросите администратора Umbrella добавить этот адрес в разрешённые сети ТВ-панели. Страница откроется сама, как только доступ появится.',
      unknown: 'адрес неизвестен'
    },
    en: {
      title: 'This wallboard is not available from this address',
      text: 'Ask your Umbrella administrator to add this address to the wallboard’s allowed networks. The page will open by itself once access is granted.',
      unknown: 'unknown address'
    }
  };

  // ---------- state ----------

  var state = {
    slug: '',
    lang: 'ru',
    data: null,            // last successful payload
    lastOk: null,          // Date of the last successful poll
    version: null,         // first version seen
    knownIds: null,        // ids of the previous successful poll (null before the first one)
    newUntil: {},          // id -> timestamp until which the row pulses
    failing: false,
    denied: false,
    refreshMs: 30000,
    pollTimer: 0,
    pages: [[]],           // arrays of row indexes
    page: 0,
    pageTimer: 0,
    pageStarted: 0,
    lastCounts: '',
    theme: '',
    severities: [],        // the alert scale, most severe first (see scaleOf)
    zone: ''               // IANA time zone of the clock and times ('' = the browser's)
  };

  var el = {};
  function $(id) { return document.getElementById(id); }

  // ---------- helpers ----------

  function fmt(s, vars) {
    return String(s).replace(/\{(\w+)\}/g, function (m, k) {
      return vars && vars[k] !== undefined && vars[k] !== null ? String(vars[k]) : m;
    });
  }
  function t(key, vars) {
    var dict = STRINGS[state.lang] || STRINGS.ru;
    var s = dict[key];
    if (s === undefined) s = STRINGS.en[key];
    if (s === undefined) s = key;
    return fmt(s, vars);
  }
  function pad2(n) { return n < 10 ? '0' + n : String(n); }
  // wall is the moment as the clock of the board's time zone shows it: a Date whose local
  // fields are that wall time. Without a zone (or a browser that does not know it) the
  // browser's own time is used.
  var zoneFmt = null;
  function setZone(zone) {
    zone = String(zone || '');
    if (zone === state.zone) return;
    state.zone = zone;
    zoneFmt = null;
    if (!zone || !window.Intl || !Intl.DateTimeFormat) return;
    try {
      zoneFmt = new Intl.DateTimeFormat('en-US', { timeZone: zone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric' });
    } catch (e) {
      zoneFmt = null;
    }
  }
  function wall(d) {
    if (!zoneFmt || !zoneFmt.formatToParts) return d;
    var p = {};
    var parts = zoneFmt.formatToParts(d);
    for (var i = 0; i < parts.length; i++) p[parts[i].type] = parseInt(parts[i].value, 10);
    return new Date(p.year, p.month - 1, p.day, p.hour % 24, p.minute, p.second);
  }
  function hhmm(d) { d = wall(d); return pad2(d.getHours()) + ':' + pad2(d.getMinutes()); }
  function hhmmss(d) { var w = wall(d); return pad2(w.getHours()) + ':' + pad2(w.getMinutes()) + ':' + pad2(w.getSeconds()); }
  function ddmm(d, withYear) {
    d = wall(d);
    var s = pad2(d.getDate()) + '.' + pad2(d.getMonth() + 1);
    return withYear ? s + '.' + d.getFullYear() : s;
  }
  function sameDay(a, b) {
    a = wall(a);
    b = wall(b);
    return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  }
  function parseDate(s) {
    if (!s) return null;
    var d = new Date(s);
    return isNaN(d.getTime()) ? null : d;
  }
  function node(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined && text !== null) n.textContent = String(text);
    return n;
  }
  function clear(n) { while (n.firstChild) n.removeChild(n.firstChild); }
  // scaleOf reads the alert scale of a payload: name, priority (P1…P5), tone and the words in
  // each language, most severe first. A payload without it (an older server) gets a scale made
  // of the severity counters it carries, in their order, shown by name.
  function scaleOf(json) {
    var out = [];
    var list = Array.isArray(json.severities) ? json.severities : [];
    for (var i = 0; i < list.length; i++) {
      var s = list[i];
      if (!s || !s.name) continue;
      out.push({ name: String(s.name), priority: s.priority ? String(s.priority) : '', tone: String(s.tone || s.name), title: s.title || {} });
    }
    if (out.length) return out;
    var c = json.counts || {};
    for (var k in c) {
      if (Object.prototype.hasOwnProperty.call(c, k) && !COUNT_KEYS[k] && typeof c[k] === 'number') {
        out.push({ name: k, priority: '', tone: k, title: {} });
      }
    }
    return out;
  }
  // sevOf is the level of a severity name; an unknown one is shown as the mildest level.
  function sevOf(name) {
    var list = state.severities;
    for (var i = 0; i < list.length; i++) if (list[i].name === name) return list[i];
    if (list.length) return list[list.length - 1];
    return { name: String(name || ''), priority: '', tone: 'info', title: {} };
  }
  // sevText is the priority and the word of a level, e.g. "P1 · Critical".
  function sevText(s) {
    var word = (s.title && (s.title[state.lang] || s.title.en)) || s.name;
    return s.priority ? s.priority + ' · ' + word : word;
  }
  function pickLang(l) {
    l = String(l || '').toLowerCase();
    if (l.indexOf('ru') === 0) return 'ru';
    if (l.indexOf('en') === 0) return 'en';
    return '';
  }
  function browserLang() {
    var langs = (navigator.languages && navigator.languages.length) ? navigator.languages : [navigator.language || 'ru'];
    for (var i = 0; i < langs.length; i++) {
      var l = pickLang(langs[i]);
      if (l) return l;
    }
    return 'ru';
  }

  function duration(ms) {
    var min = Math.floor(Math.max(0, ms) / 60000);
    if (min < 1) return t('dur.lt1');
    if (min < 60) return t('dur.m', { m: min });
    var h = Math.floor(min / 60);
    if (h < 24) return t('dur.hm', { h: h, mm: pad2(min % 60) });
    var d = Math.floor(h / 24);
    if (d < 3 && h % 24) return t('dur.dh', { d: d, h: h % 24 });
    return t('dur.d', { d: d });
  }

  function openedText(inc, now) {
    var res = inc.status === 'resolved' ? parseDate(inc.resolved_at) : null;
    if (res) return t('resolved.at', { t: sameDay(res, now) ? hhmm(res) : ddmm(res) + ' ' + hhmm(res) });
    var o = parseDate(inc.opened_at) || parseDate(inc.first_seen);
    if (!o) return '';
    if (sameDay(o, now)) return t('opened.today', { t: hhmm(o) });
    return t('opened.date', { d: ddmm(o, wall(o).getFullYear() !== wall(now).getFullYear()), t: hhmm(o) });
  }

  function durationText(inc, now) {
    var o = parseDate(inc.opened_at) || parseDate(inc.first_seen);
    if (!o) return '';
    var end = inc.status === 'resolved' ? (parseDate(inc.resolved_at) || now) : now;
    return duration(end.getTime() - o.getTime());
  }

  // ---------- slug ----------

  function readSlug() {
    var parts = location.pathname.split('/').filter(function (p) { return p !== ''; });
    var i = parts.indexOf('tv');
    var raw = i >= 0 && parts.length > i + 1 ? parts[i + 1] : parts[parts.length - 1] || '';
    try { return decodeURIComponent(raw); } catch (e) { return raw; }
  }

  // ---------- clock & cursor ----------

  var lastClock = '';
  function tickClock() {
    var s = hhmm(new Date());
    if (s !== lastClock) {
      lastClock = s;
      el.clock.textContent = s;
    }
  }

  function setupCursor() {
    var timer = 0;
    function hide() { document.body.classList.add('no-cursor'); }
    function show() {
      document.body.classList.remove('no-cursor');
      clearTimeout(timer);
      timer = setTimeout(hide, CURSOR_HIDE_MS);
    }
    document.addEventListener('mousemove', show, { passive: true });
    document.addEventListener('mousedown', show, { passive: true });
    hide();
  }

  // ---------- networking ----------

  function fetchBoard() {
    var url = '/api/public/tv/' + encodeURIComponent(state.slug);
    var ctrl = typeof AbortController === 'function' ? new AbortController() : null;
    var opts = { cache: 'no-store', credentials: 'same-origin', headers: { Accept: 'application/json' } };
    if (ctrl) opts.signal = ctrl.signal;
    var to = 0;
    var p = fetch(url, opts).then(function (res) {
      return res.text().then(function (body) {
        var json = null;
        try { json = body ? JSON.parse(body) : null; } catch (e) { json = null; }
        return { status: res.status, ok: res.ok, json: json };
      });
    });
    var timeout = new Promise(function (resolve, reject) {
      to = setTimeout(function () {
        if (ctrl) { try { ctrl.abort(); } catch (e) { /* ignore */ } }
        reject(new Error('timeout'));
      }, FETCH_TIMEOUT_MS);
    });
    return Promise.race([p, timeout]).then(function (r) {
      clearTimeout(to);
      return r;
    }, function (err) {
      clearTimeout(to);
      throw err;
    });
  }

  function schedule() {
    clearTimeout(state.pollTimer);
    state.pollTimer = setTimeout(poll, state.refreshMs);
  }

  function poll() {
    fetchBoard().then(function (r) {
      if (r.status === 403) {
        showDenied(r.json && r.json.client_ip ? String(r.json.client_ip) : '');
      } else if (r.ok && r.json && r.json.board) {
        onData(r.json);
      } else {
        onError();
      }
    }, function () {
      onError();
    }).then(schedule, schedule);
  }

  function onError() {
    state.failing = true;
    renderConnection();
  }

  function showDenied(ip) {
    state.denied = true;
    var lang = browserLang();
    var d = DENIED[lang];
    document.documentElement.lang = lang;
    clear(el.denied);
    el.denied.appendChild(node('div', 'denied-mark'));
    el.denied.appendChild(node('div', 'denied-title', d.title));
    el.denied.appendChild(node('div', 'denied-ip', ip || d.unknown));
    el.denied.appendChild(node('div', 'denied-text', d.text));
    el.denied.hidden = false;
    el.app.hidden = true;
    document.body.classList.remove('is-loading');
  }

  function onData(json) {
    if (json.version !== undefined && json.version !== null && json.version !== '') {
      if (state.version === null) state.version = json.version;
      else if (json.version !== state.version) {
        location.reload();
        return;
      }
    }
    if (state.denied) {
      state.denied = false;
      el.denied.hidden = true;
      el.app.hidden = false;
    }
    var board = json.board || {};
    var lang = pickLang(board.locale) || pickLang(json.default_locale) || 'ru';
    var langChanged = lang !== state.lang;
    var zoneBefore = state.zone;
    setZone(board.timezone);
    if (state.zone !== zoneBefore) lastClock = '';
    state.lang = lang;
    document.documentElement.lang = lang;

    var theme = board.theme === 'light' ? 'light' : 'dark';
    if (theme !== state.theme) {
      state.theme = theme;
      document.documentElement.className = 'theme-' + theme;
    }
    var rs = parseInt(board.refresh_seconds, 10);
    if (!(rs > 0)) rs = 30;
    if (rs < 5) rs = 5;
    state.refreshMs = rs * 1000;

    var incidents = Array.isArray(json.incidents) ? json.incidents : [];
    var now = Date.now();
    var ids = {};
    for (var i = 0; i < incidents.length; i++) {
      var id = incidents[i] && incidents[i].id;
      if (!id) continue;
      ids[id] = true;
      if (state.knownIds && !state.knownIds[id] && json.ready !== false) state.newUntil[id] = now + NEW_FLASH_MS;
    }
    for (var k in state.newUntil) {
      if (Object.prototype.hasOwnProperty.call(state.newUntil, k) && (!ids[k] || state.newUntil[k] <= now)) delete state.newUntil[k];
    }
    if (json.ready !== false) state.knownIds = ids;

    var scale = scaleOf(json);
    var scaleKey = JSON.stringify(scale);
    if (scaleKey !== JSON.stringify(state.severities)) state.lastCounts = '';
    state.severities = scale;
    state.data = json;
    state.lastOk = new Date();
    state.failing = false;
    if (langChanged) state.lastCounts = '';
    document.body.classList.remove('is-loading');

    renderHeader();
    renderConnection();
    renderBody();
  }

  // ---------- rendering: header ----------

  function renderHeader() {
    var d = state.data;
    var board = d.board || {};
    var title = board.title || board.slug || state.slug;
    if (el.title.textContent !== title) el.title.textContent = title;
    el.title.className = 'title' + (title.length > 28 ? ' is-long' : '');
    var desc = board.description || '';
    if (el.desc.textContent !== desc) el.desc.textContent = desc;
    document.title = title;

    var c = d.counts || {};
    // One counter per level of the scale, then the acknowledged ones.
    var items = [];
    for (var j = 0; j < state.severities.length; j++) {
      var s = state.severities[j];
      items.push([s.name, s.tone, sevText(s)]);
    }
    items.push(['acknowledged', 'ack', t('cnt.acknowledged')]);
    var values = [];
    for (var v = 0; v < items.length; v++) values.push(c[items[v][0]]);
    var key = state.lang + '|' + values.join(',');
    if (key === state.lastCounts) return;
    state.lastCounts = key;
    clear(el.counters);
    el.counters.setAttribute('aria-label', t('aria.counts'));
    for (var i = 0; i < items.length; i++) {
      var n = parseInt(c[items[i][0]], 10) || 0;
      var box = node('div', 'cnt tone-' + items[i][1] + (n === 0 ? ' zero' : ''));
      var top = node('div', 'cnt-top');
      var mk = node('span', 'mk mk-' + items[i][1]);
      mk.setAttribute('aria-hidden', 'true');
      top.appendChild(mk);
      top.appendChild(node('span', 'cnt-num', n));
      box.appendChild(top);
      box.appendChild(node('div', 'cnt-label', items[i][2]));
      el.counters.appendChild(box);
    }
  }

  function renderConnection() {
    var cls = 'conn';
    var text;
    if (state.failing) { cls += ' is-bad'; text = t('conn.bad'); }
    else if (!state.lastOk) { cls += ' is-wait'; text = t('conn.wait'); }
    else text = t('conn.ok');
    el.conn.className = cls;
    el.connText.textContent = text;
    el.updated.textContent = state.lastOk ? t('updated', { t: hhmmss(state.lastOk) }) : '';

    if (state.failing) {
      clear(el.lost);
      var icon = node('span', 'lost-icon');
      icon.setAttribute('aria-hidden', 'true');
      el.lost.appendChild(icon);
      el.lost.appendChild(node('span', '', state.lastOk ? t('lost', { t: hhmmss(state.lastOk) }) : t('lost.never')));
      var wasHidden = el.lost.hidden;
      el.lost.hidden = false;
      if (!state.data) renderConnecting(true);
      else if (wasHidden) layout();
    } else if (!el.lost.hidden) {
      el.lost.hidden = true;
      layout();
    }
  }

  // ---------- rendering: body ----------

  function showState(kind, title, sub) {
    clear(el.state);
    var mark = node('div', 'state-mark' + (kind === 'wait' ? ' is-wait' : ''));
    mark.setAttribute('aria-hidden', 'true');
    el.state.appendChild(mark);
    el.state.appendChild(node('div', 'state-title', title));
    if (sub) el.state.appendChild(node('div', 'state-sub', sub));
    el.state.className = 'state' + (kind === 'ok' ? ' is-ok' : '');
    el.state.hidden = false;
    clear(el.list);
    el.list.hidden = true;
    el.more.textContent = '';
    setPages([[]]);
  }

  function renderConnecting(failed) {
    showState('wait', failed ? t('lost.never') : t('connecting'), failed ? t('lost.retry', { s: Math.round(state.refreshMs / 1000) }) : '');
  }

  function renderBody() {
    var d = state.data;
    if (d.ready === false) {
      showState('wait', t('wait'), t('wait.sub'));
      return;
    }
    var incidents = Array.isArray(d.incidents) ? d.incidents : [];
    if (!incidents.length) {
      showState('ok', t('empty'), t('empty.sub', { t: hhmm(new Date()) }));
      return;
    }
    el.state.hidden = true;
    el.list.hidden = false;

    var now = new Date();
    var frag = document.createDocumentFragment();
    for (var i = 0; i < incidents.length; i++) frag.appendChild(buildRow(incidents[i] || {}, now));
    clear(el.list);
    el.list.appendChild(frag);

    var total = d.counts && d.counts.total > 0 ? d.counts.total : incidents.length;
    if (d.more || total > incidents.length) {
      el.more.textContent = t('more', { shown: incidents.length, total: total, n: Math.max(0, total - incidents.length) || '…' });
    } else {
      el.more.textContent = '';
    }
    layout();
  }

  function buildRow(inc, now) {
    var sev = sevOf(inc.severity);
    var status = inc.status === 'acknowledged' || inc.status === 'resolved' ? inc.status : 'open';
    var cls = 'row tone-' + sev.tone;
    if (status === 'acknowledged') cls += ' is-ack';
    if (status === 'resolved') cls += ' is-resolved';
    if (inc.id && state.newUntil[inc.id] && state.newUntil[inc.id] > now.getTime()) cls += ' is-new';
    var row = node('div', cls);
    row.setAttribute('role', 'listitem');
    row._inc = inc;

    row.appendChild(node('div', 'row-bar'));

    var sevBox = node('div', 'row-sev');
    var mk = node('span', 'mk mk-' + sev.tone);
    mk.setAttribute('aria-hidden', 'true');
    sevBox.appendChild(mk);
    var label = node('span', 'sev-label');
    if (sev.priority) label.appendChild(node('span', 'sev-p', sev.priority));
    label.appendChild(node('span', 'sev-word', (sev.title && (sev.title[state.lang] || sev.title.en)) || sev.name));
    sevBox.appendChild(label);
    row.appendChild(sevBox);

    var main = node('div', 'row-main');
    main.appendChild(node('div', 'row-title', inc.title || inc.signal || inc.id || ''));
    var meta = node('div', 'row-meta');
    var ci = node('span', 'row-ci');
    if (inc.ci_name) ci.appendChild(node('b', '', inc.ci_name));
    if (inc.ci_name && inc.signal) ci.appendChild(document.createTextNode(' · '));
    if (inc.signal) ci.appendChild(node('span', 'sig', inc.signal));
    meta.appendChild(ci);
    if (inc.count > 1) meta.appendChild(node('span', 'badge badge-count', t('badge.count', { n: inc.count })));
    if (inc.suppressed) meta.appendChild(node('span', 'badge badge-maint', t('badge.suppressed')));
    if (inc.fallback && status !== 'resolved') meta.appendChild(node('span', 'badge badge-fallback', t('badge.fallback')));
    main.appendChild(meta);
    row.appendChild(main);

    var svc = node('div', 'row-svc');
    var services = Array.isArray(inc.services) ? inc.services.filter(Boolean) : [];
    svc.appendChild(node('div', 'svc', services.join(', ')));
    svc.appendChild(node('div', 'team', inc.team || ''));
    row.appendChild(svc);

    var st = node('div', 'row-status');
    var badge = node('span', 'st st-' + status);
    if (status === 'acknowledged') {
      var am = node('span', 'mk mk-ack');
      am.setAttribute('aria-hidden', 'true');
      badge.appendChild(am);
    }
    badge.appendChild(node('span', '', t('st.' + status)));
    st.appendChild(badge);
    row.appendChild(st);

    var time = node('div', 'row-time');
    time.appendChild(node('div', 'dur', durationText(inc, now)));
    time.appendChild(node('div', 'since', openedText(inc, now)));
    row.appendChild(time);
    return row;
  }

  function updateDurations() {
    var now = new Date();
    var rows = el.list.children;
    for (var i = 0; i < rows.length; i++) {
      var inc = rows[i]._inc;
      if (!inc) continue;
      var dur = rows[i].querySelector('.dur');
      var since = rows[i].querySelector('.since');
      if (dur) dur.textContent = durationText(inc, now);
      if (since) since.textContent = openedText(inc, now);
      if (rows[i].classList.contains('is-new') && !(state.newUntil[inc.id] > now.getTime())) rows[i].classList.remove('is-new');
    }
    if (state.data && state.data.ready !== false && !el.state.hidden) {
      var sub = el.state.querySelector('.state-sub');
      var incs = state.data.incidents;
      if (sub && (!incs || !incs.length)) sub.textContent = t('empty.sub', { t: hhmm(now) });
    }
  }

  // ---------- pagination ----------

  function layout() {
    var rows = el.list.children;
    if (el.list.hidden || !rows.length) { setPages([[]]); return; }
    var i;
    for (i = 0; i < rows.length; i++) rows[i].style.display = '';
    var avail = el.list.clientHeight;
    var pages = [];
    var cur = [];
    var used = 0;
    for (i = 0; i < rows.length; i++) {
      var cs = window.getComputedStyle(rows[i]);
      var h = rows[i].offsetHeight + (parseFloat(cs.marginBottom) || 0);
      // the last row on a page does not need its bottom margin
      var need = rows[i].offsetHeight;
      if (cur.length && used + need > avail) {
        pages.push(cur);
        cur = [];
        used = 0;
      }
      cur.push(i);
      used += h;
    }
    if (cur.length) pages.push(cur);
    setPages(pages);
  }

  function setPages(pages) {
    var oldCount = state.pages.length;
    state.pages = pages.length ? pages : [[]];
    var n = state.pages.length;
    if (state.page >= n) state.page = 0;
    if (n <= 1) {
      clearTimeout(state.pageTimer);
      state.pageTimer = 0;
      state.pageStarted = 0;
    } else if (!state.pageTimer || oldCount !== n) {
      startPageTimer();
    }
    showPage();
  }

  function startPageTimer() {
    clearTimeout(state.pageTimer);
    state.pageStarted = Date.now();
    state.pageTimer = setTimeout(function () {
      state.pageTimer = 0;
      state.page = (state.page + 1) % state.pages.length;
      if (state.pages.length > 1) startPageTimer();
      showPage();
    }, PAGE_SECONDS * 1000);
  }

  function showPage() {
    var rows = el.list.children;
    var visible = {};
    var pg = state.pages[state.page] || [];
    for (var i = 0; i < pg.length; i++) visible[pg[i]] = true;
    for (i = 0; i < rows.length; i++) rows[i].style.display = visible[i] ? '' : 'none';

    var n = state.pages.length;
    clear(el.pager);
    if (n <= 1) return;
    el.pager.setAttribute('title', t('page', { p: state.page + 1, n: n }));
    if (n <= 12) {
      var dots = node('div', 'pager-dots');
      for (i = 0; i < n; i++) dots.appendChild(node('span', 'pager-dot' + (i === state.page ? ' on' : '')));
      el.pager.appendChild(dots);
    }
    el.pager.appendChild(node('span', 'pager-num', (state.page + 1) + '/' + n));
    var track = node('div', 'pager-track');
    var fill = node('div', 'pager-fill run');
    var elapsed = state.pageStarted ? Math.min(PAGE_SECONDS * 1000, Date.now() - state.pageStarted) : 0;
    fill.style.animationDuration = PAGE_SECONDS + 's';
    fill.style.animationDelay = (-elapsed / 1000) + 's';
    track.appendChild(fill);
    el.pager.appendChild(track);
  }

  // ---------- boot ----------

  function boot() {
    el.app = $('app');
    el.title = $('title');
    el.desc = $('desc');
    el.counters = $('counters');
    el.clock = $('clock');
    el.conn = $('conn');
    el.connText = $('conn-text');
    el.updated = $('updated');
    el.lost = $('lost');
    el.list = $('list');
    el.state = $('state');
    el.more = $('more');
    el.pager = $('pager');
    el.denied = $('denied');

    state.slug = readSlug();
    state.lang = browserLang();
    document.documentElement.lang = state.lang;
    el.title.textContent = state.slug;

    tickClock();
    setInterval(tickClock, 1000);
    setInterval(updateDurations, 30000);
    setupCursor();
    renderConnection();
    renderConnecting(false);

    var resizeTimer = 0;
    window.addEventListener('resize', function () {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(function () {
        if (state.data && !el.list.hidden) layout();
      }, 200);
    });

    poll();
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
})();
