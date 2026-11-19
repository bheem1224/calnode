// booking-logic.js — the PURE date/slot/format logic shared by book.html and manage.html, so a
// change is made once instead of twice. No DOM.
// Served inlined into the book/manage Go templates, and require()-able by the node tests
// (booking-logic.test.js). Same UMD pattern as room-logic.js — no build step, stays
// framework-free.
//
// NOT loaded by embed.js. The widget is served as its own standalone file
// (internal/handler/embed_handler.go serves the embedded bytes unmodified), so `BookingLogic`
// is undefined inside it and it carries its own copies of the few helpers it needs — see the
// comments on its dowLabels and fmt. Anything added here that all three surfaces need has to be
// mirrored there deliberately.
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.BookingLogic = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  function pad2(n) { return (n < 10 ? '0' : '') + n; }

  // dateKeyFromISO — the "YYYY-MM-DD" a slot belongs to, in the SELECTED timezone. Correct: uses
  // Intl with an explicit tz, NOT new Date().toLocaleDateString() (which keys off the browser tz
  // and was the latent bug in book.html/manage.html). This is the grouping key for slots-by-day.
  function dateKeyFromISO(iso, tz) {
    var p = new Intl.DateTimeFormat('en-CA', {
      timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit'
    }).format(new Date(iso));
    return p; // en-CA already yields YYYY-MM-DD
  }

  // ymd — "YYYY-MM-DD" for a local Date (the calendar grid's own day cells).
  function ymd(d) { return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()); }

  // groupSlotsByDay — { "YYYY-MM-DD": [slot,…] } in the selected tz. Slots are {start,…} (or pass
  // a `key` selector for shapes that differ). Optionally drops one slot (reschedule excludes the
  // current booking's own time).
  function groupSlotsByDay(slots, tz, excludeStart) {
    var by = {};
    (slots || []).forEach(function (s) {
      if (excludeStart && s.start === excludeStart) return;
      var k = dateKeyFromISO(s.start, tz);
      (by[k] = by[k] || []).push(s);
    });
    return by;
  }

  // mergeDaySlots — one day's entries in time order, each tagged `.taken`, for event
  // types that show already-booked times greyed out instead of hiding them.
  //
  // Free and taken arrive as separate arrays from the API and are only ever combined
  // here, for display. Keeping them apart on the wire is deliberate: a merged list is
  // one field away from a client submitting a taken start as a booking.
  function mergeDaySlots(free, taken) {
    var out = [];
    (free || []).forEach(function (s) { out.push(withTaken(s, false)); });
    (taken || []).forEach(function (s) { out.push(withTaken(s, true)); });
    // Parsed rather than string-compared: slot times carry a UTC offset, and two
    // entries on the same calendar day can straddle a DST change and sort wrongly.
    out.sort(function (a, b) { return Date.parse(a.start) - Date.parse(b.start); });
    return out;
  }

  function withTaken(slot, taken) {
    var copy = {};
    for (var k in slot) { if (Object.prototype.hasOwnProperty.call(slot, k)) copy[k] = slot[k]; }
    copy.taken = taken;
    return copy;
  }


  // formatTime — "1:30 PM" in the selected tz.
  function formatTime(iso, tz, locale, clockFormat) {
    return new Intl.DateTimeFormat(locale || [], {
      timeZone: tz, hour: 'numeric', minute: '2-digit',
      ...(clockFormat === '12h' || clockFormat === '24h' ? { hour12: clockFormat === '12h' } : {})
    }).format(new Date(iso));
  }

  // formatDay — a date label in the selected tz. style 'short' → "Mon, Jan 15"; 'long' →
  // "Monday, January 15".
  function formatDay(iso, tz, style, locale) {
    var long = style === 'long';
    return new Intl.DateTimeFormat(locale || [], {
      timeZone: tz, weekday: long ? 'long' : 'short',
      month: long ? 'long' : 'short', day: 'numeric'
    }).format(new Date(iso));
  }

  // dowIndex — Monday-first weekday index (0=Mon … 6=Sun) for the calendar grid offset.
  function dowIndex(date) { return (date.getDay() + 6) % 7; }

  // dowLabels — Monday-first weekday header labels (2-char abbreviations), via Intl for
  // the given locale rather than a hardcoded English array. Replaces the old
  // ['Mo','Tu',…] literal that was duplicated (in English, regardless of visitor
  // language) across book.html/manage.html. 2024-01-01 is an arbitrary fixed Monday
  // anchor — only its weekday matters, not the actual date.
  function dowLabels(locale) {
    var labels = [];
    var monday = new Date(Date.UTC(2024, 0, 1));
    for (var i = 0; i < 7; i++) {
      var d = new Date(monday.getTime() + i * 86400000);
      var full = new Intl.DateTimeFormat(locale || [], { weekday: 'short', timeZone: 'UTC' }).format(d);
      labels.push(full.slice(0, 2));
    }
    return labels;
  }

  function startOfMonth(d) { return new Date(d.getFullYear(), d.getMonth(), 1); }
  function endOfMonth(d) { return new Date(d.getFullYear(), d.getMonth() + 1, 0); }
  function addMonths(d, n) { return new Date(d.getFullYear(), d.getMonth() + n, 1); }
  function daysInMonth(year, month) { return new Date(year, month + 1, 0).getDate(); }

  // fmt — argument substitution for the translated strings the booking surfaces render
  // themselves, so the three of them don't each grow their own. Supports exactly the two
  // forms the locale files use for these keys: plain %s, taken in order, and the indexed
  // %[n]s that lets a translation reorder its arguments ("%[1]s has no available times on
  // %[2]s" is date-first in several languages). Server-side, Go's fmt does this job; this
  // is the client half of the same contract.
  //
  // Deliberately not a printf. Accepting %d without implementing number formatting would
  // be worse than not claiming to: the keys these surfaces substitute carry %s only, and
  // internal/i18n's verb-parity test holds every locale to English's verbs.
  //
  // A missing argument renders as an empty string rather than leaving "%s" on screen —
  // visibly wrong copy beats a literal format verb in front of a customer.
  function fmt(template, args) {
    var list = args || [];
    var next = 0;
    return String(template).replace(/%(?:\[(\d+)\])?s/g, function (_match, index) {
      var pick = index ? Number(index) - 1 : next++;
      var value = list[pick];
      return value === undefined || value === null ? '' : String(value);
    });
  }

  // NOTE: there is deliberately no host-label helper here. Each surface builds its own
  // (hostsLabel in book.go for the server-rendered page, in book.html's script for the
  // post-slot-pick rewrite, and in embed.js), because the label needs the resolved locale's
  // separator/conjunction keys and this module is locale-free by design. A copy used to
  // live here, exported and unit-tested but called by nothing — which made it a trap: it
  // hardcoded English " & " and would have silently un-translated the label for anyone who
  // consolidated onto it. If these are ever unified, the shared version must take the
  // locale's list_separator/list_conjunction, not hardcode punctuation.

  // Enhance the native select without changing its values, validation or change events.
  // Mirrored in embed.js, which is standalone and cannot use BookingLogic.
  function enhanceLocationSelect(select) {
    if (!select || select.hidden) return;
    var wrapper = document.createElement('div'); wrapper.className = 'location-picker';
    select.before(wrapper); wrapper.appendChild(select);
    var trigger = document.createElement('button'); trigger.type = 'button'; trigger.className = 'location-trigger'; trigger.id = select.id + '-trigger';
    trigger.setAttribute('aria-haspopup', 'listbox'); trigger.setAttribute('aria-expanded', 'false');
    var value = document.createElement('span'); value.className = 'location-value';
    var chevron = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    chevron.setAttribute('viewBox', '0 0 24 24'); chevron.setAttribute('aria-hidden', 'true');
    chevron.innerHTML = '<path d="m6 9 6 6 6-6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>';
    trigger.append(value, chevron);
    var panel = document.createElement('div'); panel.className = 'location-options'; panel.id = select.id + '-options'; panel.hidden = true;
    panel.setAttribute('role', 'listbox'); trigger.setAttribute('aria-controls', panel.id);
    if (panel.showPopover) panel.setAttribute('popover', 'auto');
    var label = select.parentElement.parentElement.querySelector('label');
    if (label) { label.htmlFor = trigger.id; label.id = select.id + '-label'; panel.setAttribute('aria-labelledby', label.id); }
    wrapper.append(trigger, panel); select.hidden = true;
    var items = [], outsideEvents;
    function sync() {
      value.textContent = select.options[select.selectedIndex]?.textContent || '';
      trigger.classList.toggle('is-placeholder', select.value === '');
      trigger.disabled = select.disabled;
      items.forEach(function (item) { item.setAttribute('aria-selected', String(item.dataset.value === select.value)); });
    }
    function close(focus) {
      if (panel.hidePopover && panel.matches(':popover-open')) panel.hidePopover();
      panel.hidden = true; trigger.setAttribute('aria-expanded', 'false');
      if (outsideEvents) outsideEvents.abort();
      if (focus) trigger.focus();
    }
    Array.from(select.options).forEach(function (option) {
      if (option.value === '' || option.disabled) return;
      var item = document.createElement('div'); item.className = 'location-option'; item.tabIndex = -1;
      item.setAttribute('role', 'option'); item.dataset.value = option.value; item.textContent = option.textContent;
      item.addEventListener('click', function () { select.value = option.value; select.dispatchEvent(new Event('change', { bubbles: true })); close(true); });
      items.push(item); panel.appendChild(item);
    });
    function open(last) {
      sync(); panel.hidden = false; trigger.setAttribute('aria-expanded', 'true');
      if (panel.showPopover) {
        var rect = trigger.getBoundingClientRect(), height = Math.min(240, items.length * 40 + 12);
        panel.style.width = Math.min(rect.width, innerWidth - 16) + 'px';
        panel.style.left = Math.max(8, Math.min(rect.left, innerWidth - rect.width - 8)) + 'px';
        panel.style.top = (rect.bottom + height + 8 <= innerHeight ? rect.bottom + 6 : Math.max(8, rect.top - height - 6)) + 'px';
        panel.showPopover();
        outsideEvents = new AbortController();
        window.addEventListener('resize', function () { close(false); }, { signal: outsideEvents.signal });
        window.addEventListener('scroll', function (event) { if (!event.composedPath().includes(panel)) close(false); }, { capture: true, signal: outsideEvents.signal });
      }
      (items.find(function (item) { return item.dataset.value === select.value; }) || items[last ? items.length - 1 : 0])?.focus();
    }
    trigger.addEventListener('click', function () { if (panel.hidden) open(false); else close(false); });
    trigger.addEventListener('keydown', function (event) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); open(event.key === 'ArrowUp'); }
    });
    var typed = '', typedAt = 0;
    panel.addEventListener('keydown', function (event) {
      var index = items.indexOf(select.getRootNode().activeElement), next;
      if (event.key === 'Escape') { event.preventDefault(); close(true); return; }
      if (event.key === 'Tab') { close(true); return; }
      if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); items[index]?.click(); return; }
      if (event.key === 'ArrowDown') next = (index + 1) % items.length;
      if (event.key === 'ArrowUp') next = (index - 1 + items.length) % items.length;
      if (event.key === 'Home') next = 0;
      if (event.key === 'End') next = items.length - 1;
      if (next !== undefined) { event.preventDefault(); items[next]?.focus(); }
      else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
        typed = (Date.now() - typedAt < 700 ? typed : '') + event.key.toLocaleLowerCase(); typedAt = Date.now();
        items.find(function (item) { return item.textContent.toLocaleLowerCase().startsWith(typed); })?.focus();
      }
    });
    panel.addEventListener('toggle', function (event) { if (event.newState === 'closed' && !panel.hidden) close(false); });
    wrapper.addEventListener('focusout', function (event) { if (!wrapper.contains(event.relatedTarget)) close(false); });
    select.addEventListener('invalid', function (event) { event.preventDefault(); trigger.setAttribute('aria-invalid', 'true'); trigger.focus(); });
    select.addEventListener('change', function () { trigger.removeAttribute('aria-invalid'); sync(); });
    sync();
  }

  // Inline brand marks avoid third-party image requests on private booking pages.
  function calendarActionIcon(kind) {
    var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 24 24'); svg.setAttribute('aria-hidden', 'true'); svg.setAttribute('focusable', 'false'); svg.classList.add('calendar-action-icon');
    if (kind === 'google') svg.innerHTML = '<path fill="#fff" d="M5 5h14v14H5z"/><path fill="#4285F4" d="M5 5h14v4H5zM5 9h4v10H5z"/><path fill="#34A853" d="M9 19h10v-4h4v4l-4 4H9z"/><path fill="#FBBC04" d="M19 9h4v10h-4z"/><path fill="#EA4335" d="M19 19h4l-4 4z"/><path fill="#188038" d="M5 19h4v4H5z"/><path fill="#1967D2" d="M1 5a4 4 0 0 1 4-4h14v4H5v14H1z"/><path fill="#FBBC04" d="M19 1a4 4 0 0 1 4 4v4h-4z"/><path fill="#4285F4" d="M10 10h4v1.4l-1.5 1 1.5 1V16h-4v-1.4h2.5v-.8l-1.5-1v-.9l1.5-1H10zm5 0h2v6h-1.4v-4.6H15z"/>';
    else if (kind === 'outlook') svg.innerHTML = '<path fill="#0364B8" d="M9 2h13v15H9z"/><path fill="#0078D4" d="M9 2h6v5H9zm6 5h7v5h-7z"/><path fill="#28A8EA" d="M15 2h7v5h-7zM9 7h6v5H9z"/><path fill="#50D9FF" d="M9 12h6v5H9z"/><path fill="#0078D4" d="m8 11 7 5 8-5v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1z"/><path fill="#1490DF" d="m8 21 7-5 8 5z"/><rect width="13" height="16" y="4" rx="1.5" fill="#0A5CAB"/><ellipse cx="6.5" cy="12" rx="3.1" ry="4.2" fill="#fff"/><ellipse cx="6.5" cy="12" rx="1.6" ry="2.6" fill="#0A5CAB"/>';
    else { svg.setAttribute('fill', 'none'); svg.setAttribute('stroke', 'currentColor'); svg.setAttribute('stroke-width', '1.7'); svg.setAttribute('stroke-linecap', 'round'); svg.setAttribute('stroke-linejoin', 'round'); svg.innerHTML = '<path d="M12 3v12m-4-4 4 4 4-4M5 16v4a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-4"/>'; }
    return svg;
  }

  function calendarActions(data, labels) {
    var root = document.createElement('div'); root.className = 'calendar-actions';
    var title = document.createElement('p'); title.textContent = labels.title; root.appendChild(title);
    var row = document.createElement('div'); row.className = 'calendar-actions-row'; root.appendChild(row);
    var start = new Date(data.start), end = new Date(data.end);
    if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime())) return root;
    var stamp = function (d) { return d.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, ''); };
    var details = [data.description, data.manageURL].filter(Boolean).join('\n\n');
    function link(label, kind, base, params) {
      var action = document.createElement(labels.disabled ? 'button' : 'a');
      if (labels.disabled) { action.type = 'button'; action.disabled = true; }
      else { action.href = base + new URLSearchParams(params); action.target = '_blank'; action.rel = 'noopener noreferrer'; }
      action.appendChild(calendarActionIcon(kind)); action.appendChild(document.createTextNode(label)); row.appendChild(action);
    }
    link('Google Calendar', 'google', 'https://calendar.google.com/calendar/render?', { action: 'TEMPLATE', text: data.title, dates: stamp(start) + '/' + stamp(end), details: details, location: data.location || '' });
    link('Outlook', 'outlook', 'https://outlook.office.com/calendar/0/action/compose?', { rru: 'addevent', subject: data.title, startdt: start.toISOString(), enddt: end.toISOString(), body: details, location: data.location || '' });
    var download = document.createElement('button'); download.type = 'button'; download.disabled = !!labels.disabled;
    download.appendChild(calendarActionIcon('download')); download.appendChild(document.createTextNode(labels.download));
    download.addEventListener('click', function () {
      var escape = function (v) { return String(v || '').replace(/\\/g, '\\\\').replace(/\r?\n/g, '\\n').replace(/,/g, '\\,').replace(/;/g, '\\;'); };
      // Fold at 75 UTF-8 octets, without splitting a Unicode code point.
      function fold(line) { var out = '', bytes = 0; for (var c of line) { var n = new TextEncoder().encode(c).length; if (bytes + n > 75) { out += '\r\n '; bytes = 1; } out += c; bytes += n; } return out; }
      var lines = ['BEGIN:VCALENDAR','VERSION:2.0','PRODID:-//Calnode//Booking//EN','BEGIN:VEVENT','UID:' + escape(data.uid || stamp(start) + '@calnode'),'DTSTAMP:' + stamp(new Date()),'DTSTART:' + stamp(start),'DTEND:' + stamp(end),'SUMMARY:' + escape(data.title),'DESCRIPTION:' + escape(details),'LOCATION:' + escape(data.location),'END:VEVENT','END:VCALENDAR'];
      var url = URL.createObjectURL(new Blob([lines.map(fold).join('\r\n') + '\r\n'], {type: 'text/calendar;charset=utf-8'}));
      var a = document.createElement('a'); a.href = url; a.download = 'booking.ics'; a.click(); setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
    }); row.appendChild(download); return root;
  }

  return {
    enhanceLocationSelect: enhanceLocationSelect,
    calendarActions: calendarActions,
    dateKeyFromISO: dateKeyFromISO,
    ymd: ymd,
    groupSlotsByDay: groupSlotsByDay,
    mergeDaySlots: mergeDaySlots,
    fmt: fmt,
    formatTime: formatTime,
    formatDay: formatDay,
    dowIndex: dowIndex,
    dowLabels: dowLabels,
    startOfMonth: startOfMonth,
    endOfMonth: endOfMonth,
    addMonths: addMonths,
    daysInMonth: daysInMonth
  };
});
