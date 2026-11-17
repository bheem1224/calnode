<script lang="ts">
  import type { Question } from "$lib/api";

  let {
    name = "New Meeting",
    description = "",
    duration = 30,
    locationType = "link",
    locationValue = "",
    hostName = "Host",
    hostAvatar = "",
    mode = "calendar",
    allowPhoneCall = false,
    allowMultipleDurations = false,
    durationOptions = [],
    calendarEventTitle = "",
    redirectUrl = "",
    questions = [],
    bookingFormConfig = "",
  }: {
    name?: string;
    description?: string;
    duration?: number;
    locationType?: string;
    locationValue?: string;
    hostName?: string;
    hostAvatar?: string;
    mode?: "calendar" | "form" | "confirmation";
    allowPhoneCall?: boolean;
    allowMultipleDurations?: boolean;
    durationOptions?: number[];
    calendarEventTitle?: string;
    redirectUrl?: string;
    questions?: Question[];
    bookingFormConfig?: string;
  } = $props();

  let selectedDay = $state<number | null>(18);
  let selectedSlot = $state("10:00 AM");
  let previewDuration = $state<number | null>(null);
  let previewGuestsOpen = $state(false);
  let previewGuestsMode = $state<"single" | "list">("single");
  let previewGuestRows = $state<string[]>([""]);

  type PreviewFormItem =
    | { kind: "name" | "email" | "phone" | "notes" | "guests"; hidden: boolean; required: boolean }
    | { kind: "question"; question: Question };

  const previewFormItems = $derived.by((): PreviewFormItem[] => {
    const qById = new Map(questions.map((q) => [q.id, q]));
    let parsed: { key: string; hidden?: boolean; required?: boolean }[] = [];
    if (bookingFormConfig && bookingFormConfig.trim() !== "") {
      try {
        const raw = JSON.parse(bookingFormConfig);
        if (Array.isArray(raw)) parsed = raw;
      } catch {
        parsed = [];
      }
    }
    const seen = new Set<string>();
    const out: PreviewFormItem[] = [];
    const addSys = (
      key: "name" | "email" | "phone" | "notes" | "guests",
      hidden: boolean,
      required: boolean,
    ) => {
      if (seen.has(key)) return;
      seen.add(key);
      if (key === "name" || key === "email") {
        hidden = false;
        required = true;
      }
      if (key === "phone" && allowPhoneCall) {
        hidden = false;
      }
      out.push({ kind: key, hidden, required });
    };
    for (const c of parsed) {
      if (
        c.key === "name" ||
        c.key === "email" ||
        c.key === "phone" ||
        c.key === "notes" ||
        c.key === "guests"
      ) {
        addSys(c.key, !!c.hidden, !!c.required);
      } else if (c.key?.startsWith("q:")) {
        const q = qById.get(c.key.slice(2));
        if (q && !seen.has(c.key)) {
          seen.add(c.key);
          out.push({ kind: "question", question: q });
        }
      }
    }
    addSys("name", false, true);
    addSys("email", false, true);
    addSys("phone", false, allowPhoneCall);
    addSys("notes", false, false);
    addSys("guests", false, false);
    for (const q of questions) {
      const k = `q:${q.id}`;
      if (!seen.has(k)) {
        seen.add(k);
        out.push({ kind: "question", question: q });
      }
    }
    return out;
  });
  const activeDuration = $derived(
    allowMultipleDurations && durationOptions.length > 0
      ? (previewDuration && durationOptions.includes(previewDuration) ? previewDuration : durationOptions[0])
      : duration || 30
  );

  const days = Array.from({ length: 30 }, (_, i) => i + 1);
  const weekdays = ["Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"];
  const slots = ["9:00 AM", "9:30 AM", "10:00 AM", "11:00 AM", "2:00 PM", "3:30 PM"];

  const locationLabel = $derived.by(() => {
    switch (locationType) {
      case "google_meet":
        return "Google Meet";
      case "teams":
        return "Microsoft Teams";
      case "zoom":
        return "Zoom";
      case "livekit":
        return "Calnode Video";
      case "phone":
        return locationValue || "Phone call";
      case "in_person":
        return locationValue || "In person";
      default:
        return locationValue || "Web conferencing";
    }
  });

  const resolvedTitle = $derived(
    (calendarEventTitle?.trim() || "{Event} between {Host} and {Scheduler}")
      .replaceAll("{Event}", name || "30-minute call")
      .replaceAll("{Duration}", `${activeDuration}m`)
      .replaceAll("{Host}", hostName || "Host")
      .replaceAll("{HostFirst}", (hostName || "Host").split(" ")[0])
      .replaceAll("{Scheduler}", "Jane Doe")
      .replaceAll("{SchedulerFirst}", "Jane")
      .replaceAll("{Location}", locationLabel)
  );
