import { describe, test, expect, afterEach, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { today, getLocalTimeZone } from '@internationalized/date';
import Switch from '$lib/components/ui/switch/switch.svelte';
import Checkbox from '$lib/components/ui/checkbox/checkbox.svelte';
import Calendar from '$lib/components/ui/calendar/calendar.svelte';
import { timezoneOptions } from '$lib/timezones';
import EmailTemplateModal from '$lib/components/event-types/EmailTemplateModal.svelte';
import BookingPreviewCard from '$lib/components/event-types/BookingPreviewCard.svelte';
import QuickLinkPalette from '$lib/components/QuickLinkPalette.svelte';
import { api } from '$lib/api';
import SecuritySettings from './routes/settings/security/+page.svelte';

// Resolve a CSS value (e.g. `var(--primary)`) to the browser's computed color
// string, so comparisons are exact and color-space-agnostic (same engine, same
// serialization) rather than fragile literal matching.
function resolveColor(value: string): string {
	const probe = document.createElement('div');
	probe.style.backgroundColor = value;
	document.body.appendChild(probe);
	const c = getComputedStyle(probe).backgroundColor;
	probe.remove();
	return c;
}

afterEach(() => {
	cleanup();
	vi.restoreAllMocks();
});

describe('email template modal layout', () => {
	test.each(['light', 'dark'])('long HTML stays editable inside the %s modal', async (mode) => {
		const wasDark = document.documentElement.classList.contains('dark');
		document.documentElement.classList.toggle('dark', mode === 'dark');
		vi.spyOn(api, 'get').mockResolvedValue({ items: [] });
		vi.spyOn(api, 'post').mockResolvedValue({ subject: 'Preview', html: '<p>Preview</p>', default_html: '' });
		try {
			await render(EmailTemplateModal, { props: {
				open: true, slug: 'review', kind: 'confirmation', subject: '', noteBody: '',
				htmlBody: '<p>A long email template with many lines of source.</p>\n'.repeat(200),
				onApply: () => {},
			} });
			const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
			const editor = dialog.querySelector<HTMLTextAreaElement>('textarea')!;
			await expect.poll(() => editor.scrollHeight > editor.clientHeight).toBe(true);
			expect(editor.getBoundingClientRect().height).toBeLessThan(400);
			await expect.poll(() => dialog.getBoundingClientRect().top).toBeGreaterThanOrEqual(0);
			expect(dialog.getBoundingClientRect().height).toBeLessThanOrEqual(innerHeight);
			const apply = [...dialog.querySelectorAll('button')].find((b) => b.textContent?.includes('Apply to event'))!;
			apply.scrollIntoView({ block: 'nearest' });
			expect(apply.getBoundingClientRect().bottom).toBeLessThanOrEqual(innerHeight);
		} finally {
			document.documentElement.classList.toggle('dark', wasDark);
		}
	});
});

describe('booking preview sizing', () => {
	test.each([
		{ mode: 'form' as const, maxWidth: 720 },
		{ mode: 'confirmation' as const, maxWidth: 520 },
	])('keeps the $mode step compact on a wide editor', async ({ mode, maxWidth }) => {
		const submit = vi.spyOn(HTMLFormElement.prototype, 'requestSubmit').mockImplementation(() => {});
		const { container } = await render(BookingPreviewCard, { props: { slug: 'preview-event', draft: {}, mode } });
		container.style.width = '1100px';
		await expect.poll(() => submit.mock.calls.length).toBeGreaterThan(0);
		const frame = container.querySelector('iframe')!;
		const bounds = frame.getBoundingClientRect();
		expect(bounds.width).toBeLessThanOrEqual(maxWidth);
		expect(bounds.width).toBeGreaterThan(maxWidth - 4);
		expect(frame.parentElement!.scrollWidth).toBeLessThanOrEqual(maxWidth);
	});

	test.each([440, 612])('scales the public document inside a %ipx preview', async (width) => {
		vi.spyOn(HTMLFormElement.prototype, 'requestSubmit').mockImplementation(() => {});
		const { container } = await render(BookingPreviewCard, { props: { slug: 'preview-event', draft: { event_type: { name: 'Unsaved name', slot_interval_minutes: 45 } } } });
		container.style.width = `${width}px`;
		const form = container.querySelector('form')!;
		const payload = form.querySelector('input')!;
		await expect.poll(() => payload.value).toContain('"slot_interval_minutes":45');
		await expect.poll(() => vi.mocked(HTMLFormElement.prototype.requestSubmit).mock.calls.length).toBeGreaterThan(0);
		const frame = container.querySelector('iframe')!;
		expect(frame.isConnected).toBe(true);
		expect(form.action).toContain('/v1/event-types/preview-event/preview');
		expect(form.target).toBe(frame.name);
		expect(frame.style.width).toBe('860px');
		const bounds = container.getBoundingClientRect();
		expect(frame.getBoundingClientRect().width).toBeGreaterThan(400);
		await expect.poll(() => frame.getBoundingClientRect().right).toBeLessThanOrEqual(bounds.right);
		expect(container.querySelectorAll('button')).toHaveLength(0);
	});
});

test('quick-link actions fit a narrow dialog without hiding the event name', async () => {
	vi.spyOn(api, 'get').mockResolvedValue({ items: [{
		id: 'review', slug: 'intro-call', name: 'A longer event type name',
		is_active: true, archived: false, duration_minutes: 30,
	}] });
	await render(QuickLinkPalette, { props: { open: true } });
	const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
	dialog.style.width = '358px';
	await expect.poll(() => dialog.textContent).toContain('A longer event type name');
	const name = [...dialog.querySelectorAll('p')].find((p) => p.textContent === 'A longer event type name')!;
	expect(name.getBoundingClientRect().width).toBeGreaterThan(100);
	const bounds = dialog.getBoundingClientRect();
	for (const button of dialog.querySelectorAll('button')) {
		const rect = button.getBoundingClientRect();
		expect(rect.left).toBeGreaterThanOrEqual(bounds.left);
		expect(rect.right).toBeLessThanOrEqual(bounds.right);
	}
});

// These tests guard the silent bug class where a shadcn-svelte component renders
// with NO state styling because its custom Tailwind variant (data-checked,
// data-selected, …) was never declared in app.css. Logic/state stays correct, so
// only a real-browser computed-style assertion catches it. See TESTING.md.
describe('shadcn component state styling', () => {
	test('Switch: checked = --primary fill, unchecked = --input', async () => {
		const { container } = await render(Switch, { props: { checked: true } });
		const on = container.querySelector('[data-slot="switch"]')!;
		expect(getComputedStyle(on).backgroundColor).toBe(resolveColor('var(--primary)'));

		cleanup();
		const { container: c2 } = await render(Switch, { props: { checked: false } });
		const off = c2.querySelector('[data-slot="switch"]')!;
		const offBg = getComputedStyle(off).backgroundColor;
		expect(offBg).toBe(resolveColor('var(--input)'));
		expect(offBg).not.toBe(resolveColor('var(--primary)'));
	});

	test('Switch: thumb translates when checked', async () => {
		const { container } = await render(Switch, { props: { checked: true } });
		const thumb = container.querySelector('[data-slot="switch-thumb"]')!;
		const t = getComputedStyle(thumb).translate;
		// data-checked:translate-x-… must apply; unstyled would be "none"/"0px".
		expect(t).not.toBe('none');
		expect(t).not.toBe('0px');
		expect(t).not.toBe('');
	});

	test('Checkbox: checked = --primary fill', async () => {
		const { container } = await render(Checkbox, { props: { checked: true } });
		const cb = container.querySelector('[data-slot="checkbox"]')!;
		expect(getComputedStyle(cb).backgroundColor).toBe(resolveColor('var(--primary)'));
	});

	// NB: query the Day element ([data-bits-day]) — the styling classes live there,
	// while the parent Cell (<td>) also carries data-selected/data-today.
	test('Calendar: today cell is underlined (data-today)', async () => {
		const { container } = await render(Calendar, {});
		const todayCell = container.querySelector('[data-bits-day][data-today]')!;
		expect(todayCell).toBeTruthy();
		expect(getComputedStyle(todayCell).textDecorationLine).toContain('underline');
	});

	test('Calendar: selected day = --primary fill (data-selected)', async () => {
		const { container } = await render(Calendar, {
			props: { value: today(getLocalTimeZone()) }
		});
		const selected = container.querySelector('[data-bits-day][data-selected]')!;
		expect(selected).toBeTruthy();
		expect(getComputedStyle(selected).backgroundColor).toBe(resolveColor('var(--primary)'));
	});
});


describe('timezone options', () => {
	test('Kolkata is searchable by its name, abbreviation, country and offset', () => {
		const kolkata = timezoneOptions().find((item) => item.value === 'Asia/Kolkata');
		expect(kolkata).toBeTruthy();
		const searchText = `${kolkata!.label} ${kolkata!.keywords}`.toLowerCase();
		for (const term of ['ist', 'india', 'kolkata', 'gmt+5:30']) {
			expect(searchText).toContain(term);
		}
		expect(kolkata!.label).toContain('UTC+05:30');
	});
});


describe('security settings', () => {
 test.each(['light', 'dark'])('keeps %s policy controls compact and labelled', async (mode) => {
  const wasDark=document.documentElement.classList.contains('dark');
  document.documentElement.classList.toggle('dark',mode==='dark');
  vi.spyOn(api,'get').mockImplementation(async(path)=>path.endsWith('/operations')?{failed_jobs:[],calendar_errors:[],failed_confirmations:0,pending_jobs:0}:({policy:{google_enabled:false,microsoft_enabled:true,members_sso_only:true,admin_password_enabled:true,auto_provision:true,approved_domains:'company.com',required_group_id:'11111111-1111-4111-8111-111111111111',require_admin_mfa:false,require_member_mfa:false,session_hours:12,dynamic_link_days:7},google_configured:true,microsoft_configured:true}));
  try {
   const {container}=await render(SecuritySettings);
   await expect.poll(()=>container.querySelectorAll('[role="switch"]').length).toBe(7);
   const content=container.firstElementChild as HTMLElement;
   expect(content.getBoundingClientRect().width).toBeLessThanOrEqual(672);
   expect(content.scrollWidth).toBeLessThanOrEqual(content.clientWidth);
   const group=container.querySelector<HTMLInputElement>('#security-group')!;
   expect(group.value).toContain('11111111');
   expect(container.querySelector('label[for="members-sso"]')).not.toBeNull();
   const save=[...container.querySelectorAll('button')].find(b=>b.textContent?.includes('Review & save'))!;
   save.click();
   await expect.poll(()=>document.querySelector('[role="dialog"]')).not.toBeNull();
   const dialog=document.querySelector<HTMLElement>('[role="dialog"]')!;
   expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth);
   expect(dialog.querySelector('input[autocomplete="current-password"]')).not.toBeNull();
  } finally {document.documentElement.classList.toggle('dark',wasDark);}
 });
});
