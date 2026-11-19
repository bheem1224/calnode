// DOM controls shared by hosted booking/manage pages and the standalone widget.
(function (root, factory) {
    // Host sites may expose a CommonJS shim; do not overwrite their exports.
    if (typeof window === 'object' && root === window)
        root.CalnodeBookingUI = factory();
    else if (typeof module === 'object' && module.exports)
        module.exports = factory();
})(globalThis, function () {
    'use strict';
    // Enhance the native select without changing its values, validation or change events.
    function enhanceLocationSelect(select) {
        if (!select || select.hidden)
            return;
        var wrapper = document.createElement('div');
        wrapper.className = 'location-picker';
        select.before(wrapper);
        wrapper.appendChild(select);
        var trigger = document.createElement('button');
        trigger.type = 'button';
        trigger.className = 'location-trigger';
        trigger.id = select.id + '-trigger';
        trigger.setAttribute('aria-haspopup', 'listbox');
        trigger.setAttribute('aria-expanded', 'false');
        var value = document.createElement('span');
        value.className = 'location-value';
        var chevron = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        chevron.setAttribute('viewBox', '0 0 24 24');
        chevron.setAttribute('aria-hidden', 'true');
        chevron.innerHTML = '<path d="m6 9 6 6 6-6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>';
        trigger.append(value, chevron);
        var panel = document.createElement('div');
        panel.className = 'location-options';
        panel.id = select.id + '-options';
        panel.hidden = true;
        panel.setAttribute('role', 'listbox');
        trigger.setAttribute('aria-controls', panel.id);
        if (panel.showPopover)
            panel.setAttribute('popover', 'auto');
        var label = select.parentElement.parentElement.querySelector('label');
        if (label) {
            label.htmlFor = trigger.id;
            label.id = select.id + '-label';
            panel.setAttribute('aria-labelledby', label.id);
        }
        wrapper.append(trigger, panel);
        select.hidden = true;
        var items = [], outsideEvents;
        function sync() {
            value.textContent = select.options[select.selectedIndex]?.textContent || '';
            trigger.classList.toggle('is-placeholder', select.value === '');
            trigger.disabled = select.disabled;
            items.forEach(function (item) { item.setAttribute('aria-selected', String(item.dataset.value === select.value)); });
        }
        function close(focus) {
            if (panel.hidePopover && panel.matches(':popover-open'))
                panel.hidePopover();
            panel.hidden = true;
            trigger.setAttribute('aria-expanded', 'false');
            if (outsideEvents)
                outsideEvents.abort();
            if (focus)
                trigger.focus();
        }
        Array.from(select.options).forEach(function (option) {
            if (option.value === '' || option.disabled)
                return;
            var item = document.createElement('div');
            item.className = 'location-option';
            item.tabIndex = -1;
            item.setAttribute('role', 'option');
            item.dataset.value = option.value;
            item.textContent = option.textContent;
            item.addEventListener('click', function () { select.value = option.value; select.dispatchEvent(new Event('change', { bubbles: true })); close(true); });
            items.push(item);
            panel.appendChild(item);
        });
        function position() {
            var rect = trigger.getBoundingClientRect(), height = Math.min(240, items.length * 40 + 12);
            if (rect.bottom < 0 || rect.top > innerHeight) {
                close(false);
                return;
            }
            panel.style.width = Math.min(rect.width, innerWidth - 16) + 'px';
            panel.style.left = Math.max(8, Math.min(rect.left, innerWidth - rect.width - 8)) + 'px';
            panel.style.top = (rect.bottom + height + 8 <= innerHeight ? rect.bottom + 6 : Math.max(8, rect.top - height - 6)) + 'px';
        }
        function open(last) {
            sync();
            panel.hidden = false;
            trigger.setAttribute('aria-expanded', 'true');
            if (panel.showPopover) {
                position();
                panel.showPopover();
                outsideEvents = new AbortController();
                window.addEventListener('resize', function () { close(false); }, { signal: outsideEvents.signal });
                // Scrolling a long form must reposition its menu, not dismiss it during
                // the browser's deferred scroll-to-focus after clicking the trigger.
                window.addEventListener('scroll', function (event) {
                    if (!event.composedPath().includes(panel))
                        position();
                }, { capture: true, signal: outsideEvents.signal });
            }
            (items.find(function (item) { return item.dataset.value === select.value; }) || items[last ? items.length - 1 : 0])?.focus({ preventScroll: true });
        }
        trigger.addEventListener('click', function () {
            if (panel.hidden)
                open(false);
            else
                close(false);
        });
        trigger.addEventListener('keydown', function (event) {
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                event.preventDefault();
                open(event.key === 'ArrowUp');
            }
        });
        var typed = '', typedAt = 0;
        panel.addEventListener('keydown', function (event) {
            var index = items.indexOf(select.getRootNode().activeElement), next;
            if (event.key === 'Escape') {
                event.preventDefault();
                close(true);
                return;
            }
            if (event.key === 'Tab') {
                close(true);
                return;
            }
            if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                items[index]?.click();
                return;
            }
            if (event.key === 'ArrowDown')
                next = (index + 1) % items.length;
            if (event.key === 'ArrowUp')
                next = (index - 1 + items.length) % items.length;
            if (event.key === 'Home')
                next = 0;
            if (event.key === 'End')
                next = items.length - 1;
            if (next !== undefined) {
                event.preventDefault();
                items[next]?.focus();
            }
            else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
                typed = (Date.now() - typedAt < 700 ? typed : '') + event.key.toLocaleLowerCase();
                typedAt = Date.now();
                items.find(function (item) { return item.textContent.toLocaleLowerCase().startsWith(typed); })?.focus();
            }
        });
        panel.addEventListener('toggle', function (event) {
            if (event.newState === 'closed' && !panel.hidden)
                close(false);
        });
        wrapper.addEventListener('focusout', function (event) {
            if (!wrapper.contains(event.relatedTarget))
                close(false);
        });
        select.addEventListener('invalid', function (event) { event.preventDefault(); trigger.setAttribute('aria-invalid', 'true'); trigger.focus(); });
        select.addEventListener('change', function () { trigger.removeAttribute('aria-invalid'); sync(); });
        sync();
    }
    // Inline brand marks avoid third-party image requests on private booking pages.
    function calendarActionIcon(kind) {
        var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        svg.setAttribute('viewBox', '0 0 24 24');
        svg.setAttribute('aria-hidden', 'true');
        svg.setAttribute('focusable', 'false');
        svg.classList.add('calendar-action-icon');
        if (kind === 'google')
            svg.innerHTML = '<path fill="#fff" d="M5 5h14v14H5z"/><path fill="#4285F4" d="M5 5h14v4H5zM5 9h4v10H5z"/><path fill="#34A853" d="M9 19h10v-4h4v4l-4 4H9z"/><path fill="#FBBC04" d="M19 9h4v10h-4z"/><path fill="#EA4335" d="M19 19h4l-4 4z"/><path fill="#188038" d="M5 19h4v4H5z"/><path fill="#1967D2" d="M1 5a4 4 0 0 1 4-4h14v4H5v14H1z"/><path fill="#FBBC04" d="M19 1a4 4 0 0 1 4 4v4h-4z"/><path fill="#4285F4" d="M10 10h4v1.4l-1.5 1 1.5 1V16h-4v-1.4h2.5v-.8l-1.5-1v-.9l1.5-1H10zm5 0h2v6h-1.4v-4.6H15z"/>';
        else if (kind === 'outlook')
            svg.innerHTML = '<path fill="#0364B8" d="M9 2h13v15H9z"/><path fill="#0078D4" d="M9 2h6v5H9zm6 5h7v5h-7z"/><path fill="#28A8EA" d="M15 2h7v5h-7zM9 7h6v5H9z"/><path fill="#50D9FF" d="M9 12h6v5H9z"/><path fill="#0078D4" d="m8 11 7 5 8-5v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1z"/><path fill="#1490DF" d="m8 21 7-5 8 5z"/><rect width="13" height="16" y="4" rx="1.5" fill="#0A5CAB"/><ellipse cx="6.5" cy="12" rx="3.1" ry="4.2" fill="#fff"/><ellipse cx="6.5" cy="12" rx="1.6" ry="2.6" fill="#0A5CAB"/>';
        else {
            svg.setAttribute('fill', 'none');
            svg.setAttribute('stroke', 'currentColor');
            svg.setAttribute('stroke-width', '1.7');
            svg.setAttribute('stroke-linecap', 'round');
            svg.setAttribute('stroke-linejoin', 'round');
            svg.innerHTML = '<path d="M12 3v12m-4-4 4 4 4-4M5 16v4a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-4"/>';
        }
        return svg;
    }
    function calendarActions(data, labels) {
        var root = document.createElement('div');
        root.className = 'calendar-actions';
        var title = document.createElement('p');
        title.textContent = labels.title;
        root.appendChild(title);
        var row = document.createElement('div');
        row.className = 'calendar-actions-row';
        root.appendChild(row);
        var start = new Date(data.start), end = new Date(data.end);
        if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()))
            return root;
        var stamp = function (d) { return d.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, ''); };
        var details = [data.description, data.manageURL].filter(Boolean).join('\n\n');
        function link(label, kind, base, params) {
            var action = document.createElement(labels.disabled ? 'button' : 'a');
            if (labels.disabled) {
                action.type = 'button';
                action.disabled = true;
            }
            else {
                action.href = base + new URLSearchParams(params);
                action.target = '_blank';
                action.rel = 'noopener noreferrer';
            }
            action.appendChild(calendarActionIcon(kind));
            action.appendChild(document.createTextNode(label));
            row.appendChild(action);
        }
        link('Google Calendar', 'google', 'https://calendar.google.com/calendar/render?', { action: 'TEMPLATE', text: data.title, dates: stamp(start) + '/' + stamp(end), details: details, location: data.location || '' });
        link('Outlook', 'outlook', 'https://outlook.office.com/calendar/0/action/compose?', { rru: 'addevent', subject: data.title, startdt: start.toISOString(), enddt: end.toISOString(), body: details, location: data.location || '' });
        var download = document.createElement('button');
        download.type = 'button';
        download.disabled = !!labels.disabled;
        download.appendChild(calendarActionIcon('download'));
        download.appendChild(document.createTextNode(labels.download));
        download.addEventListener('click', function () {
            var escape = function (v) { return String(v || '').replace(/\\/g, '\\\\').replace(/\r?\n/g, '\\n').replace(/,/g, '\\,').replace(/;/g, '\\;'); };
            // Fold at 75 UTF-8 octets, without splitting a Unicode code point.
            function fold(line) {
                var out = '', bytes = 0;
                for (var c of line) {
                    var n = new TextEncoder().encode(c).length;
                    if (bytes + n > 75) {
                        out += '\r\n ';
                        bytes = 1;
                    }
                    out += c;
                    bytes += n;
                }
                return out;
            }
            var lines = ['BEGIN:VCALENDAR', 'VERSION:2.0', 'PRODID:-//Calnode//Booking//EN', 'BEGIN:VEVENT', 'UID:' + escape(data.uid || stamp(start) + '@calnode'), 'DTSTAMP:' + stamp(new Date()), 'DTSTART:' + stamp(start), 'DTEND:' + stamp(end), 'SUMMARY:' + escape(data.title), 'DESCRIPTION:' + escape(details), 'LOCATION:' + escape(data.location), 'END:VEVENT', 'END:VCALENDAR'];
            var url = URL.createObjectURL(new Blob([lines.map(fold).join('\r\n') + '\r\n'], { type: 'text/calendar;charset=utf-8' }));
            var a = document.createElement('a');
            a.href = url;
            a.download = 'booking.ics';
            a.click();
            setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
        });
        row.appendChild(download);
        return root;
    }
    function pending(booked) { return !!booked && (booked.pending_approval || booked.status === 'pending'); }
    function questionElement(q) {
        var field = document.createElement('div');
        field.className = 'field' + (q.hidden ? ' hidden' : '');
        field.dataset.qid = q.id;
        field.dataset.qtype = q.type;
        field.dataset.identifier = q.identifier || '';
        field.dataset.required = String(!!q.required);
        field.dataset.lockIfPrefilled = String(!!q.lock_if_prefilled);
        var label = document.createElement('label');
        label.textContent = q.label + (q.required ? ' *' : '');
        field.appendChild(label);
        var id = 'ans-' + q.id;
        if (['radio_group', 'checkbox_group', 'multiselect'].includes(q.type)) {
            var group = document.createElement('div');
            group.className = 'field-choice-group';
            group.setAttribute('role', 'group');
            group.setAttribute('aria-label', q.label);
            (q.options || []).forEach(function (option, index) {
                var row = document.createElement('label');
                row.className = 'field-checkbox';
                var input = document.createElement('input');
                input.type = q.type === 'radio_group' ? 'radio' : 'checkbox';
                input.name = id;
                input.value = option;
                input.id = id + '-' + index;
                row.append(input, document.createTextNode(option));
                group.append(row);
            });
            field.append(group);
        }
        else {
            var input;
            if (q.type === 'select') {
                input = document.createElement('select');
                input.append(new Option('', ''));
                (q.options || []).forEach(function (option) { input.append(new Option(option, option)); });
            }
            else if (q.type === 'text') {
                input = document.createElement('textarea');
                input.rows = 3;
            }
            else {
                input = document.createElement('input');
                input.type = { checkbox: 'checkbox', number: 'number', phone: 'tel', url: 'url' }[q.type] || 'text';
                if (q.type === 'number')
                    input.step = 'any';
            }
            input.id = id;
            input.name = id;
            input.required = !!q.required && !q.hidden;
            label.htmlFor = id;
            if (q.type === 'checkbox') {
                var row = document.createElement('div');
                row.className = 'field-checkbox';
                row.append(input, label);
                field.replaceChildren(row);
            }
            else
                field.append(input);
        }
        return field;
    }
    // Both surfaces honor the same question identifiers and prefill locks.
    function prefill(root, qp) {
        const fName = root.querySelector('#f-name');
        const fEmail = root.querySelector('#f-email');
        if (fName && qp.has('name'))
            fName.value = qp.get('name') || '';
        if (fEmail && qp.has('email'))
            fEmail.value = qp.get('email') || '';
        // Prefill intake questions from URL query parameters (?identifier=value).
        root.querySelectorAll('[data-qid][data-identifier]').forEach(function (el) {
            const key = el.dataset.identifier;
            if (!key || !qp.has(key))
                return;
            const pre = qp.get(key) ?? '';
            const qtype = el.dataset.qtype || 'text';
            const lock = el.dataset.lockIfPrefilled === 'true';
            if (qtype === 'radio_group') {
                el.querySelectorAll('input[type="radio"]').forEach(function (r) {
                    r.checked = r.value === pre;
                    if (lock)
                        r.disabled = true;
                });
                return;
            }
            if (qtype === 'checkbox_group' || qtype === 'multiselect') {
                const wanted = new Set(pre.split(',').map(function (s) { return s.trim(); }).filter(Boolean));
                el.querySelectorAll('input[type="checkbox"]').forEach(function (c) {
                    c.checked = wanted.has(c.value);
                    if (lock)
                        c.disabled = true;
                });
                return;
            }
            const input = el.querySelector('input, select, textarea');
            if (!input)
                return;
            if (input.type === 'checkbox') {
                input.checked = ['yes', 'true', '1', 'on', 'checked'].includes(pre.trim().toLowerCase());
                if (lock)
                    input.disabled = true;
            }
            else {
                input.value = pre;
                if (lock) {
                    if (input.tagName === 'SELECT')
                        input.disabled = true;
                    else
                        input.readOnly = true;
                }
            }
        });
    }
    function collectAnswers(root, requiredMessage) {
        var answers = [];
        root.querySelectorAll('[data-qid]').forEach(function (field) {
            var kind = field.dataset.qtype, value = '';
            if (['radio_group', 'checkbox_group', 'multiselect'].includes(kind))
                value = Array.from(field.querySelectorAll('input:checked')).map(function (input) { return input.value; }).join(',');
            else {
                var input = field.querySelector('input,select,textarea');
                value = kind === 'checkbox' ? (input.checked ? 'yes' : 'no') : input.value.trim();
                if (!field.classList.contains('hidden') && !input.checkValidity()) {
                    input.reportValidity();
                    throw new Error(requiredMessage);
                }
            }
            if (field.dataset.required === 'true' && (!value || (kind === 'checkbox' && value !== 'yes')))
                throw new Error(requiredMessage);
            answers.push({ question_id: field.dataset.qid, value: value });
        });
        return answers;
    }
    function guestControl(labels) {
        function node(tag, className, text) { var item = document.createElement(tag); if (className)
            item.className = className; if (text)
            item.textContent = text; return item; }
        function button(id, className, text) { var item = node('button', className, text); item.type = 'button'; item.id = id; return item; }
        var root = node('div', 'field');
        root.id = 'guests-control';
        var toggle = button('guests-toggle-btn', 'guests-toggle', labels.add_guests);
        toggle.setAttribute('aria-expanded', 'false');
        var box = node('div', 'guests-box');
        box.id = 'guests-box';
        box.hidden = true;
        var tabs = node('div', 'guests-tabs');
        tabs.setAttribute('role', 'tablist');
        tabs.setAttribute('aria-label', labels.add_guests);
        var single = button('guests-tab-single', 'guests-tab active', labels.guests_single), list = button('guests-tab-list', 'guests-tab', labels.guests_list);
        var singlePane = node('div'), listPane = node('div');
        singlePane.id = 'guests-single-pane';
        listPane.id = 'guests-list-pane';
        [single, list].forEach(function (tab, index) { tab.setAttribute('role', 'tab'); tab.setAttribute('aria-controls', index ? listPane.id : singlePane.id); });
        [singlePane, listPane].forEach(function (pane, index) { pane.setAttribute('role', 'tabpanel'); pane.setAttribute('aria-labelledby', index ? list.id : single.id); });
        var rows = node('div', 'guest-rows');
        rows.id = 'guest-rows';
        var add = button('add-guest-btn', 'guests-toggle', labels.guests_add);
        add.style.marginTop = '.35rem';
        var emails = node('textarea');
        emails.id = 'guests-list-input';
        emails.rows = 2;
        emails.placeholder = labels.guest_list_hint;
        emails.setAttribute('aria-label', labels.guest_list_hint);
        var mode = 'single';
        function addRow() {
            if (rows.children.length >= 10)
                return;
            var row = node('div', 'guest-row'), input = node('input'), remove = button('', 'guest-remove', '×');
            input.type = 'email';
            input.placeholder = labels.email_label;
            input.setAttribute('aria-label', labels.email_label);
            remove.setAttribute('aria-label', labels.guests_remove);
            remove.onclick = function () { row.remove(); add.disabled = false; if (!rows.children.length) {
                box.hidden = true;
                toggle.setAttribute('aria-expanded', 'false');
                toggle.focus();
            } };
            row.append(input, remove);
            rows.append(row);
            add.disabled = rows.children.length >= 10;
        }
        function select(next) { mode = next; singlePane.hidden = mode !== 'single'; listPane.hidden = mode !== 'list'; [single, list].forEach(function (tab, index) { var active = (mode === 'list') === !!index; tab.classList.toggle('active', active); tab.setAttribute('aria-selected', String(active)); tab.tabIndex = active ? 0 : -1; }); }
        single.onclick = function () { select('single'); };
        list.onclick = function () { select('list'); };
        tabs.onkeydown = function (event) { if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
            event.preventDefault();
            select(mode === 'single' ? 'list' : 'single');
            (mode === 'single' ? single : list).focus();
        } };
        toggle.onclick = function () { box.hidden = !box.hidden; toggle.setAttribute('aria-expanded', String(!box.hidden)); if (!box.hidden && !rows.children.length)
            addRow(); };
        add.onclick = addRow;
        tabs.append(single, list);
        singlePane.append(rows, add);
        listPane.append(emails);
        box.append(tabs, singlePane, listPane);
        root.append(toggle, box);
        select('single');
        return { element: root, values: function () {
                if (box.hidden)
                    return [];
                var values = mode === 'list' ? emails.value.split(/[\s,;]+/).filter(Boolean) : Array.from(rows.querySelectorAll('input')).map(function (input) { return input.value.trim(); }).filter(Boolean);
                if (values.length > 10)
                    throw new Error(labels.guest_list_hint);
                return values;
            } };
    }
    function mountEmailVerification(root, options) {
        var state = options.state || { token: '', email: '' }, labels = options.labels;
        var find = function (id) { return root.querySelector('#' + id); };
        var email = find('f-email'), send = find('send-code-btn'), check = find('check-code-btn'), code = find('verify-code-input'), status = find('verify-status');
        if (!send)
            return state;
        var prefix = options.token ? '/v1/t/' + encodeURIComponent(options.token) : '/v1/book/' + encodeURIComponent(options.slug);
        function message(key) { status.textContent = labels[key] || key; }
        async function request(action, body) {
            var response = await fetch((options.base || '') + prefix + '/verify-email/' + action, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
            var data = await response.json();
            if (!response.ok)
                throw new Error(data.error || labels.verification_failed);
            return data;
        }
        email.addEventListener('input', function () {
            if (email.value.trim().toLowerCase() !== state.email) {
                state.token = '';
                status.textContent = '';
            }
        });
        send.addEventListener('click', async function () {
            if (!email.value.trim() || !email.checkValidity()) {
                email.reportValidity();
                return;
            }
            send.disabled = true;
            message('verification_sending');
            try {
                await request('send', { email: email.value.trim() });
                code.hidden = false;
                check.hidden = false;
                message('verification_sent');
                code.focus();
            }
            catch (error) {
                status.textContent = error.message || labels.verification_failed;
            }
            finally {
                send.disabled = false;
            }
        });
        check.addEventListener('click', async function () {
            check.disabled = true;
            try {
                var data = await request('check', { email: email.value.trim(), code: code.value.trim() });
                if (!data.verification_token)
                    throw new Error(labels.verification_failed);
                state.token = data.verification_token;
                state.email = email.value.trim().toLowerCase();
                code.hidden = true;
                check.hidden = true;
                message('verification_verified');
            }
            catch (error) {
                status.textContent = error.message || labels.verification_failed;
            }
            finally {
                check.disabled = false;
            }
        });
        return state;
    }
    return { enhanceLocationSelect: enhanceLocationSelect, calendarActions: calendarActions, pending: pending, questionElement: questionElement, collectAnswers: collectAnswers, mountEmailVerification: mountEmailVerification, guestControl: guestControl, prefill: prefill };
});
