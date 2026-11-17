<script lang="ts">
	import { untrack } from 'svelte';
	import { api, type EmailTemplate } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Select from '$lib/components/ui/select';
	import { toast } from 'svelte-sonner';

	type Kind = 'confirmation' | 'cancellation' | 'reschedule' | 'reminder';

	let {
		open = $bindable(false),
		slug,
		kind,
		subject = '',
		noteBody = '',
		htmlBody = '',
		onApply,
	}: {
		open: boolean;
		slug: string;
		kind: Kind;
		subject: string;
		noteBody: string;
		htmlBody: string;
		onApply: (res: { subject: string; noteBody: string; htmlBody: string }) => void;
	} = $props();

	let mode = $state<'note' | 'html'>('note');
	let draftSubject = $state('');
	let draftNote = $state('');
	let draftHtml = $state('');
	let defaultHtml = $state('');
	let previewSubject = $state('');
	let previewHtml = $state('');
	let previewError = $state('');
	let loadingPreview = $state(false);
	let sendingTest = $state(false);

	let templates = $state<EmailTemplate[]>([]);
	let saveLibOpen = $state(false);
	let libName = $state('');
	let libDefault = $state(false);
	let savingLib = $state(false);
	let debounceTimer: ReturnType<typeof setTimeout> | null = null;
	let previewVersion = 0;

	const VARS = [
		'{Event}',
		'{Duration}',
		'{Host}',
		'{HostFirst}',
		'{Scheduler}',
		'{SchedulerFirst}',
		'{Location}',
		'{StartTime}',
		'{ManageURL}',
	];

	const KIND_LABELS: Record<Kind, string> = {
		confirmation: 'Confirmation Email',
		cancellation: 'Cancellation Email',
		reschedule: 'Reschedule Email',
		reminder: 'Reminder Email',
	};

	$effect(() => {
		if (open) {
			untrack(() => {
				mode = htmlBody.trim() ? 'html' : 'note';
				draftSubject = subject;
				draftNote = noteBody;
				draftHtml = htmlBody;
				defaultHtml = '';
				previewHtml = '';
				previewError = '';
				saveLibOpen = false;
				void loadTemplates();
				void runPreview();
			});
		}
		return () => { if (debounceTimer) clearTimeout(debounceTimer); previewVersion++; };
	});

	async function loadTemplates() {
		try {
			const res = await api.get<{ items: EmailTemplate[] }>(`/v1/email-templates?kind=${kind}`);
			templates = res.items ?? [];
		} catch {
			templates = [];
		}
	}

	async function runPreview(): Promise<boolean> {
		if (!slug) return false;
		const version = ++previewVersion;
		loadingPreview = true;
		previewError = '';
		try {
			const body = mode === 'html' ? draftHtml : draftNote;
			const res = await api.post<{ subject: string; html: string; default_html: string }>(
				`/v1/event-types/${slug}/email-preview`,
				{ type: kind, mode, subject: draftSubject, body }
			);
			if (version !== previewVersion) return false;
			previewSubject = res.subject;
			previewHtml = res.html;
			defaultHtml = res.default_html;
			return true;
		} catch (e: any) {
			if (version === previewVersion) previewError = e.message || 'Invalid template';
			return false;
		} finally {
			if (version === previewVersion) loadingPreview = false;
		}
	}

	function schedulePreview() {
		if (debounceTimer) clearTimeout(debounceTimer);
		previewVersion++;
		debounceTimer = setTimeout(() => void runPreview(), 300);
	}

	async function switchMode(next: 'note' | 'html') {
		if (mode === next) return;
		mode = next;
		if (next === 'html' && !draftHtml.trim()) {
			if (!defaultHtml) {
				await runPreview();
			}
			if (mode === 'html' && !draftHtml.trim() && defaultHtml) {
				draftHtml = defaultHtml;
			}
		}
		schedulePreview();
	}

	function insertVar(token: string, target: 'subject' | 'body') {
		if (target === 'subject') {
			draftSubject = (draftSubject ? draftSubject + ' ' : '') + token;
		} else if (mode === 'html') {
			draftHtml = (draftHtml ? draftHtml + ' ' : '') + token;
		} else {
			draftNote = (draftNote ? draftNote + ' ' : '') + token;
		}
		schedulePreview();
	}

	function applySavedTemplate(tplId: string) {
		if (!tplId) return;
		if (tplId === '__default__') {
			mode = 'note';
			draftSubject = '';
			draftNote = '';
			draftHtml = '';
			schedulePreview();
			return;
		}
		const tpl = templates.find((t) => t.id === tplId);
		if (!tpl) return;
		mode = tpl.mode;
		draftSubject = tpl.subject;
		if (tpl.mode === 'html') {
			draftHtml = tpl.body;
		} else {
			draftNote = tpl.body;
			draftHtml = '';
		}
		schedulePreview();
	}

	async function saveToLibrary() {
		if (!libName.trim()) {
			toast.error('Enter a template name');
			return;
		}
		savingLib = true;
		try {
			await api.post('/v1/email-templates', {
				name: libName.trim(),
				kind,
				is_default: libDefault,
				mode,
				subject: draftSubject.trim(),
				body: mode === 'html' ? draftHtml : draftNote,
			});
			toast.success('Saved to your email templates');
			libName = '';
			libDefault = false;
			saveLibOpen = false;
			await loadTemplates();
		} catch (e: any) {
			toast.error(e.message || 'Failed to save template');
		} finally {
			savingLib = false;
		}
	}

	async function sendTest() {
		sendingTest = true;
		try {
			const res = await api.post<{ to: string }>(`/v1/event-types/${slug}/test-email`, { type: kind });
			toast.success(`Test email sent to ${res.to}`);
		} catch (e: any) {
			toast.error(e.message || 'Failed to send test email');
		} finally {
			sendingTest = false;
		}
	}

	async function handleApply() {
		if (debounceTimer) clearTimeout(debounceTimer);
		if (!(await runPreview())) {
			toast.error(previewError);
			return;
		}
		onApply({
			subject: draftSubject.trim(),
			noteBody: draftNote,
			htmlBody: mode === 'html' ? draftHtml.trim() : '',
		});
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-5xl overflow-y-auto">
		<Dialog.Header>
			<div class="flex flex-wrap items-center justify-between gap-3 pr-6">
				<div>
					<Dialog.Title>{KIND_LABELS[kind]} Builder</Dialog.Title>
					<Dialog.Description>
						Edit in Styled Note mode or customize full HTML with live preview and reusable templates.
					</Dialog.Description>
				</div>
				<div class="flex flex-wrap items-center gap-2">
					<Select.Root type="single" value="" onValueChange={(v) => { if (v) applySavedTemplate(v); }}>
						<Select.Trigger aria-label="Load saved template" class="h-8 w-48 justify-between text-xs">Load template...</Select.Trigger>
						<Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
							<Select.Item value="__default__" label="Reset to Calnode default">Reset to Calnode default</Select.Item>
							{#each templates as tpl (tpl.id)}
								<Select.Item value={tpl.id} label={tpl.name}>{tpl.name}{tpl.is_default ? ' (Default)' : ''}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
					<Button type="button" size="sm" variant="outline" class="h-8 text-xs" onclick={() => (saveLibOpen = !saveLibOpen)}>
						Save to library
					</Button>
				</div>
			</div>
		</Dialog.Header>

		{#if saveLibOpen}
			<div class="flex flex-wrap items-center gap-3 rounded-lg border bg-muted/30 p-3">
				<Input bind:value={libName} placeholder="Template name (e.g. Founder Intro)" class="h-8 w-56 text-xs" />
				<label class="flex cursor-pointer items-center gap-1.5 text-xs">
					<Checkbox bind:checked={libDefault} />
					Set as default for new event types
				</label>
				<Button type="button" size="sm" class="h-8 text-xs" disabled={savingLib} onclick={saveToLibrary}>
					{savingLib ? 'Saving...' : 'Save template'}
				</Button>
			</div>
		{/if}

		<div class="grid gap-5 lg:grid-cols-2">
			<!-- Left: Editor -->
			<div class="space-y-4">
				<div role="tablist" aria-label="Email editor mode" class="inline-flex rounded-md border bg-muted/30 p-0.5 text-xs">
					<Button
						type="button" variant="ghost" role="tab" aria-selected={mode === 'note'}
						onclick={() => switchMode('note')}
						class="rounded px-3 py-1 font-medium transition-colors {mode === 'note' ? 'bg-background shadow-xs text-foreground' : 'text-muted-foreground hover:text-foreground'}"
					>
						Styled Note
					</Button>
					<Button
						type="button" variant="ghost" role="tab" aria-selected={mode === 'html'}
						onclick={() => switchMode('html')}
						class="rounded px-3 py-1 font-medium transition-colors {mode === 'html' ? 'bg-background shadow-xs text-foreground' : 'text-muted-foreground hover:text-foreground'}"
					>
						Custom HTML
					</Button>
				</div>

				<div class="space-y-1.5">
					<Label for="tpl-subject" class="text-xs">Email subject</Label>
					<Input
						id="tpl-subject"
						bind:value={draftSubject}
						oninput={schedulePreview}
						placeholder="Leave blank for default subject"
						class="text-xs"
					/>
					<div class="flex flex-wrap gap-1 pt-0.5">
						{#each VARS.slice(0, 6) as v}
							<Button
								type="button" variant="ghost"
								onclick={() => insertVar(v, 'subject')}
								class="h-auto rounded border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground hover:bg-accent hover:text-foreground"
							>
								+{v}
							</Button>
						{/each}
					</div>
				</div>

				<div class="space-y-1.5">
					<Label for="tpl-body" class="text-xs">
						{mode === 'html' ? 'HTML body (sanitized before saving)' : 'Custom note (shown inside Calnode card)'}
					</Label>
					{#if mode === 'html'}
						<Textarea
							id="tpl-body"
							bind:value={draftHtml}
							oninput={schedulePreview}
							rows={12}
							class="h-72 max-h-96 resize-y field-sizing-fixed overflow-y-auto font-mono text-xs"
						/>
					{:else}
						<Textarea
							id="tpl-body"
							bind:value={draftNote}
							oninput={schedulePreview}
							rows={8}
							class="h-48 max-h-96 resize-y field-sizing-fixed overflow-y-auto text-xs"
						/>
					{/if}
					<div class="flex flex-wrap gap-1 pt-0.5">
						{#each VARS as v}
							<Button
								type="button" variant="ghost"
								onclick={() => insertVar(v, 'body')}
								class="h-auto rounded border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground hover:bg-accent hover:text-foreground"
							>
								+{v}
							</Button>
						{/each}
					</div>
				</div>

				{#if previewError}
					<p class="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{previewError}</p>
				{/if}
			</div>

			<!-- Right: Live Preview -->
			<div class="flex flex-col space-y-2">
				<div class="flex flex-wrap items-center justify-between gap-2 text-xs">
					<span class="font-semibold text-muted-foreground">Live Email Preview</span>
					{#if previewSubject}
						<span class="max-w-[260px] truncate text-[11px] text-muted-foreground">Subject: <strong class="text-foreground">{previewSubject}</strong></span>
					{/if}
				</div>
				<div class="flex-1 overflow-hidden rounded-lg border bg-white">
					{#if loadingPreview && !previewHtml}
						<div class="flex h-80 items-center justify-center text-xs text-zinc-500">Rendering preview...</div>
					{:else}
						<iframe
							title="Email template preview"
							sandbox=""
							srcdoc={previewHtml}
							class="h-[380px] w-full border-0 bg-white"
						></iframe>
					{/if}
				</div>
			</div>
		</div>

		<Dialog.Footer class="flex flex-wrap items-center justify-between gap-2 sm:justify-between">
			<Button type="button" variant="outline" size="sm" disabled={sendingTest} onclick={sendTest}>
				{sendingTest ? 'Sending...' : 'Send test email (saved state)'}
			</Button>
			<div class="flex items-center gap-2">
				<Button type="button" variant="ghost" onclick={() => (open = false)}>Cancel</Button>
				<Button type="button" onclick={handleApply}>Apply to event</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
