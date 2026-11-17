<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { locale, t } from '$lib/i18n';
  import { theme } from '$lib/theme.svelte';

  let { slug, draft, mode = 'calendar', minWidth = 860 }: {
    slug: string;
    draft: Record<string, unknown>;
    mode?: 'calendar' | 'form' | 'confirmation';
    minWidth?: number;
  } = $props();

  const instanceId = $props.id();
  const frameName = `booking-preview-${instanceId}`;
  let frame = $state<HTMLIFrameElement>();
  let form: HTMLFormElement;
  let container: HTMLDivElement;
  let width = $state(860);
  let height = $state(520);
  let payload = $state('');
  let frameRevision = $state(0);
  let loading = $state(true);
  let error = $state('');
  let mounted = $state(false);
  let lastDate = '';
  let lastTimezone = '';
  let lastDuration = 0;
  let lastSlot = '';
  const stageWidth = $derived(mode === 'confirmation' ? 520 : mode === 'form' ? 720 : 860);
  const frameWidth = $derived(Math.max(1, Math.min(minWidth, stageWidth), width));
  const scale = $derived(width / frameWidth);
  const action = $derived(`/v1/event-types/${encodeURIComponent(slug)}/preview?lang=${$locale}`);

  function sendView() {
    frame?.contentWindow?.postMessage({ type: 'calnode-preview-view', mode, dark: theme.resolved === 'dark' }, window.location.origin);
  }

  // Draft changes reload the public document. Tab/theme switches use messages,
  // preserving the selected date while moving between calendar and form views.
  $effect(() => {
    if (!mounted) return;
    const settings = JSON.stringify(draft);
    const notice = $t('bookingPreview.readOnly');
    void action;
    const timer = setTimeout(async () => {
      payload = JSON.stringify({ ...JSON.parse(settings), notice, mode: untrack(() => mode), date: lastDate, timezone: lastTimezone, duration: lastDuration, slot: lastSlot });
      loading = true;
      error = '';
      // Start with a new initial frame so each POST replaces its about:blank
      // entry instead of adding draft edits to the browser's Back history.
      frameRevision += 1;
      await tick();
      form?.requestSubmit();
    }, 300);
    return () => clearTimeout(timer);
  });

  $effect(() => {
    void mode;
    void theme.resolved;
    void frameWidth;
    if (mounted) sendView();
  });

  function onFrameLoad() {
    const doc = frame?.contentDocument;
    if (!payload || !doc || doc.URL === 'about:blank') return;
    if (doc.contentType.includes('json')) {
      try { error = JSON.parse(doc.body.textContent || '{}').error || $t('bookingPreview.error'); }
      catch { error = $t('bookingPreview.error'); }
      loading = false;
    } else if (!doc.querySelector('.card')) {
      error = $t('bookingPreview.error');
      loading = false;
    }
  }

  onMount(() => {
    let resizeFrame = 0;
    const observer = new ResizeObserver(([entry]) => {
      cancelAnimationFrame(resizeFrame);
      resizeFrame = requestAnimationFrame(() => { width = entry.contentRect.width; });
    });
    observer.observe(container);
    const receive = (event: MessageEvent) => {
      if (event.source !== frame?.contentWindow || event.origin !== window.location.origin) return;
      if (event.data?.type === 'calnode-preview-ready') {
        loading = false;
        sendView();
      } else if (event.data?.type === 'calnode-preview-size' && Number.isFinite(event.data.height)) {
        height = Math.min(1000, Math.max(200, event.data.height));
      } else if (event.data?.type === 'calnode-preview-state') {
        if (/^\d{4}-\d{2}-\d{2}$/.test(event.data.date)) lastDate = event.data.date;
        if (typeof event.data.timezone === 'string') lastTimezone = event.data.timezone;
        if (Number.isInteger(event.data.duration) && event.data.duration > 0) lastDuration = event.data.duration;
        if (typeof event.data.slot === 'string') lastSlot = event.data.slot;
      }
    };
    window.addEventListener('message', receive);
    mounted = true;
    return () => {
      window.removeEventListener('message', receive);
      observer.disconnect();
      cancelAnimationFrame(resizeFrame);
    };
  });
</script>

<div class="mx-auto w-full overflow-hidden rounded-xl border bg-card shadow-sm" style:max-width={`${stageWidth}px`} bind:this={container}>
  <div class="flex items-center justify-between gap-2 border-b bg-muted/30 px-4 py-2">
    <span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{$t('bookingPreview.title')}</span>
    <span class="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
      {mode === 'confirmation' ? $t('bookingPreview.confirmation') : mode === 'form' ? $t('bookingPreview.form') : $t('bookingPreview.calendar')}
    </span>
  </div>
  <form bind:this={form} method="POST" {action} target={frameName} hidden>
    <input type="hidden" name="draft" value={payload} />
  </form>
  {#if error}
    <p role="alert" class="p-4 text-sm text-destructive">{error}</p>
  {/if}
  <div class="relative overflow-hidden" style:height={error ? '0px' : `${Math.ceil(height * scale)}px`} aria-busy={loading}>
    {#key frameRevision}
    <iframe
      bind:this={frame} name={frameName} title={$t('bookingPreview.frameTitle')}
      sandbox="allow-scripts allow-same-origin" onload={onFrameLoad} scrolling="auto"
      class="absolute left-0 top-0 border-0 bg-transparent"
      style:width={`${frameWidth}px`} style:height={`${height}px`}
      style:transform={`scale(${scale})`} style:transform-origin="top left"
    ></iframe>
    {/key}
    {#if loading}
      <div role="status" class="absolute inset-x-0 top-0 bg-card/90 px-4 py-2 text-xs text-muted-foreground">{$t('bookingPreview.loading')}</div>
    {/if}
  </div>
  <p class="border-t px-4 py-2 text-[10px] text-muted-foreground">{$t('bookingPreview.readOnly')}</p>
</div>
