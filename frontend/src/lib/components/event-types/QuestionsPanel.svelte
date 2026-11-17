<script lang="ts">
  import { onMount } from "svelte";
  import { api, type Question, type QuestionType } from "$lib/api";
  import { Button, buttonVariants } from "$lib/components/ui/button";
  import { ConfirmDialog } from "$lib/components/ui/confirm-dialog";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Switch } from "$lib/components/ui/switch";
  import { Textarea } from "$lib/components/ui/textarea";
  import * as Select from "$lib/components/ui/select";
  import * as Tooltip from "$lib/components/ui/tooltip";
  import * as Dialog from "$lib/components/ui/dialog";
  import { toast } from "svelte-sonner";
  import { t, tr } from "$lib/i18n";

  let {
    slug,
    allowPhoneCall = false,
    bookingFormConfig = "",
    onQuestionsChange,
    onFormConfigChange,
  }: {
    slug: string;
    allowPhoneCall?: boolean;
    bookingFormConfig?: string;
    onQuestionsChange?: (items: Question[]) => void;
    onFormConfigChange?: (configJson: string) => void;
  } = $props();

  type FormConfigEntry = {
    key: string;
    hidden?: boolean;
    required?: boolean;
  };

  type UnifiedItem =
    | {
        kind: "system";
        key: "name" | "email" | "phone" | "notes" | "guests";
        label: string;
        typeLabel: string;
        hidden: boolean;
        required: boolean;
        locked: boolean;
      }
    | {
        kind: "question";
        key: string;
        question: Question;
      };

  const SYSTEM_DEFAULTS: Record<
    "name" | "email" | "phone" | "notes" | "guests",
    { label: string; typeLabel: string; required: boolean; locked: boolean }
  > = {
    name: { label: "Your name", typeLabel: "Name", required: true, locked: true },
    email: { label: "Email address", typeLabel: "Email", required: true, locked: true },
    phone: { label: "Phone number", typeLabel: "Phone", required: false, locked: false },
    notes: { label: "Additional notes", typeLabel: "Long Text", required: false, locked: false },
    guests: { label: "Add guests", typeLabel: "Multiple Emails", required: false, locked: false },
  };

  const OPTION_TYPES = new Set<QuestionType>([
    "select",
    "multiselect",
    "checkbox_group",
    "radio_group",
  ]);
  const needsOptions = (t: QuestionType) => OPTION_TYPES.has(t);

  const QUESTION_TYPES = $derived<{ value: QuestionType; label: string }[]>([
    { value: "text", label: $t("eventTypeEditor.questions.types.text") },
    { value: "number", label: $t("eventTypeEditor.questions.types.number") },
    { value: "phone", label: $t("eventTypeEditor.questions.types.phone") },
    { value: "address", label: $t("eventTypeEditor.questions.types.address") },
    { value: "url", label: $t("eventTypeEditor.questions.types.url") },
    { value: "select", label: $t("eventTypeEditor.questions.types.select") },
    { value: "multiselect", label: $t("eventTypeEditor.questions.types.multiselect") },
    {
      value: "checkbox",
      label: $t("eventTypeEditor.questions.types.checkbox"),
    },
    { value: "checkbox_group", label: $t("eventTypeEditor.questions.types.checkboxGroup") },
    { value: "radio_group", label: $t("eventTypeEditor.questions.types.radioGroup") },
    { value: "multiple_emails", label: $t("eventTypeEditor.questions.types.multipleEmails") },
  ]);

  let questions: Question[] = $state([]);
  let qLoading = $state(true);

  let qForm = $state({
    label: "",
    type: "text" as QuestionType,
    options: "",
    required: false,
    identifier: "",
    hidden: false,
    lock_if_prefilled: false,
  });
  let qAdding = $state(false);
  let addQOpen = $state(false);
  let identifierTouched = $state(false);

  function slugifyIdentifier(label: string): string {
    const words = label
      .trim()
      .replace(/[^a-zA-Z0-9\s]+/g, " ")
      .trim()
      .split(/\s+/)
      .filter(Boolean);
    if (words.length === 0) return "";
    return words
      .map((w, i) =>
        i === 0 ? w.toLowerCase() : w.charAt(0).toUpperCase() + w.slice(1).toLowerCase()
      )
      .join("");
  }

  let editingQId = $state<string | null>(null);
  let deleteQOpen = $state(false);
  let pendingQ = $state<Question | null>(null);
  let editQForm = $state({
    label: "",
    type: "text" as QuestionType,
    options: "",
    required: false,
    identifier: "",
    hidden: false,
    lock_if_prefilled: false,
  });
  let qSaving = $state(false);

  async function loadQuestions() {
    try {
      const res = await api.get<{ items: Question[] }>(
        `/v1/event-types/${slug}/questions/admin`,
      );
      questions = (res.items ?? []).sort((a, b) => a.position - b.position);
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.load"));
    } finally {
      qLoading = false;
    }
  }

  // This panel is only mounted once the parent's "Questions" tab is selected
  // ({#if activeTab === 'questions'}), so it must fetch its own data on mount
  // rather than relying on the parent to trigger a load — previously nothing
  // called loadQuestions() until a mutation (add/save/delete) did so as a
  // side effect, which is why existing questions never showed up until you
  // added a new one.
  onMount(loadQuestions);

  $effect(() => {
    onQuestionsChange?.(questions);
  });

  function optionsArray(raw: string): string[] {
    return raw
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
  }

  async function addQuestion(): Promise<boolean> {
    if (!qForm.label.trim()) {
      toast.error(tr("eventTypeEditor.questions.validation.label"));
      return false;
    }
    if (needsOptions(qForm.type) && optionsArray(qForm.options).length === 0) {
      toast.error(tr("eventTypeEditor.questions.validation.options"));
      return false;
    }
    qAdding = true;
    try {
      await api.post(`/v1/event-types/${slug}/questions`, {
        label: qForm.label.trim(),
        type: qForm.type,
        options: needsOptions(qForm.type)
          ? optionsArray(qForm.options)
          : undefined,
        required: qForm.required,
        identifier: qForm.identifier.trim(),
        hidden: qForm.hidden,
        lock_if_prefilled: qForm.lock_if_prefilled,
      });
      qForm = {
        label: "",
        type: "text",
        options: "",
        required: false,
        identifier: "",
        hidden: false,
        lock_if_prefilled: false,
      };
      await loadQuestions();
      return true;
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.add"));
      return false;
    } finally {
      qAdding = false;
    }
  }

  function startEditQ(q: Question) {
    editingQId = q.id;
    editQForm = {
      label: q.label,
      type: q.type,
      options: (q.options ?? []).join("\n"),
      required: q.required,
      identifier: q.identifier ?? "",
      hidden: !!q.hidden,
      lock_if_prefilled: !!q.lock_if_prefilled,
    };
  }

  function cancelEditQ() {
    editingQId = null;
  }

  async function saveQuestion(q: Question) {
    if (!editQForm.label.trim()) {
      toast.error(tr("eventTypeEditor.questions.validation.label"));
      return;
    }
    if (
      needsOptions(editQForm.type) &&
      optionsArray(editQForm.options).length === 0
    ) {
      toast.error(tr("eventTypeEditor.questions.validation.optionsShort"));
      return;
    }
    qSaving = true;
    try {
      await api.patch(`/v1/event-types/${slug}/questions/${q.id}`, {
        label: editQForm.label.trim(),
        type: editQForm.type,
        options: needsOptions(editQForm.type)
          ? optionsArray(editQForm.options)
          : [],
        required: editQForm.required,
        identifier: editQForm.identifier.trim(),
        hidden: editQForm.hidden,
        lock_if_prefilled: editQForm.lock_if_prefilled,
      });
      editingQId = null;
      await loadQuestions();
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.save"));
    } finally {
      qSaving = false;
    }
  }

  function deleteQuestion(q: Question) {
    pendingQ = q;
    deleteQOpen = true;
  }

  async function doDeleteQuestion() {
    if (!pendingQ) return;
    try {
      await api.del(`/v1/event-types/${slug}/questions/${pendingQ.id}`);
      await loadQuestions();
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.delete"));
    }
  }

  function typeLabel(value: string): string {
    return QUESTION_TYPES.find((t) => t.value === value)?.label ?? value;
  }

  let dragIdx = $state<number | null>(null);
  let dragOverIdx = $state<number | null>(null);
  let reordering = $state(false);

  let unifiedItems = $derived.by((): UnifiedItem[] => {
    const qById = new Map(questions.map((q) => [q.id, q]));
    let parsed: FormConfigEntry[] = [];
    if (bookingFormConfig && bookingFormConfig.trim() !== "") {
      try {
        const raw = JSON.parse(bookingFormConfig);
        if (Array.isArray(raw)) parsed = raw;
      } catch {
        parsed = [];
      }
    }
    const seen = new Set<string>();
    const out: UnifiedItem[] = [];

    const addSystem = (
      key: "name" | "email" | "phone" | "notes" | "guests",
      hidden: boolean,
      required: boolean,
    ) => {
      if (seen.has(key)) return;
      seen.add(key);
      const def = SYSTEM_DEFAULTS[key];
      out.push({
        kind: "system",
        key,
        label: def.label,
        typeLabel: def.typeLabel,
        hidden: def.locked ? false : key === "phone" && allowPhoneCall ? false : hidden,
        required: def.locked ? true : required,
        locked: def.locked,
      });
    };

    for (const entry of parsed) {
      if (
        entry.key === "name" ||
        entry.key === "email" ||
        entry.key === "phone" ||
        entry.key === "notes" ||
        entry.key === "guests"
      ) {
        addSystem(entry.key, !!entry.hidden, !!entry.required);
      } else if (entry.key?.startsWith("q:")) {
        const qid = entry.key.slice(2);
        const q = qById.get(qid);
        if (q && !seen.has(entry.key)) {
          seen.add(entry.key);
          out.push({ kind: "question", key: entry.key, question: q });
        }
      }
    }

    addSystem("name", false, true);
    addSystem("email", false, true);
    addSystem("phone", false, allowPhoneCall);
    addSystem("notes", false, false);
    addSystem("guests", false, false);

    for (const q of questions) {
      const k = `q:${q.id}`;
      if (!seen.has(k)) {
        seen.add(k);
        out.push({ kind: "question", key: k, question: q });
      }
    }
    return out;
  });

  async function saveUnifiedConfig(items: UnifiedItem[]) {
    const payload: FormConfigEntry[] = items.map((it) =>
      it.kind === "system"
        ? { key: it.key, hidden: it.hidden, required: it.required }
        : { key: it.key, hidden: !!it.question.hidden, required: it.question.required },
    );
    const json = JSON.stringify(payload);
    onFormConfigChange?.(json);
    reordering = true;
    try {
      await api.patch(`/v1/event-types/${slug}`, { booking_form_config: json });
      const customOrder = items
        .filter((it): it is Extract<UnifiedItem, { kind: "question" }> => it.kind === "question")
        .map((it) => it.question);
      if (customOrder.length > 0) {
        questions = customOrder.map((q, idx) => ({ ...q, position: idx }));
        await Promise.all(
          customOrder.map((q, idx) =>
            api.patch(`/v1/event-types/${slug}/questions/${q.id}`, { position: idx }),
          ),
        );
      }
    } catch (e: any) {
      toast.error(e.message || "Failed to update booking form");
    } finally {
      reordering = false;
    }
  }

  async function reorderUnified(fromIdx: number, toIdx: number) {
    if (
      fromIdx === toIdx ||
      fromIdx < 0 ||
      toIdx < 0 ||
      toIdx >= unifiedItems.length ||
      reordering
    )
      return;
    const next = [...unifiedItems];
    const [moved] = next.splice(fromIdx, 1);
    next.splice(toIdx, 0, moved);
    await saveUnifiedConfig(next);
  }

  async function toggleSystemVisibility(
    key: "phone" | "notes" | "guests",
    visible: boolean,
  ) {
    const next = unifiedItems.map((it) =>
      it.kind === "system" && it.key === key ? { ...it, hidden: !visible } : it,
    );
    await saveUnifiedConfig(next);
  }

  async function toggleQuestionVisibility(q: Question, visible: boolean) {
    try {
      await api.patch(`/v1/event-types/${slug}/questions/${q.id}`, {
        hidden: !visible,
      });
      await loadQuestions();
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.questions.error.save"));
    }
  }
