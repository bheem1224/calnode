<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type EmailTemplate } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import * as Dialog from '$lib/components/ui/dialog';
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
			mode = htmlBody.trim() ? 'html' : 'note';
			draftSubject = subject;
			draftNote = noteBody;
			draftHtml = htmlBody;
			previewError = '';
			saveLibOpen = false;
			loadTemplates();
			runPreview();
		}
	});

	async function loadTemplates() {
		try {
			const res = await api.get<{ items: EmailTemplate[] }>(`/v1/email-templates?kind=${kind}`);
			templates = res.items ?? [];
		} catch {
			templates = [];
		}
	}

	async function runPreview() {
		if (!slug) return;
		loadingPreview = true;
		previewError = '';
		try {
			const body = mode === 'html' ? draftHtml : draftNote;
			const res = await api.post<{ subject: string; html: string; default_html: string }>(
				`/v1/event-types/${slug}/email-preview`,
				{ type: kind, mode, subject: draftSubject, body }
			);
			previewSubject = res.subject;
			previewHtml = res.html;
			defaultHtml = res.default_html;
		} catch (e: any) {
			previewError = e.message || 'Invalid template';
		} finally {
			loadingPreview = false;
		}
	}

	function schedulePreview() {
		if (debounceTimer) clearTimeout(debounceTimer);
		debounceTimer = setTimeout(runPreview, 300);
	}

	async function switchMode(next: 'note' | 'html') {
		if (mode === next) return;
		mode = next;
		if (next === 'html' && !draftHtml.trim()) {
			if (!defaultHtml) {
				await runPreview();
			}
			if (defaultHtml) {
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
		await runPreview();
		if (previewError) {
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
	<Dialog.Content class="max-w-5xl">
		<Dialog.Header>
			<div class="flex flex-wrap items-center justify-between gap-3 pr-6">
				<div>
					<Dialog.Title>{KIND_LABELS[kind]} Builder</Dialog.Title>
					<Dialog.Description>
						Edit in Styled Note mode or customize full HTML with live preview and reusable templates.
					</Dialog.Description>
				</div>
				<div class="flex items-center gap-2">
					<select
						aria-label="Load saved template"
						onchange={(e) => {
							const v = (e.currentTarget as HTMLSelectElement).value;
							applySavedTemplate(v);
							(e.currentTarget as HTMLSelectElement).value = '';
						}}
						class="h-8 rounded-md border border-input bg-background px-2.5 text-xs"
					>
						<option value="">Load template...</option>
						<option value="__default__">Reset to Calnode default</option>
						{#each templates as tpl (tpl.id)}
							<option value={tpl.id}>{tpl.name}{tpl.is_default ? ' (Default)' : ''}</option>
						{/each}
					</select>
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
					<input type="checkbox" bind:checked={libDefault} class="h-3.5 w-3.5 rounded accent-primary" />
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
				<div class="inline-flex rounded-md border bg-muted/30 p-0.5 text-xs">
					<button
						type="button"
						onclick={() => switchMode('note')}
						class="rounded px-3 py-1 font-medium transition-colors {mode === 'note' ? 'bg-background shadow-xs text-foreground' : 'text-muted-foreground hover:text-foreground'}"
					>
						Styled Note
					</button>
					<button
						type="button"
						onclick={() => switchMode('html')}
						class="rounded px-3 py-1 font-medium transition-colors {mode === 'html' ? 'bg-background shadow-xs text-foreground' : 'text-muted-foreground hover:text-foreground'}"
					>
						Custom HTML
					</button>
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
							<button
								type="button"
								onclick={() => insertVar(v, 'subject')}
								class="rounded border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground hover:bg-accent hover:text-foreground"
							>
								+{v}
							</button>
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
							class="font-mono text-xs"
						/>
					{:else}
						<Textarea
							id="tpl-body"
							bind:value={draftNote}
							oninput={schedulePreview}
							rows={8}
							class="text-xs"
						/>
					{/if}
					<div class="flex flex-wrap gap-1 pt-0.5">
						{#each VARS as v}
							<button
								type="button"
								onclick={() => insertVar(v, 'body')}
								class="rounded border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground hover:bg-accent hover:text-foreground"
							>
								+{v}
							</button>
						{/each}
					</div>
				</div>

				{#if previewError}
					<p class="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{previewError}</p>
				{/if}
			</div>

			<!-- Right: Live Preview -->
			<div class="flex flex-col space-y-2">
				<div class="flex items-center justify-between text-xs">
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