</script>

<div class="w-full overflow-hidden rounded-xl border bg-card shadow-sm">
  <div class="flex items-center justify-between border-b bg-muted/30 px-4 py-2">
    <span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Live Preview</span>
    <span class="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
      {mode === "confirmation" ? "Confirmation page" : mode === "form" ? "Booking form" : "Booking page"}
    </span>
  </div>

  {#if mode === "confirmation"}
    <div class="p-6">
      <div class="mx-auto max-w-md space-y-4 rounded-xl border bg-background/60 p-5">
        <div class="flex flex-col items-center space-y-1.5 text-center">
          <div class="flex h-10 w-10 items-center justify-center rounded-full bg-emerald-500/15 text-lg font-bold text-emerald-500">
            ✓
          </div>
          <h3 class="text-base font-semibold text-foreground">This meeting is scheduled</h3>
          <p class="text-xs text-muted-foreground">
            A calendar invitation has been sent to your email address.
          </p>
        </div>
        <div class="space-y-2.5 border-t pt-3.5 text-xs">
          <div class="flex justify-between gap-3">
            <span class="text-muted-foreground">What</span>
            <span class="text-right font-medium text-foreground">{resolvedTitle}</span>
          </div>
          <div class="flex justify-between gap-3">
            <span class="text-muted-foreground">When</span>
            <span class="font-medium text-foreground">Wed, Nov {selectedDay ?? 18} · {selectedSlot} ({activeDuration}m)</span>
          </div>
          <div class="flex justify-between gap-3">
            <span class="text-muted-foreground">Who</span>
            <span class="font-medium text-foreground">{hostName}, Jane Doe</span>
          </div>
          <div class="flex justify-between gap-3">
            <span class="text-muted-foreground">Where</span>
            <span class="font-medium text-foreground">{locationLabel}</span>
          </div>
          {#if redirectUrl?.trim()}
            <div class="truncate rounded bg-muted px-2.5 py-1.5 text-[11px] text-muted-foreground">
              Redirects to: <span class="font-mono text-foreground">{redirectUrl}</span>
            </div>
          {/if}
        </div>
        <div class="flex justify-center gap-2 border-t pt-3 text-[11px] text-muted-foreground">
          <span class="rounded-md border px-2.5 py-1">Reschedule</span>
          <span class="rounded-md border px-2.5 py-1">Cancel</span>
        </div>
      </div>
    </div>
  {:else}
    <!-- Mirrors /book/[slug] 2-3 column booking card -->
    <div class="w-full max-w-[860px] grid grid-cols-1 md:grid-cols-[210px_1fr]">
      <!-- Left info panel -->
      <div class="border-b p-5 md:border-b-0 md:border-r">
        <div class="mb-3">
          {#if hostAvatar}
            <img src={hostAvatar} alt={hostName} class="h-10 w-10 rounded-full object-cover" />
          {:else}
            <div class="flex h-10 w-10 items-center justify-center rounded-full bg-primary text-sm font-semibold text-primary-foreground">
              {(hostName || "H").slice(0, 1).toUpperCase()}
            </div>
          {/if}
        </div>
        <p class="truncate text-xs font-medium text-muted-foreground">{hostName}</p>
        <h3 class="mt-0.5 text-base font-semibold tracking-tight">{name || "Untitled Event"}</h3>
        {#if description}
          <p class="mt-2 line-clamp-3 text-xs leading-relaxed text-muted-foreground">{description}</p>
        {/if}

        <div class="mt-4 space-y-2 text-xs text-muted-foreground">
          {#if mode === "form"}
            <div class="space-y-1.5 border-b pb-2.5 text-foreground">
              <div class="flex items-start gap-2">
                <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mt-0.5 shrink-0 text-muted-foreground"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
                <span class="font-medium">Wed, Nov {selectedDay ?? 18}, 2026<br/>{selectedSlot}</span>
              </div>
              <div class="flex items-center gap-2">
                <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
                <span>America/New_York</span>
              </div>
            </div>
          {/if}
          <div class="flex items-center gap-2">
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
            {#if allowMultipleDurations && durationOptions.length > 1}
              <div class="inline-flex items-center gap-0.5 rounded-lg border p-0.5">
                {#each durationOptions as d}
                  <button
                    type="button"
                    onclick={() => (previewDuration = d)}
                    class="rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors {activeDuration === d
                      ? 'bg-muted text-foreground font-semibold'
                      : 'text-muted-foreground hover:text-foreground'}"
                  >{d}m</button>
                {/each}
              </div>
            {:else}
              <span>{activeDuration} min</span>
            {/if}
          </div>
          <div class="flex items-center gap-2">
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 10c0 6-8 12-8 12s-8-6-8-12a8 8 0 0 1 16 0Z"/><circle cx="12" cy="10" r="3"/></svg>
            <span class="truncate">{locationLabel}</span>
          </div>
        </div>
      </div>

      <!-- Right pane: Calendar+Slots OR Booking Form -->
      {#if mode === "form"}
        <div class="space-y-3 p-5">
          <div class="space-y-2.5 text-xs">
            {#each previewFormItems as item}
              {#if item.kind === "name"}
                <div>
                  <div class="mb-1 font-medium text-foreground">Your name <span class="text-destructive">*</span></div>
                  <div class="rounded-md border bg-background px-2.5 py-1.5 text-muted-foreground">Jane Doe</div>
                </div>
              {:else if item.kind === "email"}
                <div>
                  <div class="mb-1 font-medium text-foreground">Email address <span class="text-destructive">*</span></div>
                  <div class="rounded-md border bg-background px-2.5 py-1.5 text-muted-foreground">jane@example.com</div>
                </div>
              {:else if item.kind === "phone" && !item.hidden}
                <div>
                  <div class="mb-1 font-medium text-foreground">Phone number <span class="font-normal text-muted-foreground">{allowPhoneCall ? "" : "(optional)"}</span></div>
                  <div class="rounded-md border bg-background px-2.5 py-1.5 text-muted-foreground">+1 (555) 000-0000</div>
                </div>
              {:else if item.kind === "notes" && !item.hidden}
                <div>
                  <div class="mb-1 font-medium text-foreground">Additional notes</div>
                  <div class="rounded-md border bg-background px-2.5 py-2 text-muted-foreground">Please share anything that will help prepare...</div>
                </div>
              {:else if item.kind === "guests" && !item.hidden}
                <div>
                  <button
                    type="button"
                    onclick={() => {
                      previewGuestsOpen = !previewGuestsOpen;
                      if (previewGuestsOpen && previewGuestRows.length === 0) previewGuestRows = [""];
                    }}
                    class="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
                  >
                    <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="8.5" cy="7" r="4"/><line x1="20" y1="8" x2="20" y2="14"/><line x1="23" y1="11" x2="17" y2="11"/></svg>
                    Add guests
                  </button>
                  {#if previewGuestsOpen}
                    <div class="mt-2 space-y-2 rounded-md border bg-muted/20 p-2">
                      <div class="inline-flex rounded-md border bg-muted/40 p-0.5 text-[11px]">
                        <button type="button" onclick={() => (previewGuestsMode = "single")} class="rounded px-2 py-0.5 {previewGuestsMode === 'single' ? 'bg-background text-foreground shadow-xs' : 'text-muted-foreground'}">One at a time</button>
                        <button type="button" onclick={() => (previewGuestsMode = "list")} class="rounded px-2 py-0.5 {previewGuestsMode === 'list' ? 'bg-background text-foreground shadow-xs' : 'text-muted-foreground'}">Add a list</button>
                      </div>
                      {#if previewGuestsMode === "single"}
                        <div class="space-y-1.5">
                          {#each previewGuestRows as _, rIdx}
                            <div class="flex items-center gap-1.5">
                              <div class="flex-1 rounded-md border bg-background px-2.5 py-1.5 text-muted-foreground">Email</div>
                              <button
                                type="button"
                                onclick={() => {
                                  previewGuestRows = previewGuestRows.filter((_, i) => i !== rIdx);
                                  if (previewGuestRows.length === 0) previewGuestsOpen = false;
                                }}
                                class="rounded border px-2 py-1 text-muted-foreground hover:text-foreground"
                                aria-label="Remove guest"
                              >×</button>
                            </div>
                          {/each}
                          <button
                            type="button"
                            onclick={() => (previewGuestRows = [...previewGuestRows, ""])}
                            class="inline-flex items-center gap-1 text-[11px] font-medium text-muted-foreground hover:text-foreground"
                          >
                            + Add another
                          </button>
                        </div>
                      {:else}
                        <div class="rounded-md border bg-background px-2.5 py-2 text-muted-foreground">john@doe.com, alex@smith.com</div>
                      {/if}
                    </div>
                  {/if}
                </div>
              {:else if item.kind === "question" && !item.question.hidden}
                {@const q = item.question}
                <div>
                  {#if q.type === "checkbox"}
                    <div class="flex items-center gap-2 py-0.5">
                      <div class="h-3.5 w-3.5 shrink-0 rounded border bg-background"></div>
                      <span class="font-medium text-foreground">{q.label}{#if q.required}<span class="text-destructive">*</span>{/if}</span>
                    </div>
                  {:else if q.type === "select"}
                    <div class="mb-1 font-medium text-foreground">{q.label}{#if q.required}<span class="text-destructive">*</span>{/if}</div>
                    <div class="flex items-center justify-between rounded-md border bg-background px-2.5 py-1.5 text-muted-foreground">
                      <span>{q.options?.[0] ?? "Choose an option"}</span>
                      <span>▾</span>
                    </div>
                  {:else}
                    <div class="mb-1 font-medium text-foreground">{q.label}{#if q.required}<span class="text-destructive">*</span>{/if}</div>
                    <div class="rounded-md border bg-background px-2.5 py-2 text-muted-foreground">Answer...</div>
                  {/if}
                </div>
              {/if}
            {/each}
            <div class="pt-1">
              <div class="w-full rounded-md bg-primary py-2 text-center text-xs font-medium text-primary-foreground">
                Confirm booking
              </div>
            </div>
          </div>
        </div>
      {:else}
        <div class="grid grid-cols-1 sm:grid-cols-[minmax(0,1fr)_148px]">
          <div class="p-5">
            <div class="mb-3 flex items-center justify-between text-xs font-semibold">
              <span>November 2026</span>
              <div class="flex gap-1 text-muted-foreground">
                <span class="rounded border px-1.5 py-0.5">&lsaquo;</span>
                <span class="rounded border px-1.5 py-0.5">&rsaquo;</span>
              </div>
            </div>
            <div class="mb-1.5 grid grid-cols-7 gap-1 text-center text-[10px] font-medium text-muted-foreground">
              {#each weekdays as w}<span>{w}</span>{/each}
            </div>
            <div class="grid grid-cols-7 gap-1 text-center text-[11px]">
              {#each days as d}
                {@const isAvail = d >= 17 && (d % 7 !== 0 && d % 7 !== 6)}
                <button
                  type="button"
                  disabled={!isAvail}
                  onclick={() => (selectedDay = d)}
                  class="aspect-square rounded-md font-medium transition-colors {selectedDay === d
                    ? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-950'
                    : isAvail
                      ? 'border border-zinc-200 bg-zinc-100 text-zinc-900 hover:bg-zinc-200 dark:border-zinc-700 dark:bg-zinc-800/90 dark:text-zinc-100 dark:hover:bg-zinc-700'
                      : 'cursor-default text-zinc-500 opacity-35'}"
                >
                  {d}
                </button>
              {/each}
            </div>
            <div class="mt-3 inline-flex items-center gap-1 rounded-md border bg-muted/30 px-2 py-1 text-[10px] text-muted-foreground">
              America/New York ▾
            </div>
          </div>
          <div class="border-t p-4 sm:border-l sm:border-t-0">
            <p class="mb-2 text-[11px] font-medium text-muted-foreground">Wed, Nov {selectedDay ?? 18}</p>
            <div class="max-h-[380px] space-y-1.5 overflow-y-auto">
              {#each slots as s}
                <button
                  type="button"
                  onclick={() => (selectedSlot = s)}
                  class="w-full rounded-md border px-2 py-1.5 text-center text-[11px] font-medium transition-colors {selectedSlot === s
                    ? 'border-primary bg-primary text-primary-foreground'
                    : 'hover:bg-accent'}"
                >
                  {s}
                </button>
              {/each}
            </div>
          </div>
        </div>
      {/if}
    </div>
  {/if}
</div>