</script>

<ConfirmDialog
  bind:open={deleteQOpen}
  title={$t("eventTypeEditor.questions.confirm.title")}
  description={pendingQ
    ? $t("eventTypeEditor.questions.confirm.description", {
        label: pendingQ.label,
      })
    : ""}
  confirmText={$t("common.remove")}
  destructive
  onConfirm={doDeleteQuestion}
/>

<!-- Booking Form -->
<div class="space-y-4">
  <h2
    class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
  >
    Booking form
  </h2>
  <p class="mb-3 text-sm text-muted-foreground">
    {$t("eventTypeEditor.questions.subtitle")}
  </p>

  <!-- Unified drag-and-drop booking questions list -->
  <div class="rounded-lg border bg-card overflow-hidden">
    {#if qLoading}
      <p class="px-4 py-4 text-sm text-muted-foreground">
        {$t("common.loading")}
      </p>
    {:else}
      <div class="divide-y" role="list">
        {#each unifiedItems as item, idx (item.key)}
          {#if item.kind === "system"}
            <div
              role="listitem"
              draggable={!reordering && editingQId === null}
              ondragstart={(e) => {
                dragIdx = idx;
                e.dataTransfer?.setData("text/plain", String(idx));
              }}
              ondragover={(e) => {
                e.preventDefault();
                dragOverIdx = idx;
              }}
              ondragleave={() => {
                if (dragOverIdx === idx) dragOverIdx = null;
              }}
              ondrop={async (e) => {
                e.preventDefault();
                dragOverIdx = null;
                if (dragIdx !== null && dragIdx !== idx) {
                  await reorderUnified(dragIdx, idx);
                }
                dragIdx = null;
              }}
              ondragend={() => {
                dragIdx = null;
                dragOverIdx = null;
              }}
              class="flex items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-muted/20 {dragOverIdx === idx ? 'bg-muted/40 ring-1 ring-inset ring-primary/40' : ''}"
            >
              <div class="flex items-center gap-3 min-w-0">
                <span
                  class="cursor-grab active:cursor-grabbing text-muted-foreground/60 hover:text-foreground shrink-0"
                  title="Drag to reorder"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="9" cy="5" r="1"/><circle cx="9" cy="12" r="1"/><circle cx="9" cy="19" r="1"/><circle cx="15" cy="5" r="1"/><circle cx="15" cy="12" r="1"/><circle cx="15" cy="19" r="1"/></svg>
                </span>
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-1.5">
                    <span class="text-sm font-medium {item.hidden ? 'text-muted-foreground line-through' : ''}">{item.label}</span>
                    <span class="rounded border px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                      {item.required ? "Required" : "Optional"}
                    </span>
                    {#if item.hidden}
                      <span class="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">Hidden</span>
                    {/if}
                  </div>
                  <div class="text-xs text-muted-foreground">{item.typeLabel}</div>
                </div>
              </div>
              <div class="flex items-center gap-2 shrink-0">
                {#if !item.locked}
                  <Switch
                    checked={!item.hidden}
                    disabled={item.key === "phone" && allowPhoneCall}
                    onCheckedChange={(v) => {
                      if (item.key === "phone" || item.key === "notes" || item.key === "guests") {
                        toggleSystemVisibility(item.key, v);
                      }
                    }}
                    aria-label="Toggle {item.label}"
                  />
                {/if}
              </div>
            </div>
          {:else}
            {@const q = item.question}
            {#if editingQId === q.id}
              <div class="bg-muted/20 p-4 space-y-3">
                <div class="grid gap-3 sm:grid-cols-2">
                  <div class="space-y-1.5">
                    <Label for="eq-label-{q.id}" class="text-xs text-muted-foreground">{$t("eventTypeEditor.questions.label")}</Label>
                    <Input id="eq-label-{q.id}" bind:value={editQForm.label} />
                  </div>
                  <div class="space-y-1.5">
                    <Label for="eq-type-{q.id}" class="text-xs text-muted-foreground">{$t("eventTypeEditor.questions.table.type")}</Label>
                    <Select.Root
                      type="single"
                      value={editQForm.type}
                      onValueChange={(v) => {
                        if (v) editQForm.type = v as QuestionType;
                      }}
                    >
                      <Select.Trigger id="eq-type-{q.id}" class="w-full">
                        {QUESTION_TYPES.find((t) => t.value === editQForm.type)?.label ?? $t("common.select")}
                      </Select.Trigger>
                      <Select.Content>
                        {#each QUESTION_TYPES as qt}
                          <Select.Item value={qt.value} label={qt.label}>{qt.label}</Select.Item>
                        {/each}
                      </Select.Content>
                    </Select.Root>
                  </div>
                  {#if needsOptions(editQForm.type)}
                    <div class="sm:col-span-2 space-y-1.5">
                      <Label for="eq-options-{q.id}" class="text-xs text-muted-foreground">{$t("eventTypeEditor.questions.options")}</Label>
                      <Textarea id="eq-options-{q.id}" bind:value={editQForm.options} rows={3} />
                    </div>
                  {/if}
                  <div class="space-y-1.5">
                    <Label for="eq-ident-{q.id}" class="text-xs text-muted-foreground">Identifier</Label>
                    <Input id="eq-ident-{q.id}" bind:value={editQForm.identifier} placeholder="company_name" />
                  </div>
                  <div class="flex flex-wrap items-center gap-4 pt-5">
                    <div class="flex items-center gap-2">
                      <Switch id="eq-req-{q.id}" bind:checked={editQForm.required} />
                      <Label for="eq-req-{q.id}" class="text-xs">Required</Label>
                    </div>
                    <div class="flex items-center gap-2">
                      <Switch id="eq-hid-{q.id}" bind:checked={editQForm.hidden} />
                      <Label for="eq-hid-{q.id}" class="text-xs">Hidden</Label>
                    </div>
                    <div class="flex items-center gap-2">
                      <Switch id="eq-lock-{q.id}" bind:checked={editQForm.lock_if_prefilled} />
                      <Label for="eq-lock-{q.id}" class="text-xs">Lock if prefilled</Label>
                    </div>
                  </div>
                </div>
                <div class="flex justify-end gap-2">
                  <Button type="button" variant="outline" size="sm" onclick={cancelEditQ}>{$t("common.cancel")}</Button>
                  <Button type="button" size="sm" disabled={qSaving} onclick={() => saveQuestion(q)}>
                    {qSaving ? $t("common.saving") : $t("common.save")}
                  </Button>
                </div>
              </div>
            {:else}
              <div
                role="listitem"
                draggable={!reordering && editingQId === null}
                ondragstart={(e) => {
                  dragIdx = idx;
                  e.dataTransfer?.setData("text/plain", String(idx));
                }}
                ondragover={(e) => {
                  e.preventDefault();
                  dragOverIdx = idx;
                }}
                ondragleave={() => {
                  if (dragOverIdx === idx) dragOverIdx = null;
                }}
                ondrop={async (e) => {
                  e.preventDefault();
                  dragOverIdx = null;
                  if (dragIdx !== null && dragIdx !== idx) {
                    await reorderUnified(dragIdx, idx);
                  }
                  dragIdx = null;
                }}
                ondragend={() => {
                  dragIdx = null;
                  dragOverIdx = null;
                }}
                class="flex items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-muted/20 {dragOverIdx === idx ? 'bg-muted/40 ring-1 ring-inset ring-primary/40' : ''}"
              >
                <div class="flex items-center gap-3 min-w-0">
                  <span
                    class="cursor-grab active:cursor-grabbing text-muted-foreground/60 hover:text-foreground shrink-0"
                    title="Drag to reorder"
                  >
                    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="9" cy="5" r="1"/><circle cx="9" cy="12" r="1"/><circle cx="9" cy="19" r="1"/><circle cx="15" cy="5" r="1"/><circle cx="15" cy="12" r="1"/><circle cx="15" cy="19" r="1"/></svg>
                  </span>
                  <div class="min-w-0">
                    <div class="flex flex-wrap items-center gap-1.5">
                      <span class="text-sm font-medium {q.hidden ? 'text-muted-foreground line-through' : ''}">{q.label}</span>
                      <span class="rounded border px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                        {q.required ? "Required" : "Optional"}
                      </span>
                      {#if q.hidden}
                        <span class="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                          {$t("eventTypeEditor.questions.hiddenBadge")}
                        </span>
                      {/if}
                      {#if q.identifier}
                        <span class="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                          ?{q.identifier}
                        </span>
                      {/if}
                    </div>
                    <div class="text-xs text-muted-foreground">
                      {typeLabel(q.type)}{#if needsOptions(q.type) && q.options?.length} · {q.options.join(", ")}{/if}
                    </div>
                  </div>
                </div>
                <div class="flex items-center gap-2 shrink-0">
                  <Switch
                    checked={!q.hidden}
                    onCheckedChange={(v) => toggleQuestionVisibility(q, v)}
                    aria-label="Toggle {q.label}"
                  />
                  <Button type="button" variant="outline" size="sm" class="h-7 px-2.5 text-xs" onclick={() => startEditQ(q)}>
                    Edit
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    class="h-7 w-7 text-muted-foreground hover:text-destructive"
                    onclick={() => deleteQuestion(q)}
                    aria-label={$t("eventTypeEditor.questions.tooltip.remove")}
                  >
                    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
                  </Button>
                </div>
              </div>
            {/if}
          {/if}
        {/each}
      </div>
    {/if}

    <!-- Add question button + modal -->
    <div class="border-t px-4 py-3">
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => (addQOpen = true)}
      >
        + {$t("eventTypeEditor.questions.add")}
      </Button>
    </div>
  </div>
</div>

<Dialog.Root bind:open={addQOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>{$t("eventTypeEditor.questions.add")}</Dialog.Title>
      <Dialog.Description
        >Add a custom question to the booking form.</Dialog.Description
      >
    </Dialog.Header>
    <div class="space-y-4 py-2">
      <div class="space-y-1.5">
        <Label for="q-type">{$t("eventTypeEditor.questions.table.type")}</Label>
        <Select.Root
          type="single"
          value={qForm.type}
          onValueChange={(v) => {
            if (v) qForm.type = v as QuestionType;
          }}
        >
          <Select.Trigger id="q-type" class="w-full">
            {QUESTION_TYPES.find((t) => t.value === qForm.type)?.label ??
              $t("common.select")}
          </Select.Trigger>
          <Select.Content>
            {#each QUESTION_TYPES as qt}
              <Select.Item value={qt.value} label={qt.label}
                >{qt.label}</Select.Item
              >
            {/each}
          </Select.Content>
        </Select.Root>
      </div>
      <div class="space-y-1.5">
        <Label for="q-label">{$t("eventTypeEditor.questions.label")}</Label>
        <Input
          id="q-label"
          bind:value={qForm.label}
          oninput={() => {
            if (!identifierTouched) {
              qForm.identifier = slugifyIdentifier(qForm.label);
            }
          }}
          placeholder={$t("eventTypeEditor.questions.labelPlaceholder")}
        />
      </div>
      {#if needsOptions(qForm.type)}
        <div class="space-y-1.5">
          <Label for="q-options"
            >{$t("eventTypeEditor.questions.options")}</Label
          >
          <Textarea
            id="q-options"
            bind:value={qForm.options}
            rows={3}
            placeholder={$t("eventTypeEditor.questions.optionsPlaceholder")}
          />
        </div>
      {/if}
      <div class="space-y-1.5">
        <Label for="q-identifier">{$t("eventTypeEditor.questions.identifierLabel")}</Label>
        <Input
          id="q-identifier"
          bind:value={qForm.identifier}
          oninput={() => {
            identifierTouched = qForm.identifier.trim().length > 0;
          }}
          placeholder={$t("eventTypeEditor.questions.identifierPlaceholder")}
        />
      </div>
      <div
        class="flex items-center justify-between rounded-md border px-3 py-2.5"
      >
        <Label for="q-required" class="cursor-pointer font-normal"
          >{$t("eventTypeEditor.questions.table.required")}</Label
        >
        <Switch id="q-required" bind:checked={qForm.required} />
      </div>
      <div
        class="flex items-center justify-between rounded-md border px-3 py-2.5"
      >
        <Label for="q-hidden" class="cursor-pointer font-normal"
          >{$t("eventTypeEditor.questions.hiddenLabel")}</Label
        >
        <Switch id="q-hidden" bind:checked={qForm.hidden} />
      </div>
      <div
        class="flex items-center justify-between rounded-md border px-3 py-2.5"
      >
        <Label for="q-lock" class="cursor-pointer font-normal"
          >{$t("eventTypeEditor.questions.lockLabel")}</Label
        >
        <Switch id="q-lock" bind:checked={qForm.lock_if_prefilled} />
      </div>
    </div>
    <Dialog.Footer>
      <Button
        type="button"
        variant="outline"
        onclick={() => (addQOpen = false)}
      >
        {$t("common.cancel")}
      </Button>
      <Button
        type="button"
        disabled={qAdding}
        onclick={async () => {
          if (await addQuestion()) addQOpen = false;
        }}
      >
        {qAdding
          ? $t("eventTypeEditor.questions.adding")
          : $t("eventTypeEditor.questions.add")}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
