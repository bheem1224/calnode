<script lang="ts">
  import { onMount } from "svelte";
  import { page } from "$app/stores";
  import { goto, beforeNavigate } from "$app/navigation";
  import { base } from "$app/paths";
  import {
    api,
    type EventType,
    type EventTypeHost,
    type LocationOption,
    type TeamMember,
    type Team,
    type CalendarStatus,
    type ZoomStatus,
    type SingleUseLink,
    type Question,
    type AvailabilitySchedule,
  } from "$lib/api";
  import { currentUser } from "$lib/stores";
  import { Button, buttonVariants } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Checkbox } from "$lib/components/ui/checkbox";
  import { Textarea } from "$lib/components/ui/textarea";
  import { Switch } from "$lib/components/ui/switch";
  import * as Select from "$lib/components/ui/select";
  import * as Tooltip from "$lib/components/ui/tooltip";
  import { toast } from "svelte-sonner";
  import { saveOnCmdS } from "$lib/save-shortcut";
  import QuestionsPanel from "$lib/components/event-types/QuestionsPanel.svelte";
  import EmbedPanel from "$lib/components/event-types/EmbedPanel.svelte";
  import BookingPreviewCard from "$lib/components/event-types/BookingPreviewCard.svelte";
  import EmailTemplateModal from "$lib/components/event-types/EmailTemplateModal.svelte";
  import WebhooksManager from "$lib/components/WebhooksManager.svelte";
  import { ConfirmDialog } from "$lib/components/ui/confirm-dialog";
  import * as Dialog from "$lib/components/ui/dialog";
  import * as Popover from "$lib/components/ui/popover";
  import { DateRangePicker } from "$lib/components/ui/date-range-picker";
  import { t, tr, localeTag } from "$lib/i18n";
  import LinkedText from "$lib/i18n/linked-text.svelte";

  // Ordered by expected usage. 'custom_video' is retired from the picker but the
  // backend still renders any legacy event types that use it.
  const LOCATION_TYPES = $derived([
    { value: "zoom", label: $t("eventTypeEditor.location.types.zoom") },
    { value: "teams", label: $t("eventTypeEditor.location.types.teams") },
    {
      value: "google_meet",
      label: $t("eventTypeEditor.location.types.googleMeet"),
    },
    { value: "livekit", label: $t("eventTypeEditor.location.types.livekit") },
    { value: "phone", label: $t("eventTypeEditor.location.types.phone") },
    { value: "link", label: $t("eventTypeEditor.location.types.link") },
    {
      value: "in_person",
      label: $t("eventTypeEditor.location.types.inPerson"),
    },
  ]);

  const LOCATION_NEEDS_VALUE = $derived<Record<string, string>>({
    link: $t("eventTypeEditor.location.labels.meetingUrl"),
    zoom: $t("eventTypeEditor.location.labels.zoomLink"),
    google_meet: $t("eventTypeEditor.location.labels.meetLink"),
    teams: $t("eventTypeEditor.location.labels.teamsLink"),
    phone: $t("eventTypeEditor.location.labels.phone"),
    in_person: $t("eventTypeEditor.location.labels.address"),
    custom_video: $t("eventTypeEditor.location.labels.meetingUrl"),
  });
  // Placeholder hint per type — in-person is the only optional value.
  const LOCATION_PLACEHOLDER = $derived<Record<string, string>>({
    zoom: $t("eventTypeEditor.location.placeholder.zoomRequired"),
    link: $t("eventTypeEditor.location.placeholder.linkRequired"),
    phone: $t("eventTypeEditor.location.placeholder.phoneRequired"),
    in_person: $t("eventTypeEditor.location.placeholder.addressOptional"),
  });

  const INVITE_DELIVERY = $derived([
    {
      value: "calendar",
      label: $t("eventTypeEditor.invite.delivery.calendar"),
    },
    { value: "calnode", label: $t("eventTypeEditor.invite.delivery.calnode") },
    { value: "none", label: $t("eventTypeEditor.invite.delivery.none") },
  ]);

  // ── Event type ───────────────────────────────────────────────────────────────
  let et = $state<EventType | null>(null);
  const TABS = $derived([
    { id: "general" as const, label: "Basics", section: "Setup" },
    { id: "availability" as const, label: "Availability", section: "Setup" },
    { id: "hosts" as const, label: $t("eventTypeEditor.tabs.hosts"), section: "Setup" },
    { id: "questions" as const, label: "Booking form", section: "Booking experience" },
    { id: "confirmation" as const, label: "Confirmation", section: "Booking experience" },
    { id: "payments" as const, label: $t("settings.nav.payments"), section: "Booking experience" },
    { id: "limits" as const, label: "Limits & buffers", section: "Policies" },
    { id: "reschedule" as const, label: "Reschedule & cancel", section: "Policies" },
    { id: "privacy" as const, label: $t("eventTypeEditor.tabs.privacy"), section: "Policies" },
    { id: "webhooks" as const, label: $t("webhooks.title"), section: "Automation" },
    { id: "embed" as const, label: $t("eventTypeEditor.tabs.embed"), section: "Automation" },
  ]);
  let activeTab = $state<(typeof TABS)[number]["id"]>("general");
  let durationMenuOpen = $state(false);
  const DURATION_PRESETS = [5, 10, 15, 20, 25, 30, 45, 60, 90, 120];
  let previewQuestions = $state<Question[]>([]);
  let bookingFormConfig = $state("");
  let availRules = $state<{ schedule_id?: string | null; day_of_week: number; start_time: string; end_time: string }[]>([]);
  let etLoading = $state(true);
  let etError = $state("");
  let etSaving = $state(false);
  let savedSnapshot = $state("");
  let unsavedOpen = $state(false);
  let pendingNavHref = $state("");
  let allowNav = $state(false);
  let calTitleModalOpen = $state(false);
  const CAL_TITLE_VARS = [
    { token: "{Event}", desc: "The event type title" },
    { token: "{Duration}", desc: "The event duration" },
    { token: "{Host}", desc: "Your full name" },
    { token: "{HostFirst}", desc: "Your first name" },
    { token: "{Scheduler}", desc: "The full name of the person booking" },
    { token: "{SchedulerFirst}", desc: "The first name of the person booking" },
    { token: "{Location}", desc: "The location of the event" },
  ];

  // Connected calendar (the owner's) — drives the meeting-link auto-generation hint.
  // Only fetched when the viewer owns the event type: the status endpoints describe the
  // caller's own connections, which say nothing about an event type an admin is editing
  // for someone else.
  let calStatus = $state<CalendarStatus | null>(null);
  // Owner's Zoom connection — drives the Zoom auto-mint hint.
  let zoomStatus = $state<ZoomStatus | null>(null);
  // Which connected provider can natively mint each online platform's link.
  const PLATFORM_PROVIDER: Record<string, string> = {
    google_meet: "google",
    teams: "microsoft",
  };
  const isOnlineMeeting = (t: string) => t === "google_meet" || t === "teams";
  // Any location that produces a join link the booker has to receive somehow.
  const isOnlineLocation = (t: string) =>
    isOnlineMeeting(t) || t === "zoom" || t === "livekit";

  let form = $state({
    display_order: 0,
    name: "",
    slug: "",
    description: "",
    duration_minutes: 30,
    slot_interval_minutes: 30,
    is_active: true,
    is_public: true,
    show_taken_slots: false,
    invite_delivery: "calendar" as EventType["invite_delivery"],
    location_type: "link",
    location_value: "",
    buffer_before_minutes: 0,
    buffer_after_minutes: 0,
    min_notice_minutes: 0,
    max_future_days: 60,
    max_active_bookings: 1,
    max_per_day: "" as number | "" | null,
    max_per_week: "" as number | "" | null,
    max_per_month: "" as number | "" | null,
    max_per_year: "" as number | "" | null,
    max_duration_per_day: "" as number | "" | null,
    max_duration_per_week: "" as number | "" | null,
    show_first_slot_only: false,
    future_limit_mode: "calendar_days" as "calendar_days" | "business_days" | "date_range",
    future_range_start: "",
    future_range_end: "",
    calendar_event_title: "",
    redirect_url: "",
    reply_to: "",
    disable_cancel: false,
    disable_reschedule: false,
    require_cancel_reason: "optional" as "none" | "optional" | "required" | "both" | "host_only" | "attendee_only",
    allow_reschedule_past: false,
    hide_notes_in_calendar: false,
    hide_calendar_event_details: false,
    requires_confirmation: false,
    confirm_mode: "always" as "always" | "under_notice" | "free_email",
    confirm_notice_minutes: 30,
    unconfirmed_blocks_slots: true,
    require_email_verification: false,
    allow_multiple_durations: false,
    duration_options: [15, 30, 45, 60] as number[],
    location_options: [] as LocationOption[],
    destination_calendar_id: "",
    schedule_id: "",
    optimized_slots: false,
    team_id: "",
    price_cents: 0,
    currency: "usd",
  });

  // "Your own system" means Calnode sends the booker nothing at all. When email is not
  // configured that is easy to pick by accident and impossible to notice later, so it is
  // confirmed once rather than applied silently.
  let confirmNoneOpen = $state(false);
  let noneAcknowledged = $state(false);
  // Remounts the Select when a choice is declined. bits-ui keeps the clicked option as its
  // internal selection even though the bound value did not change, so re-picking it fires no
  // change event and the dialog would never reopen.
  let inviteSelectReset = $state(0);

  function requestInviteDelivery(v: string | undefined) {
    if (!v) return;
    const mode = v as EventType["invite_delivery"];
    if (
      mode === "none" &&
      et?.invite_sender_ready === false &&
      !noneAcknowledged
    ) {
      confirmNoneOpen = true;
      inviteSelectReset += 1;
      return;
    }
    form.invite_delivery = mode;
    if (mode !== "none") noneAcknowledged = false;
  }

  // Price is edited in major units (e.g. dollars); stored as integer cents.
  let priceMajor = $state("0");
  let paidEnabled = $state(false);

  // Limits & buffers UX helpers (5-min dropdowns + toggles, mapped directly to existing DB columns)
  const BUFFER_OPTIONS = [
    { value: 0, label: "No buffer time" },
    { value: 5, label: "5 minutes" },
    { value: 10, label: "10 minutes" },
    { value: 15, label: "15 minutes" },
    { value: 20, label: "20 minutes" },
    { value: 25, label: "25 minutes" },
    { value: 30, label: "30 minutes" },
    { value: 45, label: "45 minutes" },
    { value: 60, label: "60 minutes" },
    { value: 90, label: "90 minutes" },
    { value: 120, label: "120 minutes" },
  ];
  const NOTICE_OPTIONS = [
    { value: 0, label: "No minimum notice" },
    { value: 5, label: "5 minutes" },
    { value: 10, label: "10 minutes" },
    { value: 15, label: "15 minutes" },
    { value: 30, label: "30 minutes" },
    { value: 45, label: "45 minutes" },
    { value: 60, label: "1 hour" },
    { value: 120, label: "2 hours" },
    { value: 180, label: "3 hours" },
    { value: 240, label: "4 hours" },
    { value: 360, label: "6 hours" },
    { value: 720, label: "12 hours" },
    { value: 1440, label: "24 hours" },
    { value: 2880, label: "48 hours" },
  ];
  const INTERVAL_OPTIONS = [5, 10, 15, 20, 25, 30, 45, 60];

  function optionLabel(
    opts: { value: number; label: string }[],
    v: number,
  ): string {
    return opts.find((o) => o.value === Number(v))?.label ?? `${v} minutes`;
  }

  type FreqUnit = "day" | "week" | "month" | "year";
  const ALL_FREQ_UNITS: FreqUnit[] = ["day", "week", "month", "year"];
  let freqEnabled = $state(false);
  let freqOrder = $state<FreqUnit[]>([]);
  let durEnabled = $state(false);
  let durOrder = $state<("day" | "week")[]>([]);
  let futureLimitEnabled = $state(false);
  let maxActiveEnabled = $state(false);

  function freqLabel(u: FreqUnit): string {
    if (u === "day") return $t("eventTypeEditor.limits.perDay");
    if (u === "week") return $t("eventTypeEditor.limits.perWeek");
    if (u === "month") return $t("eventTypeEditor.limits.perMonth");
    return $t("eventTypeEditor.limits.perYear");
  }

  function getFreqVal(u: FreqUnit): number | "" | null {
    if (u === "day") return form.max_per_day;
    if (u === "week") return form.max_per_week;
    if (u === "month") return form.max_per_month;
    return form.max_per_year;
  }

  function setFreqVal(u: FreqUnit, v: number | "" | null) {
    if (u === "day") form.max_per_day = v;
    else if (u === "week") form.max_per_week = v;
    else if (u === "month") form.max_per_month = v;
    else form.max_per_year = v;
  }

  function syncFreqFromForm() {
    const next: FreqUnit[] = [];
    for (const u of ALL_FREQ_UNITS) {
      const v = getFreqVal(u);
      if (v !== "" && v !== null && v !== undefined) next.push(u);
    }
    freqOrder = next;
    const nextDur: ("day" | "week")[] = [];
    if (form.max_duration_per_day !== "" && form.max_duration_per_day !== null && form.max_duration_per_day !== undefined)
      nextDur.push("day");
    if (form.max_duration_per_week !== "" && form.max_duration_per_week !== null && form.max_duration_per_week !== undefined)
      nextDur.push("week");
    durOrder = nextDur;
  }

  function setFreqRowUnit(idx: number, newUnit: FreqUnit) {
    const oldUnit = freqOrder[idx];
    if (!oldUnit || oldUnit === newUnit) return;
    const val = getFreqVal(oldUnit);
    setFreqVal(oldUnit, "");
    setFreqVal(newUnit, val || 1);
    freqOrder = freqOrder.map((u, i) => (i === idx ? newUnit : u));
  }

  function addFreqRow() {
    const next = ALL_FREQ_UNITS.find((u) => !freqOrder.includes(u));
    if (!next) return;
    setFreqVal(next, next === "day" ? 1 : 3);
    freqOrder = [...freqOrder, next];
  }

  function removeFreqRow(unit: FreqUnit) {
    setFreqVal(unit, "");
    freqOrder = freqOrder.filter((u) => u !== unit);
    if (freqOrder.length === 0) freqEnabled = false;
  }

  function toggleFreqEnabled(on: boolean) {
    freqEnabled = on;
    if (!on) {
      for (const u of ALL_FREQ_UNITS) setFreqVal(u, "");
      freqOrder = [];
    } else if (freqOrder.length === 0) {
      form.max_per_day = 1;
      freqOrder = ["day"];
    }
  }

  function toggleDurEnabled(on: boolean) {
    durEnabled = on;
    if (!on) {
      form.max_duration_per_day = "";
      form.max_duration_per_week = "";
      durOrder = [];
    } else if (durOrder.length === 0) {
      form.max_duration_per_day = 60;
      durOrder = ["day"];
    }
  }

  function addDurRow() {
    if (!durOrder.includes("day")) {
      form.max_duration_per_day = 60;
      durOrder = [...durOrder, "day"];
    } else if (!durOrder.includes("week")) {
      form.max_duration_per_week = 180;
      durOrder = [...durOrder, "week"];
    }
  }

  function removeDurRow(unit: "day" | "week") {
    if (unit === "day") form.max_duration_per_day = "";
    else form.max_duration_per_week = "";
    durOrder = durOrder.filter((u) => u !== unit);
    if (durOrder.length === 0) durEnabled = false;
  }

  function setDurRowUnit(idx: number, newUnit: "day" | "week") {
    const oldUnit = durOrder[idx];
    if (!oldUnit || oldUnit === newUnit) return;
    const val = oldUnit === "day" ? form.max_duration_per_day : form.max_duration_per_week;
    if (oldUnit === "day") {
      form.max_duration_per_day = "";
      form.max_duration_per_week = val || 60;
    } else {
      form.max_duration_per_week = "";
      form.max_duration_per_day = val || 60;
    }
    durOrder = durOrder.map((u, i) => (i === idx ? newUnit : u));
  }

  function toggleFutureLimit(on: boolean) {
    futureLimitEnabled = on;
    form.max_future_days = on
      ? Number(form.max_future_days) > 0
        ? Number(form.max_future_days)
        : 30
      : 0;
  }

  function toggleMaxActive(on: boolean) {
    maxActiveEnabled = on;
    form.max_active_bookings = on
      ? Number(form.max_active_bookings) > 0
        ? Number(form.max_active_bookings)
        : 1
      : 0;
  }

  function togglePaidEnabled(on: boolean) {
    paidEnabled = on;
    if (!on) {
      priceMajor = "0.00";
    } else if (!(Number(priceMajor) > 0)) {
      priceMajor = "25.00";
    }
  }

  // True when the connected calendar will auto-generate the chosen platform's link.
  const meetAutoGen = $derived(
    isOnlineMeeting(form.location_type) &&
      !!calStatus?.connected &&
      calStatus?.provider === PLATFORM_PROVIDER[form.location_type],
  );

  // True when the owner's connected Zoom account will auto-mint a meeting per booking.
  const zoomAutoGen = $derived(
    form.location_type === "zoom" && !!zoomStatus?.connected,
  );

  // ── Routing ──────────────────────────────────────────────────────────────────
  // The editor asks two plain questions — "who can host?" and (for a team)
  // "do they rotate or all attend?" — and derives routing_mode + host roles from
  // the answers. The engine and DB are unchanged; this is purely how roles are
  // authored.
  const RR_STRATEGIES = $derived([
    { value: "even", label: $t("eventTypeEditor.hosts.rr.even") },
    { value: "priority", label: $t("eventTypeEditor.hosts.rr.priority") },
    { value: "soonest", label: $t("eventTypeEditor.hosts.rr.soonest") },
  ]);
  type Strategy = "even" | "priority" | "soonest";
  let rrStrategy = $state<Strategy>("even");

  type Host = { user_id: string; name: string; email: string };
  type TogetherHost = Host & { optional: boolean };

  // Q1: just me, or specific people?  Q2 (people only): rotate, or all attend?
  let hostScope = $state<"me" | "people">("me");
  let staffing = $state<"rotate" | "together">("rotate");
  // Rotation pool (each "rotation"); together = required + optional join-if-free.
  let rotationHosts = $state<Host[]>([]);
  let togetherHosts = $state<TogetherHost[]>([]);
  let hostsLoaded = $state(false);
  let transferOwner = $state("");
  let transferring = $state(false);
  let transferHosts = $state<EventTypeHost[]>([]);
  async function transferEvent() {
    if (!transferOwner || !$currentUser) return;
    transferring = true;
    try {
      await api.post(`/v1/event-types/${slug}/transfer`, {
        expected_owner_id: $currentUser.id,
        new_owner_id: transferOwner,
      });
      toast.success(tr("eventTypeEditor.toast.transferred"));
      await goto(`${base}/event-types`);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : tr("eventTypeEditor.error.transfer"),
      );
    } finally {
      transferring = false;
    }
  }

  let members = $state<TeamMember[]>([]);
  let teams = $state<Team[]>([]);

  // routing_mode is derived from the two answers — never set directly.
  const routingMode = $derived(
    hostScope === "me"
      ? "fixed"
      : staffing === "rotate"
        ? "round_robin"
        : "collective",
  );
  // An admin editing someone else's event type: "Just me" then means the owner, and the
  // owner's connections, not the admin's, decide which meeting links can be generated.
  const editingForOwner = $derived(
    !!et && et.owned === false && et.can_edit !== false,
  );
  const ownerLabel = $derived(
    et?.owner_name || et?.owner_email || $t("eventTypeEditor.ownerFallback"),
  );

  async function loadHosts() {
    try {
      const res = await api.get<{ items: EventTypeHost[] }>(
        `/v1/event-types/${slug}/hosts`,
      );
      const items = res.items ?? [];
      transferHosts = items.filter(
        (h) => h.role === "required" && h.user_id !== $currentUser?.id,
      );
      const toHost = (h: EventTypeHost): Host => ({
        user_id: h.user_id,
        name: h.name,
        email: h.email,
      });
      rotationHosts = items.filter((h) => h.role === "rotation").map(toHost);
      togetherHosts = items
        .filter((h) => h.role === "required" || h.role === "optional")
        .map((h) => ({ ...toHost(h), optional: h.role === "optional" }));
      hostsLoaded = true;
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.error.loadHosts"));
    }
  }

  async function loadMembers() {
    if (members.length > 0) return;
    try {
      members = await api.get<TeamMember[]>("/v1/users");
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.error.loadMembers"));
    }
  }

  async function loadTeams() {
    if (teams.length > 0) return;
    try {
      const res = await api.get<{ items: Team[] }>("/v1/teams");
      teams = res.items ?? [];
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.error.loadTeams"));
    }
  }

  function setScope(s: "me" | "people") {
    hostScope = s;
    if (s === "people") {
      if (!hostsLoaded) loadHosts();
      loadMembers();
      loadTeams();
    }
  }

  // Members already chosen in the *active* staffing list, so the pickers don't
  // re-offer them. Scoped to the visible list (rotation vs together): otherwise
  // hosts loaded for the other mode — e.g. a collective event's members still in
  // togetherHosts after switching to Rotate — would wrongly mark everyone
  // unavailable and make addOne skip every team member.
  const assignedIds = $derived(
    new Set(
      (staffing === "rotate" ? rotationHosts : togetherHosts).map(
        (h) => h.user_id,
      ),
    ),
  );
  // Any active member not already chosen — including the current user, who can
  // be a host like anyone else.
  const availableMembers = $derived(
    members.filter((m) => !m.archived && !assignedIds.has(m.id)),
  );

  type Target = "rotation" | "together";
  function addOne(target: Target, h: Host) {
    // Dedup against this target's own list, not the union — a person can sit in
    // only one staffing list, and the other list may hold stale members from a
    // previous mode.
    const list: { user_id: string }[] =
      target === "rotation" ? rotationHosts : togetherHosts;
    if (list.some((x) => x.user_id === h.user_id)) return;
    if (target === "rotation") rotationHosts = [...rotationHosts, h];
    else togetherHosts = [...togetherHosts, { ...h, optional: false }];
  }
  function addMember(target: Target, userId: string | undefined) {
    if (!userId) return;
    const m = members.find((x) => x.id === userId);
    if (m) addOne(target, { user_id: m.id, name: m.name, email: m.email });
  }
  async function addTeam(target: Target, teamId: string | undefined) {
    if (!teamId) return;
    try {
      const team = await api.get<Team>(`/v1/teams/${teamId}`);
      (team.members ?? [])
        .filter((tm) => !tm.archived)
        .forEach((tm) =>
          addOne(target, { user_id: tm.id, name: tm.name, email: tm.email }),
        );
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.error.loadTeamMembers"));
    }
  }
  function removePerson(target: Target, userId: string) {
    if (target === "rotation")
      rotationHosts = rotationHosts.filter((h) => h.user_id !== userId);
    else togetherHosts = togetherHosts.filter((h) => h.user_id !== userId);
  }
  function setOptional(userId: string, optional: boolean) {
    togetherHosts = togetherHosts.map((h) =>
      h.user_id === userId ? { ...h, optional } : h,
    );
  }
  // Reorder the rotation pool (priority = list position; index 0 is highest).
  function moveRotation(idx: number, dir: -1 | 1) {
    const list = [...rotationHosts];
    const j = idx + dir;
    if (j < 0 || j >= list.length) return;
    [list[idx], list[j]] = [list[j], list[idx]];
    rotationHosts = list;
  }

  // Notification / messaging state
  const REMINDER_OPTIONS = $derived([
    { value: 1, label: $t("eventTypeEditor.reminders.option.1h") },
    { value: 2, label: $t("eventTypeEditor.reminders.option.2h") },
    { value: 4, label: $t("eventTypeEditor.reminders.option.4h") },
    { value: 8, label: $t("eventTypeEditor.reminders.option.8h") },
    { value: 12, label: $t("eventTypeEditor.reminders.option.12h") },
    { value: 24, label: $t("eventTypeEditor.reminders.option.24h") },
    { value: 48, label: $t("eventTypeEditor.reminders.option.48h") },
    { value: 72, label: $t("eventTypeEditor.reminders.option.72h") },
    { value: 168, label: $t("eventTypeEditor.reminders.option.168h") },
  ]);
  let reminders = $state<number[]>([]);

  let msg_confirmation = $state("");
  let msg_cancellation = $state("");
  let msg_reschedule = $state("");
  let msg_reminder = $state("");
  // Assistant's opening chat line for this event type — blank keeps the built-in
  // (translated) default; set overrides it verbatim, same as the email notes above.
  let msg_greeting = $state("");
  // Optional custom subject lines (empty = built-in default subject).
  let subj_confirmation = $state("");
  let subj_cancellation = $state("");
  let subj_reschedule = $state("");
  let subj_reminder = $state("");
  let html_confirmation = $state("");
  let html_cancellation = $state("");
  let html_reschedule = $state("");
  let html_reminder = $state("");
  let emailBuilderOpen = $state(false);
  let emailBuilderKind = $state<"confirmation" | "cancellation" | "reschedule" | "reminder">("confirmation");

  const currentSnapshot = $derived(
    JSON.stringify({ form, routingMode, rrStrategy, reminders, msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder, msg_greeting, subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder, html_confirmation, html_cancellation, html_reschedule, html_reminder })
  );
  const isDirty = $derived(
    savedSnapshot !== "" && currentSnapshot !== savedSnapshot
  );

  beforeNavigate(({ cancel, to }) => {
    if (allowNav || !isDirty || !to) return;
    cancel();
    pendingNavHref = to.url.href;
    unsavedOpen = true;
  });

  async function saveAndExit() {
    await saveET();
    if (!isDirty && pendingNavHref) {
      allowNav = true;
      unsavedOpen = false;
      window.location.href = pendingNavHref;
    }
  }

  function discardAndExit() {
    allowNav = true;
    unsavedOpen = false;
    if (pendingNavHref) {
      window.location.href = pendingNavHref;
    }
  }
  // Track which message accordions are open
  let msgOpen = $state({
    confirmation: false,
    cancellation: false,
    reschedule: false,
    reminder: false,
  });
  // Track preview toggle per accordion
  let previewOpen = $state({
    confirmation: false,
    cancellation: false,
    reschedule: false,
    reminder: false,
  });
  // Test email send state per type
  type MsgKey = "confirmation" | "cancellation" | "reschedule" | "reminder";
  let testSending = $state<Partial<Record<MsgKey, boolean>>>({});
  let testSent = $state<Partial<Record<MsgKey, boolean>>>({});
  let testError = $state<Partial<Record<MsgKey, string>>>({});

  type ConnectedCalendarItem = {
    calendar_id: string;
    name: string;
    provider: string;
    account_email: string;
    is_destination: boolean;
  };
  let connectedCalendars = $state<ConnectedCalendarItem[]>([]);
  const displayCalendars = $derived.by(() => {
    const personal = connectedCalendars.filter((c) => {
      const lower = (c.name || "").toLowerCase();
      if (lower.includes("holiday") || lower === "family" || (c.calendar_id || "").includes("#")) return false;
      return c.is_destination || c.calendar_id === c.account_email || c.name === c.account_email || lower === "calendar";
    });
    return personal.length > 0
      ? personal
      : connectedCalendars.filter((c) => !(c.name || "").toLowerCase().includes("holiday") && (c.name || "").toLowerCase() !== "family");
  });
  let availSchedules = $state<AvailabilitySchedule[]>([]);

  let allowPhoneCall = $state(false);
  const slug = $page.params.slug;

  async function loadET() {
    etError = "";
    try {
      const [etRes, calRes, schedRes, rulesRes] = await Promise.all([
        api.get<EventType>(`/v1/event-types/${slug}`),
        api.get<{ items: ConnectedCalendarItem[] }>("/v1/calendar/calendars").catch(() => ({ items: [] })),
        api.get<{ items: AvailabilitySchedule[] }>("/v1/availability-schedules").catch(() => ({ items: [] })),
        api.get<{ rules: { schedule_id?: string | null; day_of_week: number; start_time: string; end_time: string }[] }>("/v1/availability").catch(() => ({ rules: [] })),
      ]);
      et = etRes;
      connectedCalendars = calRes.items ?? [];
      availSchedules = schedRes.items ?? [];
      availRules = rulesRes.rules ?? [];
      bookingFormConfig = et.booking_form_config ?? "";
      allowPhoneCall = et.allow_phone_call;
      form = {
        display_order: et.display_order,
        name: et.name,
        slug: et.slug,
        description: et.description ?? "",
        duration_minutes: et.duration_minutes,
        slot_interval_minutes: et.slot_interval_minutes,
        is_active: et.is_active,
        is_public: et.is_public,
        show_taken_slots: et.show_taken_slots ?? false,
        invite_delivery: et.invite_delivery ?? "calendar",
        location_type: et.location_type,
        location_value: et.location_value ?? "",
        buffer_before_minutes: et.buffer_before_minutes,
        buffer_after_minutes: et.buffer_after_minutes,
        min_notice_minutes: et.min_notice_minutes,
        max_future_days: et.max_future_days,
        max_active_bookings: et.max_active_bookings,
        max_per_day: et.max_per_day ?? "",
        max_per_week: et.max_per_week ?? "",
        max_per_month: et.max_per_month ?? "",
        max_per_year: et.max_per_year ?? "",
        max_duration_per_day: et.max_duration_per_day ?? "",
        max_duration_per_week: et.max_duration_per_week ?? "",
        show_first_slot_only: !!et.show_first_slot_only,
        future_limit_mode: et.future_limit_mode ?? "calendar_days",
        future_range_start: et.future_range_start ?? "",
        future_range_end: et.future_range_end ?? "",
        calendar_event_title: et.calendar_event_title || "{Event} between {Host} and {Scheduler}",
        redirect_url: et.redirect_url ?? "",
        reply_to: et.reply_to ?? "",
        disable_cancel: !!et.disable_cancel,
        disable_reschedule: !!et.disable_reschedule,
        require_cancel_reason: et.require_cancel_reason ?? "optional",
        allow_reschedule_past: !!et.allow_reschedule_past,
        hide_notes_in_calendar: !!et.hide_notes_in_calendar,
        hide_calendar_event_details: !!et.hide_calendar_event_details,
        requires_confirmation: !!et.requires_confirmation,
        confirm_mode: (et.confirm_mode as "always" | "under_notice" | "free_email") ?? "always",
        confirm_notice_minutes: et.confirm_notice_minutes ?? 30,
        unconfirmed_blocks_slots: et.unconfirmed_blocks_slots ?? true,
        require_email_verification: !!et.require_email_verification,
        allow_multiple_durations: !!et.allow_multiple_durations,
        duration_options:
          et.duration_options && et.duration_options.length > 0
            ? [...et.duration_options]
            : [et.duration_minutes || 30],
        location_options: (et.location_options ?? []).map((o) => ({
          type: o.type,
          value: o.value ?? "",
        })),
        destination_calendar_id: et.destination_calendar_id ?? "",
        schedule_id: et.schedule_id ?? "",
        optimized_slots: !!et.optimized_slots,
        team_id: et.team_id ?? "",
        price_cents: et.price_cents ?? 0,
        currency: et.currency ?? "usd",
      };
      loadTeams();
      priceMajor = ((et.price_cents ?? 0) / 100).toFixed(2);
      paidEnabled = (et.price_cents ?? 0) > 0;
      freqEnabled = !!(
        (et.max_per_day && et.max_per_day > 0) ||
        (et.max_per_week && et.max_per_week > 0) ||
        (et.max_per_month && et.max_per_month > 0) ||
        (et.max_per_year && et.max_per_year > 0)
      );
      durEnabled = !!(
        (et.max_duration_per_day && et.max_duration_per_day > 0) ||
        (et.max_duration_per_week && et.max_duration_per_week > 0)
      );
      syncFreqFromForm();
      futureLimitEnabled =
        (et.max_future_days ?? 0) > 0 ||
        et.future_limit_mode === "date_range";
      maxActiveEnabled = (et.max_active_bookings ?? 0) > 0;
      reminders = et.reminders ?? [];
      if (et.routing_mode === "round_robin") {
        hostScope = "people";
        staffing = "rotate";
      } else if (et.routing_mode === "collective") {
        hostScope = "people";
        staffing = "together";
      } else {
        hostScope = "me";
      }
      rrStrategy = (
        ["even", "priority", "soonest"].includes(et.rr_strategy ?? "")
          ? et.rr_strategy
          : "even"
      ) as Strategy;
      msg_confirmation = et.msg_confirmation ?? "";
      msg_cancellation = et.msg_cancellation ?? "";
      msg_reschedule = et.msg_reschedule ?? "";
      msg_reminder = et.msg_reminder ?? "";
      msg_greeting = et.msg_greeting ?? "";
      subj_confirmation = et.subj_confirmation ?? "";
      subj_cancellation = et.subj_cancellation ?? "";
      subj_reschedule = et.subj_reschedule ?? "";
      subj_reminder = et.subj_reminder ?? "";
      html_confirmation = et.html_confirmation ?? "";
      html_cancellation = et.html_cancellation ?? "";
      html_reschedule = et.html_reschedule ?? "";
      html_reminder = et.html_reminder ?? "";
      savedSnapshot = JSON.stringify({ form, routingMode, rrStrategy, reminders, msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder, msg_greeting, subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder, html_confirmation, html_cancellation, html_reschedule, html_reminder });
    } catch (e: any) {
      etError = e.message;
    } finally {
      etLoading = false;
    }
  }

  async function saveET() {
    if (!Number.isSafeInteger(form.display_order)) {
      toast.error(tr("eventTypeEditor.validation.directoryOrder"));
      return;
    }
    if (!form.name.trim()) {
      toast.error(tr("eventTypeEditor.validation.name"));
      return;
    }
    if (form.duration_minutes < 5) {
      toast.error(tr("eventTypeEditor.validation.duration"));
      return;
    }
    // Matches the API, which only requires a positive value. A stricter floor here would
    // make an event type configured below it via the API unsaveable from the editor -
    // including when the person is editing something else entirely.
    if (form.slot_interval_minutes < 1) {
      toast.error(tr("eventTypeEditor.validation.slotInterval"));
      return;
    }
    if (form.max_active_bookings < 0) {
      toast.error(tr("eventTypeEditor.validation.maxActive"));
      return;
    }
    const toCap = (v: number | "" | null) =>
      v === "" || v === null ? null : Number(v);
    const maxPerDay = toCap(form.max_per_day);
    const maxPerWeek = toCap(form.max_per_week);
    const maxPerMonth = toCap(form.max_per_month);
    const maxPerYear = toCap(form.max_per_year);
    const maxDurDay = toCap(form.max_duration_per_day);
    const maxDurWeek = toCap(form.max_duration_per_week);
    if (maxPerDay !== null && (!Number.isFinite(maxPerDay) || maxPerDay < 0)) {
      toast.error("Max bookings per day cannot be negative.");
      return;
    }
    if (
      maxPerWeek !== null &&
      (!Number.isFinite(maxPerWeek) || maxPerWeek < 0)
    ) {
      toast.error("Max bookings per week cannot be negative.");
      return;
    }
    if (routingMode === "round_robin" && rotationHosts.length === 0) {
      toast.error(tr("eventTypeEditor.validation.rotation"));
      return;
    }
    if (
      routingMode === "collective" &&
      !togetherHosts.some((h) => !h.optional)
    ) {
      toast.error(tr("eventTypeEditor.validation.collective"));
      return;
    }
    etSaving = true;
    try {
      const updated = await api.patch<EventType>(`/v1/event-types/${slug}`, {
        display_order: form.display_order,
        slug: form.slug.trim(),
        name: form.name.trim(),
        description: form.description.trim() || null,
        duration_minutes: Number(form.duration_minutes),
        slot_interval_minutes: Number(form.slot_interval_minutes),
        is_active: form.is_active,
        is_public: form.is_public,
        show_taken_slots: form.show_taken_slots,
        invite_delivery: form.invite_delivery,
        allow_phone_call: allowPhoneCall,
        location_type: form.location_type,
        location_value: form.location_value.trim() || null,
        buffer_before_minutes: Number(form.buffer_before_minutes),
        buffer_after_minutes: Number(form.buffer_after_minutes),
        min_notice_minutes: Number(form.min_notice_minutes),
        max_future_days: Number(form.max_future_days),
        max_active_bookings: Number(form.max_active_bookings),
        max_per_day: maxPerDay === 0 ? null : maxPerDay,
        max_per_week: maxPerWeek === 0 ? null : maxPerWeek,
        max_per_month: maxPerMonth === 0 ? null : maxPerMonth,
        max_per_year: maxPerYear === 0 ? null : maxPerYear,
        max_duration_per_day: maxDurDay === 0 ? null : maxDurDay,
        max_duration_per_week: maxDurWeek === 0 ? null : maxDurWeek,
        show_first_slot_only: form.show_first_slot_only,
        future_limit_mode: form.future_limit_mode,
        future_range_start: form.future_range_start.trim(),
        future_range_end: form.future_range_end.trim(),
        calendar_event_title: form.calendar_event_title.trim(),
        redirect_url: form.redirect_url.trim(),
        reply_to: form.reply_to.trim(),
        disable_cancel: form.disable_cancel,
        disable_reschedule: form.disable_reschedule,
        require_cancel_reason: form.require_cancel_reason,
        allow_reschedule_past: form.allow_reschedule_past,
        hide_notes_in_calendar: form.hide_notes_in_calendar,
        hide_calendar_event_details: form.hide_calendar_event_details,
        requires_confirmation: form.requires_confirmation,
        confirm_mode: form.confirm_mode,
        confirm_notice_minutes: Math.max(0, Number(form.confirm_notice_minutes) || 0),
        unconfirmed_blocks_slots: form.unconfirmed_blocks_slots,
        require_email_verification: form.require_email_verification,
        allow_multiple_durations: form.allow_multiple_durations,
        duration_options: form.allow_multiple_durations
          ? form.duration_options
          : [],
        location_options: form.location_options,
        destination_calendar_id: form.destination_calendar_id,
        schedule_id: form.schedule_id,
        optimized_slots: form.optimized_slots,
        team_id: form.team_id,
        price_cents: Math.max(0, Math.round(Number(priceMajor) * 100)) || 0,
        currency: form.currency.trim().toLowerCase() || "usd",
        routing_mode: routingMode,
        rr_strategy: rrStrategy,
        reminders,
        // Not `|| null`: this form always saves the whole page state, so a blanked
        // field must send '' to actually clear it. The API treats null as "leave
        // unchanged" (for partial-PATCH callers), which would silently keep the old
        // value here — the field would look cleared in the UI but persist server-side.
        msg_confirmation: msg_confirmation.trim(),
        msg_cancellation: msg_cancellation.trim(),
        msg_reschedule: msg_reschedule.trim(),
        msg_reminder: msg_reminder.trim(),
        msg_greeting: msg_greeting.trim(),
        subj_confirmation: subj_confirmation.trim(),
        subj_cancellation: subj_cancellation.trim(),
        subj_reschedule: subj_reschedule.trim(),
        subj_reminder: subj_reminder.trim(),
        html_confirmation: html_confirmation.trim(),
        html_cancellation: html_cancellation.trim(),
        html_reschedule: html_reschedule.trim(),
        html_reminder: html_reminder.trim(),
      });
      // A rename moves the row out from under the name this page was loaded with, so
      // every request after the PATCH has to use the one the server just confirmed.
      const effSlug = updated?.slug || slug;

      if (routingMode === "round_robin") {
        await api.put(`/v1/event-types/${effSlug}/hosts`, {
          hosts: rotationHosts.map((hh, i) => ({
            user_id: hh.user_id,
            role: "rotation",
            priority: i,
          })),
        });
      } else if (routingMode === "collective") {
        await api.put(`/v1/event-types/${effSlug}/hosts`, {
          hosts: togetherHosts.map((hh, i) => ({
            user_id: hh.user_id,
            role: hh.optional ? "optional" : "required",
            priority: i,
          })),
        });
      } else {
        // The owner, not the person saving: an admin saving someone else's single-host
        // event type would otherwise make themselves its host.
        await api.put(`/v1/event-types/${effSlug}/hosts`, {
          hosts: [
            {
              user_id: et?.owner_id ?? $currentUser?.id,
              role: "required",
              priority: 0,
            },
          ],
        });
      }
      toast.success(tr("eventTypeEditor.toast.saved"));
      savedSnapshot = JSON.stringify({ form, routingMode, rrStrategy, reminders, msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder, msg_greeting, subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder, html_confirmation, html_cancellation, html_reschedule, html_reminder });
      // The URL carries the slug, so a rename has to move the page too or a reload
      // lands on a 404. replaceState: the old address no longer resolves, and
      // leaving it in history is a back button that breaks.
      if (effSlug !== slug) {
        allowNav = true;
        await goto(`${base}/event-types/${effSlug}`, { replaceState: true });
        allowNav = false;
        return;
      }
      await loadET();
      await loadHosts();
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.toast.saveFailed"));
    } finally {
      etSaving = false;
    }
  }

  async function sendTestEmail(type: MsgKey) {
    testSending = { ...testSending, [type]: true };
    testSent = { ...testSent, [type]: false };
    testError = { ...testError, [type]: "" };
    try {
      await api.post(`/v1/event-types/${slug}/test-email`, { type });
      testSent = { ...testSent, [type]: true };
      setTimeout(() => {
        testSent = { ...testSent, [type]: false };
      }, 4000);
    } catch (e: any) {
      testError = { ...testError, [type]: e.message };
    } finally {
      testSending = { ...testSending, [type]: false };
    }
  }

  function buildPreview(type: MsgKey, note: string): string {
    const name = et?.name ?? tr("eventTypeEditor.preview.sampleEventFallback");
    const loc = et?.location_value
      ? `\n${tr("eventTypeEditor.preview.locationLine", { value: et.location_value })}`
      : "";
    const dur = et?.duration_minutes ?? 30;
    const noteBlk = note.trim() ? `\n---\n${note.trim()}\n` : "";
    const start = tr("eventTypeEditor.preview.sampleStart");
    const prev = tr("eventTypeEditor.preview.samplePrev");
    const attendee = tr("eventTypeEditor.preview.sampleAttendee");
    const host = tr("eventTypeEditor.preview.sampleHost");
    // Compute end time by adding duration to the sample 14:00 start; format via
    // the admin locale so the preview stays in step with sampleStart.
    const endTotalMin = 14 * 60 + dur;
    const endH24 = Math.floor(endTotalMin / 60) % 24;
    const endMin = endTotalMin % 60;
    const endTime = new Date(
      Date.UTC(2026, 0, 1, endH24, endMin),
    ).toLocaleTimeString(localeTag(), {
      hour: "2-digit",
      minute: "2-digit",
      hour12: localeTag() !== "fr",
      timeZone: "UTC",
    });
    const end = tr("eventTypeEditor.preview.sampleEnd", { time: endTime });

    return tr(`eventTypeEditor.preview.${type}.body`, {
      attendee,
      event: name,
      host,
      start,
      end,
      location: loc,
      note: noteBlk,
      prev,
    });
  }

  onMount(async () => {
    await loadET();
    // Editor-only data — skip for read-only hosts.
    if (et?.can_edit === false) return;
    // Connected calendar — best-effort; drives the meeting-link hint only, and only
    // for the owner's own event types (see calStatus).
    if (et?.owned !== false) {
      api
        .get<CalendarStatus>("/v1/calendar/status")
        .then((s) => (calStatus = s))
        .catch(() => {});
      api
        .get<ZoomStatus>("/v1/zoom/status")
        .then((s) => (zoomStatus = s))
        .catch(() => {});
    }
    await loadHosts();
    if (hostScope === "people") {
      loadMembers();
      loadTeams();
    }
  });

  let mintingOneTime = $state(false);
  async function copyPublicLink() {
    const url = `${window.location.origin}/book/${form.slug || slug}`;
    try {
      await navigator.clipboard.writeText(url);
      toast.success("Booking link copied to clipboard");
    } catch {
      toast.error("Unable to copy automatically");
    }
  }
  async function mintOneTimeLink() {
    if (!et || mintingOneTime) return;
    mintingOneTime = true;
    try {
      const created = await api.post<SingleUseLink>(
        `/v1/event-types/${et.id}/single-use-links`,
        {},
      );
      const url = created.url?.startsWith("http")
        ? created.url
        : `${window.location.origin}/t/${created.token}`;
      await navigator.clipboard.writeText(url);
      toast.success("1-time link copied to clipboard");
    } catch (e: any) {
      toast.error(e.message || "Failed to create 1-time link");
    } finally {
      mintingOneTime = false;
    }
  }
</script>

{#snippet hostPickers(target: Target, idPrefix: string)}
  <div class="grid grid-cols-2 gap-4">
    <div class="space-y-1.5">
      <Label for="{idPrefix}-add-member"
        >{$t("eventTypeEditor.hosts.addMember")}</Label
      >
      <Select.Root
        type="single"
        value=""
        onValueChange={(v) => addMember(target, v)}
      >
        <Select.Trigger id="{idPrefix}-add-member" class=" w-full justify-between"
          >{$t("eventTypeEditor.hosts.selectMember")}</Select.Trigger
        >
        <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
          {#if availableMembers.length > 0}
            {#each availableMembers as m}
              <Select.Item value={m.id} label={m.name}
                >{m.name} · {m.email}</Select.Item
              >
            {/each}
          {:else}
            <div class="px-2 py-1.5 text-xs text-muted-foreground">
              {$t("eventTypeEditor.hosts.noMembers")}
            </div>
          {/if}
        </Select.Content>
      </Select.Root>
    </div>
    <div class="space-y-1.5">
      <Label for="{idPrefix}-add-team"
        >{$t("eventTypeEditor.hosts.addTeam")}</Label
      >
      <Select.Root
        type="single"
        value=""
        onValueChange={(v) => addTeam(target, v)}
      >
        <Select.Trigger id="{idPrefix}-add-team" class=" w-full justify-between"
          >{$t("eventTypeEditor.hosts.selectTeam")}</Select.Trigger
        >
        <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
          {#if teams.length > 0}
            {#each teams as tm}
              <Select.Item value={tm.id} label={tm.name}
                >{tm.name} ({tm.member_count})</Select.Item
              >
            {/each}
          {:else}
            <div class="px-2 py-1.5 text-xs text-muted-foreground">
              {$t("eventTypeEditor.hosts.noTeams")}
            </div>
          {/if}
        </Select.Content>
      </Select.Root>
    </div>
  </div>
{/snippet}

<svelte:head
  ><title
    >{$t("eventTypeEditor.headTitle", { name: et?.name ?? slug ?? "" })}</title
  ></svelte:head
>
<svelte:window onkeydown={saveOnCmdS(saveET, () => !etSaving)} />

<div
  class="sticky top-0 z-20 -mx-4 mb-6 border-b bg-background/95 px-4 py-3 backdrop-blur-sm sm:-mx-6 sm:px-6 lg:-mx-8 lg:px-8"
>
  <div class="flex flex-wrap items-center justify-between gap-3">
    <div class="flex min-w-0 items-center gap-3">
      <a
        href="{base}/event-types"
        class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"><polyline points="15 18 9 12 15 6" /></svg
        >
        {$t("eventTypeEditor.back")}
      </a>
      <h1 class="truncate text-lg font-semibold tracking-tight sm:text-xl">
        {form.name || et?.name || slug}
      </h1>
    </div>

    {#if et && et.can_edit !== false}
      <div class="flex flex-wrap items-center gap-2">
        <div
          class="flex items-center gap-2 rounded-md border bg-card px-2.5 py-1"
        >
          <span class="text-xs text-muted-foreground"
            >{form.is_active ? "Active" : "Inactive"}</span
          >
          <Switch bind:checked={form.is_active} />
        </div>
        <div
          class="flex items-center gap-2 rounded-md border bg-card px-2.5 py-1"
          title="Show on your public profile directory (direct link always works while Active)"
        >
          <span class="text-xs text-muted-foreground"
            >{form.is_public ? "Public" : "Hidden"}</span
          >
          <Switch bind:checked={form.is_public} />
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onclick={() => window.open(`/book/${form.slug || slug}`, "_blank")}
          title={$t("eventTypeEditor.previewTooltip")}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            ><path
              d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"
            /><polyline points="15 3 21 3 21 9" /><line
              x1="10"
              y1="14"
              x2="21"
              y2="3"
            /></svg
          >
        </Button>
        <Button type="button" variant="outline" size="sm" onclick={copyPublicLink}
          >Copy Link</Button
        >
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={mintOneTimeLink}
          disabled={mintingOneTime || !form.is_active}
        >
          {mintingOneTime ? "Copying..." : "1-Time Link"}
        </Button>
        <Button type="button" size="sm" onclick={saveET} disabled={etSaving}>
          {etSaving ? "Saving..." : "Save"}
        </Button>
      </div>
    {/if}
  </div>
</div>

{#if etLoading}
  <p class="py-8 text-sm text-muted-foreground">{$t("common.loading")}</p>
{:else if etError}
  <p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
    {etError}
  </p>
{:else if et && et.can_edit === false}
  <!-- Read-only: the user hosts this event type but doesn't own it -->
  <div
    class="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800"
  >
    {#if et.owner_email}
      <span class="font-medium">{et.owner_name || et.owner_email}</span>
      {$t("eventTypeEditor.readOnly.banner")}
      <a
        href="mailto:{et.owner_email}?subject={encodeURIComponent(
          'Change request: ' + et.name,
        )}"
        class="font-medium underline"
        >{$t("eventTypeEditor.readOnly.messageThem")}</a
      >
      {$t("eventTypeEditor.readOnly.messageRequest")}
    {:else if et.owner_name}
      <span class="font-medium">{et.owner_name}</span>
      {$t("eventTypeEditor.readOnly.banner")}
      {$t("eventTypeEditor.readOnly.messageThem")}
      {$t("eventTypeEditor.readOnly.messageRequest")}
    {:else}
      {$t("eventTypeEditor.readOnly.managedByOwner")}
    {/if}
  </div>
  <div class="rounded-lg border bg-card p-6">
    <dl class="grid grid-cols-[140px_1fr] gap-x-4 gap-y-3 text-sm">
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.name")}
      </dt>
      <dd class="font-medium">{et.name}</dd>
      {#if et.description}<dt class="text-muted-foreground">
          {$t("eventTypeEditor.readOnly.labels.description")}
        </dt>
        <dd class="whitespace-pre-line">{et.description}</dd>{/if}
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.duration")}
      </dt>
      <dd>
        {et.duration_minutes}{$t("eventTypeEditor.readOnly.durationSuffix")}
      </dd>
      {#if et.slot_interval_minutes !== et.duration_minutes}
        <dt class="text-muted-foreground">
          {$t("eventTypeEditor.readOnly.labels.slotInterval")}
        </dt>
        <dd>
          {et.slot_interval_minutes}{$t(
            "eventTypeEditor.readOnly.durationSuffix",
          )}
        </dd>
      {/if}
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.location")}
      </dt>
      <dd>
        {LOCATION_TYPES.find((l) => l.value === et?.location_type)?.label ??
          et.location_type}{#if et.location_value}
          · {et.location_value}{/if}
      </dd>
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.routing")}
      </dt>
      <dd class="capitalize">{et.routing_mode.replace("_", " ")}</dd>
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.status")}
      </dt>
      <dd>
        {et.is_active
          ? $t("eventTypeEditor.readOnly.status.active")
          : $t("eventTypeEditor.readOnly.status.inactive")} · {et.is_public
          ? $t("eventTypeEditor.readOnly.status.listed")
          : $t("eventTypeEditor.readOnly.status.unlisted")}
      </dd>
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.bookingPage")}
      </dt>
      <dd>
        <a
          href="/book/{et.slug}"
          target="_blank"
          rel="noopener"
          class="text-primary underline">/book/{et.slug}</a
        >
      </dd>
    </dl>
  </div>
{:else}
  {#if editingForOwner}
    <div
      class="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-800/60 dark:bg-amber-950/40 dark:text-amber-200"
    >
      {$t("eventTypeEditor.adminEditing.banner", { owner: ownerLabel })}
    </div>
  {/if}

  <div
    class="grid grid-cols-1 gap-8 items-start pb-16 lg:grid-cols-[184px_minmax(360px,460px)] xl:grid-cols-[184px_minmax(360px,460px)_1fr]"
  >
    <!-- Sub-Navigation Rail (Left) -->
    <nav
      class="flex shrink-0 gap-1 overflow-x-auto pb-2 lg:flex-col lg:overflow-visible lg:pb-0"
      aria-label="Event type sections"
    >
      {#each TABS as tab, i}
        {#if i === 0 || TABS[i - 1].section !== tab.section}
          <p
            class="hidden px-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/70 lg:block {i === 0 ? 'pt-1' : 'pt-4'}"
          >
            {tab.section}
          </p>
        {/if}
        <button
          type="button"
          onclick={() => (activeTab = tab.id)}
          class="shrink-0 rounded-md px-3 py-2 text-left text-sm font-medium transition-colors {activeTab ===
          tab.id
            ? 'bg-secondary text-secondary-foreground'
            : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}"
        >
          {tab.label}
        </button>
      {/each}
    </nav>

    <!-- Focused Settings Pane (Center) -->
    <div class="min-w-0 w-full max-w-2xl">
      {#snippet infoTip(text: string)}
        <Tooltip.Provider>
          <Tooltip.Root>
            <Tooltip.Trigger
              type="button"
              class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-muted-foreground/70 hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              aria-label={text}
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="13"
                height="13"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
                ><circle cx="12" cy="12" r="10" /><path d="M12 16v-4" /><path
                  d="M12 8h.01"
                /></svg
              >
            </Tooltip.Trigger>
            <Tooltip.Content class="max-w-xs text-xs leading-relaxed"
              >{text}</Tooltip.Content
            >
          </Tooltip.Root>
        </Tooltip.Provider>
      {/snippet}

      {#if activeTab === "general"}
        <!-- General Settings -->
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("eventTypeEditor.general.section")}
          </h2>
          <div class="rounded-lg border bg-card p-4 sm:p-6">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div class="space-y-1.5 min-w-0">
                <Label for="et-name">{$t("eventTypeEditor.general.name")}</Label
                >
                <Input id="et-name" bind:value={form.name} />
              </div>
              <div class="space-y-1.5 min-w-0">
                <div class="flex items-center gap-1.5">
                  <Label for="et-display-order"
                    >{$t("eventTypeEditor.general.directoryOrder")}</Label
                  >
                  {@render infoTip(
                    $t("eventTypeEditor.general.directoryOrderHint"),
                  )}
                </div>
                <Input
                  id="et-display-order"
                  type="number"
                  step="1"
                  bind:value={form.display_order}
                />
              </div>
              <div class="space-y-1.5 sm:col-span-2 min-w-0">
                <div class="flex items-center gap-1.5">
                  <Label for="et-slug"
                    >{$t("eventTypeEditor.general.bookingLink")}</Label
                  >
                  {@render infoTip($t("eventTypeEditor.general.slugHint"))}
                </div>
                <div class="flex items-center gap-1.5">
                  <span class="text-sm text-muted-foreground whitespace-nowrap"
                    >{$t("eventTypeEditor.general.bookingLinkPrefix")}</span
                  >
                  <Input id="et-slug" class="min-w-0" bind:value={form.slug} />
                </div>
              </div>
              {#if form.allow_multiple_durations}
                <div class="sm:col-span-2 space-y-1.5 min-w-0">
                  <Label>{$t("eventTypeEditor.general.availableDurations")}</Label>
                  <div class="flex min-h-9 flex-wrap items-center gap-1.5 rounded-md border border-input bg-transparent px-2.5 py-1.5 shadow-xs dark:bg-input/30">
                    {#each form.duration_options as d}
                      <span class="inline-flex items-center gap-1 rounded bg-muted px-2 py-0.5 text-xs font-medium">
                        {d} mins
                        {#if form.duration_options.length > 1}
                          <button
                            type="button"
                            class="text-muted-foreground hover:text-foreground"
                            aria-label="Remove {d} mins"
                            onclick={() => {
                              const next = form.duration_options.filter((x) => x !== d);
                              form.duration_options = next;
                              if (!next.includes(form.duration_minutes) && next.length > 0) {
                                form.duration_minutes = next[0];
                              }
                            }}
                          >×</button>
                        {/if}
                      </span>
                    {/each}
                    <Popover.Root bind:open={durationMenuOpen}>
                      <Popover.Trigger class="inline-flex h-6 items-center gap-1 rounded px-2 text-xs text-muted-foreground hover:bg-accent hover:text-foreground">
                        + Select durations
                      </Popover.Trigger>
                      <Popover.Content align="start" class="w-44 p-1">
                        {#each DURATION_PRESETS as d}
                          {@const selected = form.duration_options.includes(d)}
                          <button
                            type="button"
                            onclick={() => {
                              if (selected) {
                                if (form.duration_options.length <= 1) return;
                                const next = form.duration_options.filter((x) => x !== d);
                                form.duration_options = next;
                                if (!next.includes(form.duration_minutes)) {
                                  form.duration_minutes = next[0];
                                }
                              } else {
                                form.duration_options = [...form.duration_options, d].sort((a, b) => a - b);
                              }
                            }}
                            class="flex w-full items-center justify-between rounded-sm px-2.5 py-1.5 text-left text-xs hover:bg-accent {selected ? 'font-medium text-foreground' : 'text-muted-foreground'}"
                          >
                            <span>{d} mins</span>
                            {#if selected}
                              <span class="text-primary">✓</span>
                            {/if}
                          </button>
                        {/each}
                      </Popover.Content>
                    </Popover.Root>
                  </div>
                </div>
                <div class="space-y-1.5 min-w-0">
                  <div class="flex items-center gap-1.5">
                    <Label for="et-dur-default"
                      >{$t("eventTypeEditor.general.defaultDuration")}</Label
                    >
                    {@render infoTip($t("eventTypeEditor.general.durationHint"))}
                  </div>
                  <Select.Root
                    type="single"
                    value={String(form.duration_minutes)}
                    onValueChange={(v) => {
                      if (v) form.duration_minutes = Number(v);
                    }}
                  >
                    <Select.Trigger
                      id="et-dur-default"
                      class="min- overflow-hidden w-full justify-between"
                    >
                      <span class="truncate">{form.duration_minutes} mins</span>
                    </Select.Trigger>
                    <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                      {#each form.duration_options as d}
                        <Select.Item value={String(d)} label="{d} mins">{d} mins</Select.Item>
                      {/each}
                    </Select.Content>
                  </Select.Root>
                </div>
              {:else}
                <div class="space-y-1.5 min-w-0">
                  <div class="flex items-center gap-1.5">
                    <Label for="et-dur"
                      >{$t("eventTypeEditor.general.duration")}</Label
                    >
                    {@render infoTip($t("eventTypeEditor.general.durationHint"))}
                  </div>
                  <Input
                    id="et-dur"
                    type="number"
                    min="5"
                    step="5"
                    bind:value={form.duration_minutes}
                  />
                </div>
              {/if}
              <div class="space-y-1.5 min-w-0">
                <div class="flex items-center gap-1.5">
                  <Label for="et-slot"
                    >{$t("eventTypeEditor.general.slotInterval")}</Label
                  >
                  {@render infoTip(
                    $t("eventTypeEditor.general.slotIntervalHint"),
                  )}
                </div>
                <Select.Root
                  type="single"
                  value={String(form.slot_interval_minutes)}
                  onValueChange={(v) => {
                    if (v) form.slot_interval_minutes = Number(v);
                  }}
                >
                  <Select.Trigger
                    id="et-slot"
                    class="min- overflow-hidden w-full justify-between"
                  >
                    <span class="truncate"
                      >{form.slot_interval_minutes === form.duration_minutes
                        ? `Use event length (${form.duration_minutes} min)`
                        : `${form.slot_interval_minutes} minutes`}</span
                    >
                  </Select.Trigger>
                  <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                    {#each INTERVAL_OPTIONS as mins}
                      <Select.Item value={String(mins)} label="{mins} minutes"
                        >{mins} minutes{mins === form.duration_minutes
                          ? " (default)"
                          : ""}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
              <div class="sm:col-span-2 flex items-center justify-between gap-4 rounded-md border p-3.5">
                <div>
                  <Label for="et-multi-dur"
                    >{$t("eventTypeEditor.general.allowMultipleDurations")}</Label
                  >
                  <p class="text-xs text-muted-foreground">
                    {$t("eventTypeEditor.general.allowMultipleDurationsHint")}
                  </p>
                </div>
                <Switch
                  id="et-multi-dur"
                  checked={form.allow_multiple_durations}
                  onCheckedChange={(checked) => {
                    form.allow_multiple_durations = checked;
                    if (checked) {
                      form.duration_options = [form.duration_minutes || 30];
                    }
                  }}
                />
              </div>
              <div class="sm:col-span-2 space-y-1.5 min-w-0">
                <Label for="et-desc"
                  >{$t("eventTypeEditor.general.description")}</Label
                >
                <Textarea
                  id="et-desc"
                  bind:value={form.description}
                  placeholder={$t(
                    "eventTypeEditor.general.descriptionPlaceholder",
                  )}
                  rows={3}
                  class="resize-y"
                />
              </div>
            </div>

            <div class="mt-4">
              <div
                class="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5"
              >
                <div class="flex items-center gap-1.5 min-w-0">
                  <Label for="show-taken" class="cursor-pointer font-normal"
                    >{$t("eventTypeEditor.general.showTaken")}</Label
                  >
                  {@render infoTip(
                    $t("eventTypeEditor.general.showTakenHint") +
                      (form.show_taken_slots
                        ? " " + $t("eventTypeEditor.general.showTakenWarning")
                        : ""),
                  )}
                </div>
                <Switch id="show-taken" bind:checked={form.show_taken_slots} />
              </div>
            </div>

            {#if isOnlineMeeting(form.location_type)}
              <div
                class="mt-4 flex items-center justify-between rounded-md border px-3 py-2.5"
              >
                <Label for="allow-phone" class="cursor-pointer font-normal"
                  >{$t("eventTypeEditor.general.allowPhoneCall")}</Label
                >
                <Switch id="allow-phone" bind:checked={allowPhoneCall} />
              </div>
            {/if}
            <!-- Location -->
            <div class="mt-6 border-t pt-5">
              <p
                class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                {$t("eventTypeEditor.location.section")}
              </p>
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <div class="space-y-1.5 min-w-0">
                  <Label for="et-loc"
                    >{$t("eventTypeEditor.location.type")}</Label
                  >
                  <Select.Root type="single" bind:value={form.location_type}>
                    <Select.Trigger
                      id="et-loc"
                      class="min- overflow-hidden w-full justify-between"
                    >
                      <span class="truncate"
                        >{LOCATION_TYPES.find(
                          (lt) => lt.value === form.location_type,
                        )?.label ?? $t("common.select")}</span
                      >
                    </Select.Trigger>
                    <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                      {#each LOCATION_TYPES as lt}
                        <Select.Item value={lt.value} label={lt.label}
                          >{lt.label}</Select.Item
                        >
                      {/each}
                    </Select.Content>
                  </Select.Root>
                </div>
                <div class="space-y-1.5 min-w-0">
                  {#if isOnlineMeeting(form.location_type)}
                    {@const platform =
                      form.location_type === "teams"
                        ? $t("eventTypeEditor.location.types.teams")
                        : $t("eventTypeEditor.location.types.googleMeet")}
                    {@const meetHint = editingForOwner
                      ? $t("eventTypeEditor.location.hint.meet.ownerEditing", {
                          platform,
                          owner: ownerLabel,
                        })
                      : meetAutoGen
                        ? $t("eventTypeEditor.location.hint.meet.autoGen", {
                            platform,
                          }) +
                          (form.location_type === "teams"
                            ? " " +
                              $t(
                                "eventTypeEditor.location.hint.meet.autoGenTeamsFallback",
                              )
                            : "")
                        : (calStatus?.connected
                            ? $t(
                                calStatus.provider === "google"
                                  ? "eventTypeEditor.location.hint.meet.manualGoogle"
                                  : "eventTypeEditor.location.hint.meet.manualMicrosoft",
                                { platform },
                              )
                            : $t(
                                "eventTypeEditor.location.hint.meet.noCalendar",
                                { platform },
                              )) +
                          " " +
                          $t(
                            "eventTypeEditor.location.hint.meet.pasteFallback",
                          )}
                    <div class="flex items-center gap-1.5">
                      <Label for="et-loc-val"
                        >{form.location_type === "teams"
                          ? $t("eventTypeEditor.location.labels.teamsLink")
                          : $t(
                              "eventTypeEditor.location.labels.meetLink",
                            )}</Label
                      >
                      {@render infoTip(meetHint)}
                    </div>
                    <Input
                      id="et-loc-val"
                      bind:value={form.location_value}
                      placeholder={meetAutoGen
                        ? $t(
                            "eventTypeEditor.location.placeholder.optionalFallback",
                          )
                        : $t("eventTypeEditor.location.placeholder.pasteMeet", {
                            platform,
                          })}
                    />
                  {:else if form.location_type === "zoom"}
                    {@const zoomHint = editingForOwner
                      ? $t("eventTypeEditor.location.hint.zoom.ownerEditing", {
                          owner: ownerLabel,
                        })
                      : zoomAutoGen
                        ? $t("eventTypeEditor.location.hint.zoom.autoGen")
                        : (zoomStatus?.configured
                            ? $t(
                                "eventTypeEditor.location.hint.zoom.connectCalendar",
                              )
                            : $t(
                                "eventTypeEditor.location.hint.zoom.notConfigured",
                              )) +
                          " " +
                          $t(
                            "eventTypeEditor.location.hint.zoom.pasteFallback",
                          )}
                    <div class="flex items-center gap-1.5">
                      <Label for="et-loc-val"
                        >{$t("eventTypeEditor.location.labels.zoomLink")}</Label
                      >
                      {@render infoTip(zoomHint)}
                    </div>
                    <Input
                      id="et-loc-val"
                      bind:value={form.location_value}
                      placeholder={zoomAutoGen
                        ? $t(
                            "eventTypeEditor.location.placeholder.optionalFallback",
                          )
                        : $t("eventTypeEditor.location.placeholder.pasteZoom")}
                    />
                  {:else if form.location_type === "livekit"}
                    <div class="flex items-center gap-1.5">
                      <Label
                        >{$t(
                          "eventTypeEditor.location.labels.videoRoom",
                        )}</Label
                      >
                      {@render infoTip(
                        $t("eventTypeEditor.location.hint.livekit"),
                      )}
                    </div>
                    <p
                      class="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground"
                    >
                      Auto-generated per booking
                    </p>
                  {:else}
                    <Label for="et-loc-val"
                      >{LOCATION_NEEDS_VALUE[form.location_type] ??
                        $t("eventTypeEditor.location.labels.details")}</Label
                    >
                    <Input
                      id="et-loc-val"
                      bind:value={form.location_value}
                      placeholder={LOCATION_PLACEHOLDER[form.location_type] ??
                        $t("eventTypeEditor.location.placeholder.optional")}
                    />
                  {/if}
                </div>
              </div>
            </div>

            <!-- Additional location options -->
            <div class="mt-6 border-t pt-5">
              <div class="space-y-2">
                <div class="flex items-center justify-between gap-2">
                  <div>
                    <Label>{$t("eventTypeEditor.location.multiOptionsTitle")}</Label>
                    <p class="text-xs text-muted-foreground">
                      {$t("eventTypeEditor.location.multiOptionsHint")}
                    </p>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onclick={() => {
                      form.location_options = [
                        ...form.location_options,
                        { type: form.location_type || "google_meet", value: form.location_value || "" },
                      ];
                    }}>+ {$t("eventTypeEditor.location.addOption")}</Button
                  >
                </div>
                {#if form.location_options.length > 0}
                  <div class="space-y-2 pt-1">
                    {#each form.location_options as opt, idx}
                      <div class="flex flex-wrap items-center gap-2">
                        <Select.Root
                          type="single"
                          value={opt.type}
                          onValueChange={(v) => {
                            if (v) {
                              form.location_options = form.location_options.map((item, i) =>
                                i === idx ? { ...item, type: v } : item
                              );
                            }
                          }}
                        >
                          <Select.Trigger class=" w-full justify-between">
                            {LOCATION_TYPES.find((lt) => lt.value === opt.type)?.label ?? opt.type}
                          </Select.Trigger>
                          <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                            {#each LOCATION_TYPES as lt}
                              <Select.Item value={lt.value} label={lt.label}>{lt.label}</Select.Item>
                            {/each}
                          </Select.Content>
                        </Select.Root>
                        <Input
                          class="flex-1 min-w-[180px]"
                          placeholder={$t("eventTypeEditor.location.optionValuePlaceholder")}
                          value={opt.value ?? ""}
                          oninput={(e) => {
                            const val = (e.currentTarget as HTMLInputElement).value;
                            form.location_options = form.location_options.map((item, i) =>
                              i === idx ? { ...item, value: val } : item
                            );
                          }}
                        />
                        <Button
                          type="button"
                          size="sm"
                          variant="ghost"
                          class="text-destructive hover:text-destructive"
                          onclick={() => {
                            form.location_options = form.location_options.filter((_, i) => i !== idx);
                          }}>{$t("common.remove")}</Button
                        >
                      </div>
                    {/each}
                  </div>
                {/if}
              </div>

              <p
                class="mb-3 mt-4 border-t pt-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                {$t("eventTypeEditor.invite.section")}
              </p>
              <div class="grid grid-cols-2 gap-4">
                <div class="space-y-1.5">
                  <Label for="et-invite"
                    >{$t("eventTypeEditor.invite.sentBy")}</Label
                  >
                  {#key inviteSelectReset}
                    <Select.Root
                      type="single"
                      value={form.invite_delivery}
                      onValueChange={requestInviteDelivery}
                    >
                      <Select.Trigger id="et-invite" class=" w-full justify-between">
                        {INVITE_DELIVERY.find(
                          (o) => o.value === form.invite_delivery,
                        )?.label ?? $t("common.select")}
                      </Select.Trigger>
                      <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                        {#each INVITE_DELIVERY as o}
                          <Select.Item value={o.value} label={o.label}
                            >{o.label}</Select.Item
                          >
                        {/each}
                      </Select.Content>
                    </Select.Root>
                  {/key}
                </div>
                <p class="self-end text-xs text-muted-foreground">
                  {#if form.invite_delivery === "calnode"}
                    <LinkedText
                      text={$t("eventTypeEditor.invite.hint.calnode")}
                      links={{
                        email: {
                          href: `${base}/settings/email`,
                          text: $t("eventTypeEditor.invite.settingsEmailLink"),
                          class: "underline",
                        },
                      }}
                    />
                  {:else if form.invite_delivery === "none"}
                    {$t("eventTypeEditor.invite.hint.none")}
                  {:else}
                    {$t("eventTypeEditor.invite.hint.calendar")}
                  {/if}
                </p>
              </div>
              <p class="mt-2 text-xs text-muted-foreground">
                {$t("eventTypeEditor.invite.appliesNewOnly")}
              </p>
              {#if form.invite_delivery === "calnode" && et?.invite_sender_ready === false}
                <p
                  class="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800"
                >
                  <LinkedText
                    text={$t("eventTypeEditor.invite.emailNotSetup")}
                    links={{
                      email: {
                        href: `${base}/settings/email`,
                        text: $t("eventTypeEditor.invite.settingsEmailLink"),
                        class: "underline",
                      },
                    }}
                  />
                </p>
              {/if}
              {#if form.invite_delivery === "none" && isOnlineLocation(form.location_type)}
                <p
                  class="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800"
                >
                  {$t("eventTypeEditor.invite.onlineNoneWarning")}
                </p>
              {/if}
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "availability"}
        {@const activeSched = form.schedule_id
          ? availSchedules.find((s) => s.id === form.schedule_id)
          : availSchedules.find((s) => s.is_default) ?? availSchedules[0]}
        {@const schedRules = availRules.filter((r) =>
          activeSched ? (!r.schedule_id && activeSched.is_default) || r.schedule_id === activeSched.id : !r.schedule_id
        )}
        {@const dayNames = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]}
        <div class="mb-8 space-y-6">
          <div class="rounded-lg border bg-card p-5">
            <div class="flex items-start justify-between gap-4">
              <div>
                <h2 class="text-sm font-semibold">Optimized slots</h2>
                <p class="mt-1 text-xs text-muted-foreground">
                  Show start times that fit tightly around your existing bookings, instead of snapping to fixed intervals like :00 and :30.
                </p>
              </div>
              <Switch bind:checked={form.optimized_slots} />
            </div>
          </div>

          <div class="rounded-lg border bg-card p-5 space-y-4">
            <div class="space-y-1.5">
              <Label>Availability</Label>
              <Select.Root
                type="single"
                value={form.schedule_id}
                onValueChange={(v) => {
                  form.schedule_id = v ?? "";
                }}
              >
                <Select.Trigger class="min- overflow-hidden w-full justify-between">
                  <span class="truncate">
                    {form.schedule_id
                      ? (availSchedules.find((s) => s.id === form.schedule_id)?.name ?? form.schedule_id)
                      : `Default schedule${activeSched ? ` (${activeSched.name})` : ""}`}
                  </span>
                </Select.Trigger>
                <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                  <Select.Item value="" label="Default schedule">Default schedule</Select.Item>
                  {#each availSchedules as sched}
                    <Select.Item value={sched.id} label={sched.name}>
                      {sched.name}{sched.is_default ? " · Default" : ""}{sched.timezone ? ` (${sched.timezone})` : ""}
                    </Select.Item>
                  {/each}
                </Select.Content>
              </Select.Root>
            </div>

            <div class="divide-y rounded-md border bg-muted/20 px-3 text-xs">
              {#each dayNames as dayName, dow}
                {@const dayRanges = schedRules.filter((r) => r.day_of_week === dow)}
                <div class="flex items-center justify-between py-2">
                  <span class="font-medium text-foreground">{dayName}</span>
                  {#if dayRanges.length === 0}
                    <span class="text-muted-foreground">Unavailable</span>
                  {:else}
                    <span class="text-muted-foreground">
                      {dayRanges.map((r) => `${r.start_time} - ${r.end_time}`).join(", ")}
                    </span>
                  {/if}
                </div>
              {/each}
            </div>

            <div class="flex items-center justify-between text-xs text-muted-foreground pt-1">
              <span>{activeSched?.timezone || "UTC"}</span>
              <a href="/availability" class="inline-flex items-center gap-1 font-medium text-foreground hover:underline">
                Edit availability ↗
              </a>
            </div>
          </div>

          <div class="rounded-lg border bg-card p-5 space-y-2">
            <Label>{$t("eventTypeEditor.location.addToCalendar")}</Label>
            <Select.Root
              type="single"
              value={form.destination_calendar_id}
              onValueChange={(v) => {
                form.destination_calendar_id = v ?? "";
              }}
            >
              <Select.Trigger class=" w-full justify-between">
                {(() => {
                  if (!form.destination_calendar_id) {
                    return $t("eventTypeEditor.location.addToCalendarDefault");
                  }
                  const match = displayCalendars.find(
                    (c) => c.calendar_id === form.destination_calendar_id
                  ) ?? connectedCalendars.find(
                    (c) => c.calendar_id === form.destination_calendar_id
                  );
                  if (!match) return form.destination_calendar_id;
                  const providerName = match.provider === "microsoft" ? "Outlook" : "Google Calendar";
                  return `${providerName} · ${match.account_email || match.name || match.calendar_id}`;
                })()}
              </Select.Trigger>
              <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                <Select.Item
                  value=""
                  label={$t("eventTypeEditor.location.addToCalendarDefault")}
                >
                  {$t("eventTypeEditor.location.addToCalendarDefault")}
                </Select.Item>
                {#each displayCalendars as cal}
                  {@const isMs = cal.provider === "microsoft"}
                  {@const calLabel = `${isMs ? "Outlook" : "Google Calendar"} · ${cal.account_email || cal.name || cal.calendar_id}`}
                  <Select.Item value={cal.calendar_id} label={calLabel}>
                    <span class="inline-flex items-center gap-2">
                      <span class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded text-[10px] font-bold {isMs ? 'bg-sky-500/20 text-sky-400' : 'bg-blue-500/20 text-blue-400'}">
                        {isMs ? "O" : "G"}
                      </span>
                      <span>{calLabel}</span>
                    </span>
                  </Select.Item>
                {/each}
              </Select.Content>
            </Select.Root>
            <p class="text-xs text-muted-foreground">
              Bookings always add to your main personal calendar; conflict checks still run across all enabled calendars.
            </p>
          </div>
        </div>
      {/if}

      {#if activeTab === "limits"}
        <!-- Limits & Buffers -->
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            Limits & buffers
          </h2>
          <div class="rounded-lg border bg-card p-4 sm:p-6 space-y-5">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div class="space-y-1.5 min-w-0">
                <Label for="et-buf-before"
                  >{$t("eventTypeEditor.scheduling.bufferBefore")}</Label
                >
                <Select.Root
                  type="single"
                  value={String(form.buffer_before_minutes)}
                  onValueChange={(v) => {
                    if (v !== undefined) form.buffer_before_minutes = Number(v);
                  }}
                >
                  <Select.Trigger id="et-buf-before" class="min- w-full justify-between"
                    >{optionLabel(
                      BUFFER_OPTIONS,
                      form.buffer_before_minutes,
                    )}</Select.Trigger
                  >
                  <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                    {#each BUFFER_OPTIONS as opt}
                      <Select.Item value={String(opt.value)} label={opt.label}
                        >{opt.label}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
              <div class="space-y-1.5 min-w-0">
                <Label for="et-buf-after"
                  >{$t("eventTypeEditor.scheduling.bufferAfter")}</Label
                >
                <Select.Root
                  type="single"
                  value={String(form.buffer_after_minutes)}
                  onValueChange={(v) => {
                    if (v !== undefined) form.buffer_after_minutes = Number(v);
                  }}
                >
                  <Select.Trigger id="et-buf-after" class="min- w-full justify-between"
                    >{optionLabel(
                      BUFFER_OPTIONS,
                      form.buffer_after_minutes,
                    )}</Select.Trigger
                  >
                  <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                    {#each BUFFER_OPTIONS as opt}
                      <Select.Item value={String(opt.value)} label={opt.label}
                        >{opt.label}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
              <div class="space-y-1.5 sm:col-span-2 min-w-0">
                <Label for="et-notice"
                  >{$t("eventTypeEditor.scheduling.minNotice")}</Label
                >
                <Select.Root
                  type="single"
                  value={String(form.min_notice_minutes)}
                  onValueChange={(v) => {
                    if (v !== undefined) form.min_notice_minutes = Number(v);
                  }}
                >
                  <Select.Trigger id="et-notice" class="min- w-full justify-between"
                    >{optionLabel(
                      NOTICE_OPTIONS,
                      form.min_notice_minutes,
                    )}</Select.Trigger
                  >
                  <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                    {#each NOTICE_OPTIONS as opt}
                      <Select.Item value={String(opt.value)} label={opt.label}
                        >{opt.label}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
            </div>

            <!-- Limit booking frequency (dynamic per-row dropdown: Per Day / Per Week) -->
            <div class="rounded-lg border p-4 space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-1.5 min-w-0">
                  <p class="text-sm font-medium">Limit booking frequency</p>
                </div>
                <Switch
                  checked={freqEnabled}
                  onCheckedChange={toggleFreqEnabled}
                />
              </div>
              {#if freqEnabled}
                <div class="space-y-2.5 pt-1">
                  {#each freqOrder as unit, idx (unit)}
                    {@const availableUnits = ALL_FREQ_UNITS.filter(
                      (u) => u === unit || !freqOrder.includes(u),
                    )}
                    <div class="flex items-center gap-2">
                      {#if unit === "day"}
                        <Input
                          type="number"
                          min="1"
                          class="w-20 shrink-0"
                          bind:value={form.max_per_day}
                          aria-label="Max bookings per day"
                        />
                      {:else if unit === "week"}
                        <Input
                          type="number"
                          min="1"
                          class="w-20 shrink-0"
                          bind:value={form.max_per_week}
                          aria-label="Max bookings per week"
                        />
                      {:else if unit === "month"}
                        <Input
                          type="number"
                          min="1"
                          class="w-20 shrink-0"
                          bind:value={form.max_per_month}
                          aria-label="Max bookings per month"
                        />
                      {:else}
                        <Input
                          type="number"
                          min="1"
                          class="w-20 shrink-0"
                          bind:value={form.max_per_year}
                          aria-label="Max bookings per year"
                        />
                      {/if}
                      <Select.Root
                        type="single"
                        value={unit}
                        onValueChange={(v) => {
                          if (v) setFreqRowUnit(idx, v as FreqUnit);
                        }}
                      >
                        <Select.Trigger
                          class="min- w-full justify-between"
                          aria-label="Frequency period"
                        >
                          {freqLabel(unit)}
                        </Select.Trigger>
                        <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                          {#each availableUnits as opt}
                            <Select.Item
                              value={opt}
                              label={freqLabel(opt)}
                            >
                              {freqLabel(opt)}
                            </Select.Item>
                          {/each}
                        </Select.Content>
                      </Select.Root>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        class="shrink-0 text-destructive hover:text-destructive"
                        onclick={() => removeFreqRow(unit)}
                        aria-label="Remove limit"
                      >
                        <svg
                          xmlns="http://www.w3.org/2000/svg"
                          width="15"
                          height="15"
                          viewBox="0 0 24 24"
                          fill="none"
                          stroke="currentColor"
                          stroke-width="2"
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          ><polyline points="3 6 5 6 21 6" /><path
                            d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"
                          /><path d="M10 11v6" /><path d="M14 11v6" /></svg
                        >
                      </Button>
                    </div>
                  {/each}
                  {#if freqOrder.length < ALL_FREQ_UNITS.length}
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      class="h-8 px-2 text-xs"
                      onclick={addFreqRow}
                    >
                      + Add limit
                    </Button>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- Limit total booking duration -->
            <div class="rounded-lg border p-4 space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-1.5 min-w-0">
                  <p class="text-sm font-medium">{$t("eventTypeEditor.limits.durationTitle")}</p>
                </div>
                <Switch
                  checked={durEnabled}
                  onCheckedChange={toggleDurEnabled}
                />
              </div>
              {#if durEnabled}
                <div class="space-y-2 pt-1">
                  {#each durOrder as unit, idx (unit)}
                    {@const availDur = (["day", "week"] as const).filter(
                      (u) => u === unit || !durOrder.includes(u),
                    )}
                    <div class="flex items-center gap-2">
                      {#if unit === "day"}
                        <Input
                          type="number"
                          min="5"
                          step="5"
                          class="w-24 shrink-0"
                          bind:value={form.max_duration_per_day}
                          aria-label="Max minutes per day"
                        />
                      {:else}
                        <Input
                          type="number"
                          min="5"
                          step="5"
                          class="w-24 shrink-0"
                          bind:value={form.max_duration_per_week}
                          aria-label="Max minutes per week"
                        />
                      {/if}
                      <span class="text-xs text-muted-foreground shrink-0">{$t("eventTypeEditor.limits.minutes")}</span>
                      <Select.Root
                        type="single"
                        value={unit}
                        onValueChange={(v) => {
                          if (v) setDurRowUnit(idx, v as "day" | "week");
                        }}
                      >
                        <Select.Trigger class="min- w-full justify-between" aria-label="Duration period">
                          {unit === "day" ? $t("eventTypeEditor.limits.perDay") : $t("eventTypeEditor.limits.perWeek")}
                        </Select.Trigger>
                        <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                          {#each availDur as opt}
                            <Select.Item
                              value={opt}
                              label={opt === "day" ? $t("eventTypeEditor.limits.perDay") : $t("eventTypeEditor.limits.perWeek")}
                            >
                              {opt === "day" ? $t("eventTypeEditor.limits.perDay") : $t("eventTypeEditor.limits.perWeek")}
                            </Select.Item>
                          {/each}
                        </Select.Content>
                      </Select.Root>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        class="shrink-0 text-destructive hover:text-destructive"
                        onclick={() => removeDurRow(unit)}
                        aria-label="Remove duration limit"
                      >
                        <svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6" /><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" /><path d="M10 11v6" /><path d="M14 11v6" /></svg>
                      </Button>
                    </div>
                  {/each}
                  {#if durOrder.length < 2}
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      class="h-8 px-2 text-xs"
                      onclick={addDurRow}
                    >
                      + Add limit
                    </Button>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- Limit future bookings -->
            <div class="rounded-lg border p-4 space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-1.5 min-w-0">
                  <p class="text-sm font-medium">Limit future bookings</p>
                </div>
                <Switch
                  checked={futureLimitEnabled}
                  onCheckedChange={toggleFutureLimit}
                />
              </div>
              {#if futureLimitEnabled}
                <div class="space-y-3 pt-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <Input
                      id="et-future"
                      type="number"
                      min="1"
                      class="w-24 shrink-0"
                      disabled={form.future_limit_mode === "date_range"}
                      bind:value={form.max_future_days}
                    />
                    <Select.Root
                      type="single"
                      value={form.future_limit_mode}
                      onValueChange={(v) => {
                        if (v) form.future_limit_mode = v as typeof form.future_limit_mode;
                      }}
                    >
                      <Select.Trigger class="flex-1 min-w-[200px] w-full justify-between">
                        {form.future_limit_mode === "business_days"
                          ? $t("eventTypeEditor.limits.businessDays")
                          : form.future_limit_mode === "date_range"
                            ? $t("eventTypeEditor.limits.dateRange")
                            : $t("eventTypeEditor.limits.calendarDays")}
                      </Select.Trigger>
                      <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                        <Select.Item
                          value="calendar_days"
                          label={$t("eventTypeEditor.limits.calendarDays")}
                          >{$t("eventTypeEditor.limits.calendarDays")}</Select.Item
                        >
                        <Select.Item
                          value="business_days"
                          label={$t("eventTypeEditor.limits.businessDays")}
                          >{$t("eventTypeEditor.limits.businessDays")}</Select.Item
                        >
                        <Select.Item
                          value="date_range"
                          label={$t("eventTypeEditor.limits.dateRange")}
                          >{$t("eventTypeEditor.limits.dateRange")}</Select.Item
                        >
                      </Select.Content>
                    </Select.Root>
                  </div>
                  {#if form.future_limit_mode === "date_range"}
                    <div class="max-w-xs">
                      <DateRangePicker
                        bind:start={form.future_range_start}
                        bind:end={form.future_range_end}
                        placeholder="Select date range"
                      />
                    </div>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- Show only the first available slot each day -->
            <div class="rounded-lg border p-4">
              <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-1.5 min-w-0">
                  <p class="text-sm font-medium">{$t("eventTypeEditor.limits.firstSlotTitle")}</p>
                  {@render infoTip($t("eventTypeEditor.limits.firstSlotHint"))}
                </div>
                <Switch bind:checked={form.show_first_slot_only} />
              </div>
            </div>

            <!-- Limit upcoming bookings per booker -->
            <div class="rounded-lg border p-4 space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-1.5 min-w-0">
                  <p class="text-sm font-medium">
                    Limit upcoming bookings per booker
                  </p>
                </div>
                <Switch
                  checked={maxActiveEnabled}
                  onCheckedChange={toggleMaxActive}
                />
              </div>
              {#if maxActiveEnabled}
                <div class="flex items-center gap-2 pt-1">
                  <Input
                    id="et-max-active"
                    type="number"
                    min="1"
                    class="w-24"
                    bind:value={form.max_active_bookings}
                  />
                  <span class="text-sm text-muted-foreground"
                    >active bookings</span
                  >
                </div>
              {/if}
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "reschedule"}
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            Reschedule &amp; cancel
          </h2>
          <div class="space-y-4 rounded-lg border bg-card p-6">
            <div class="flex items-center justify-between gap-4">
              <div>
                <Label>{$t("eventTypeEditor.policies.disableCancel")}</Label>
                <p class="text-xs text-muted-foreground">
                  {$t("eventTypeEditor.policies.disableCancelHint")}
                </p>
              </div>
              <Switch bind:checked={form.disable_cancel} />
            </div>

            {#if !form.disable_cancel}
              {@const cancelReasonRequired =
                form.require_cancel_reason === "required" ||
                form.require_cancel_reason === "both" ||
                form.require_cancel_reason === "host_only" ||
                form.require_cancel_reason === "attendee_only"}
              {@const cancelReasonSelection =
                form.require_cancel_reason === "required"
                  ? "attendee_only"
                  : form.require_cancel_reason}
              <div class="space-y-3 border-t pt-4">
                <div class="flex items-center justify-between gap-4">
                  <div>
                    <Label for="et-cancel-reason-toggle"
                      >{$t("eventTypeEditor.policies.cancelReason")}</Label
                    >
                    <p class="text-xs text-muted-foreground">
                      {$t("eventTypeEditor.policies.cancelReasonHint")}
                    </p>
                  </div>
                  <Switch
                    id="et-cancel-reason-toggle"
                    checked={cancelReasonRequired}
                    onCheckedChange={(checked) => {
                      form.require_cancel_reason = checked ? "both" : "optional";
                    }}
                  />
                </div>
                {#if cancelReasonRequired}
                  <Select.Root
                    type="single"
                    value={cancelReasonSelection}
                    onValueChange={(v) => {
                      if (v) form.require_cancel_reason = v as typeof form.require_cancel_reason;
                    }}
                  >
                    <Select.Trigger id="et-cancel-reason" class=" w-full justify-between">
                      {cancelReasonSelection === "both"
                        ? $t("eventTypeEditor.policies.cancelReasonBoth")
                        : cancelReasonSelection === "host_only"
                          ? $t("eventTypeEditor.policies.cancelReasonHostOnly")
                          : $t("eventTypeEditor.policies.cancelReasonAttendeeOnly")}
                    </Select.Trigger>
                    <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                      <Select.Item
                        value="both"
                        label={$t("eventTypeEditor.policies.cancelReasonBoth")}
                        >{$t("eventTypeEditor.policies.cancelReasonBoth")}</Select.Item
                      >
                      <Select.Item
                        value="host_only"
                        label={$t("eventTypeEditor.policies.cancelReasonHostOnly")}
                        >{$t("eventTypeEditor.policies.cancelReasonHostOnly")}</Select.Item
                      >
                      <Select.Item
                        value="attendee_only"
                        label={$t(
                          "eventTypeEditor.policies.cancelReasonAttendeeOnly",
                        )}
                        >{$t(
                          "eventTypeEditor.policies.cancelReasonAttendeeOnly",
                        )}</Select.Item
                      >
                    </Select.Content>
                  </Select.Root>
                {/if}
              </div>
            {/if}

            <div class="flex items-center justify-between gap-4 border-t pt-4">
              <div>
                <Label>{$t("eventTypeEditor.policies.disableReschedule")}</Label
                >
                <p class="text-xs text-muted-foreground">
                  {$t("eventTypeEditor.policies.disableRescheduleHint")}
                </p>
              </div>
              <Switch bind:checked={form.disable_reschedule} />
            </div>

            {#if !form.disable_reschedule}
              <div class="flex items-center justify-between gap-4 border-t pt-4">
                <div>
                  <Label
                    >{$t("eventTypeEditor.policies.allowReschedulePast")}</Label
                  >
                  <p class="text-xs text-muted-foreground">
                    {$t("eventTypeEditor.policies.allowReschedulePastHint")}
                  </p>
                </div>
                <Switch bind:checked={form.allow_reschedule_past} />
              </div>
            {/if}
          </div>
        </div>
      {/if}

      {#if activeTab === "privacy"}
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("eventTypeEditor.privacy.title")}
          </h2>
          <div class="space-y-4 rounded-lg border bg-card p-6">
            <div class="space-y-3">
              <div class="flex items-center justify-between gap-4">
                <div>
                  <Label for="et-req-confirm"
                    >{$t("eventTypeEditor.privacy.requiresConfirmation")}</Label
                  >
                  <p class="text-xs text-muted-foreground">
                    {$t("eventTypeEditor.privacy.requiresConfirmationHint")}
                  </p>
                </div>
                <Switch
                  id="et-req-confirm"
                  bind:checked={form.requires_confirmation}
                />
              </div>

              {#if form.requires_confirmation}
                <div class="space-y-3 rounded-md border bg-muted/20 p-4">
                  <div class="space-y-1.5">
                    <Label for="et-confirm-mode"
                      >{$t("eventTypeEditor.privacy.confirmModeLabel")}</Label
                    >
                    <Select.Root
                      type="single"
                      value={form.confirm_mode}
                      onValueChange={(v) => {
                        if (v) form.confirm_mode = v as typeof form.confirm_mode;
                      }}
                    >
                      <Select.Trigger id="et-confirm-mode" class=" w-full justify-between">
                        {form.confirm_mode === "under_notice"
                          ? $t("eventTypeEditor.privacy.confirmModeUnderNotice")
                          : form.confirm_mode === "free_email"
                            ? $t("eventTypeEditor.privacy.confirmModeFreeEmail")
                            : $t("eventTypeEditor.privacy.confirmModeAlways")}
                      </Select.Trigger>
                      <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                        <Select.Item
                          value="always"
                          label={$t("eventTypeEditor.privacy.confirmModeAlways")}
                          >{$t("eventTypeEditor.privacy.confirmModeAlways")}</Select.Item
                        >
                        <Select.Item
                          value="under_notice"
                          label={$t(
                            "eventTypeEditor.privacy.confirmModeUnderNotice",
                          )}
                          >{$t(
                            "eventTypeEditor.privacy.confirmModeUnderNotice",
                          )}</Select.Item
                        >
                        <Select.Item
                          value="free_email"
                          label={$t(
                            "eventTypeEditor.privacy.confirmModeFreeEmail",
                          )}
                          >{$t(
                            "eventTypeEditor.privacy.confirmModeFreeEmail",
                          )}</Select.Item
                        >
                      </Select.Content>
                    </Select.Root>
                  </div>

                  {#if form.confirm_mode === "under_notice"}
                    <div class="flex items-center gap-2">
                      <Input
                        id="et-confirm-notice"
                        type="number"
                        min="1"
                        class="w-24"
                        bind:value={form.confirm_notice_minutes}
                      />
                      <span class="text-sm text-muted-foreground"
                        >{$t("eventTypeEditor.privacy.confirmNoticeUnit")}</span
                      >
                    </div>
                  {/if}

                  <div class="flex items-center justify-between gap-4 pt-1">
                    <Label for="et-unconfirmed-blocks" class="font-normal">
                      {$t("eventTypeEditor.privacy.unconfirmedBlocksSlots")}
                    </Label>
                    <Switch
                      id="et-unconfirmed-blocks"
                      bind:checked={form.unconfirmed_blocks_slots}
                    />
                  </div>
                </div>
              {/if}
            </div>

            <div class="flex items-center justify-between gap-4 border-t pt-4">
              <div>
                <Label for="et-email-verify"
                  >{$t("eventTypeEditor.privacy.requireEmailVerification")}</Label
                >
                <p class="text-xs text-muted-foreground">
                  {$t("eventTypeEditor.privacy.requireEmailVerificationHint")}
                </p>
              </div>
              <Switch
                id="et-email-verify"
                bind:checked={form.require_email_verification}
              />
            </div>

            <div class="flex items-center justify-between gap-4 border-t pt-4">
              <div>
                <Label for="et-hide-notes"
                  >{$t("eventTypeEditor.privacy.hideNotesInCalendar")}</Label
                >
                <p class="text-xs text-muted-foreground">
                  {$t("eventTypeEditor.privacy.hideNotesInCalendarHint")}
                </p>
              </div>
              <Switch
                id="et-hide-notes"
                bind:checked={form.hide_notes_in_calendar}
              />
            </div>

            <div class="flex items-center justify-between gap-4 border-t pt-4">
              <div>
                <Label for="et-hide-cal-details"
                  >{$t("eventTypeEditor.privacy.hideCalendarEventDetails")}</Label
                >
                <p class="text-xs text-muted-foreground">
                  {$t("eventTypeEditor.privacy.hideCalendarEventDetailsHint")}
                </p>
              </div>
              <Switch
                id="et-hide-cal-details"
                bind:checked={form.hide_calendar_event_details}
              />
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "hosts"}
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("eventTypeEditor.tabs.hosts")}
          </h2>
          <div class="rounded-lg border bg-card p-6 space-y-5">
            {#if teams.length > 0}
              <div class="space-y-1.5">
                <Label for="et-team-assign">Team assignment</Label>
                <Select.Root type="single" value={form.team_id || "__personal__"} onValueChange={(v) => { if (!v) return; form.team_id = v === "__personal__" ? "" : v; if (form.team_id) setScope("people"); }}>
                  <Select.Trigger id="et-team-assign" class="w-full justify-between">{teams.find((tm) => tm.id === form.team_id)?.name ?? "Personal (no team)"}</Select.Trigger>
                  <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                    <Select.Item value="__personal__" label="Personal (no team)">Personal (no team)</Select.Item>
                    {#each teams.filter((tm) => $currentUser?.is_admin || tm.my_role === "owner") as tm (tm.id)}
                      <Select.Item value={tm.id} label={tm.name}>{tm.name}</Select.Item>
                    {/each}
                  </Select.Content>
                </Select.Root>
                <p class="text-xs text-muted-foreground">
                  Team event types appear on the team's booking page and must use Collective or Round-robin scheduling.
                </p>
              </div>
            {/if}
            <div>
              <!-- Q1 — Hosting -->
              <div class="space-y-1.5">
                <Label>Hosting</Label>
                <div class="inline-flex rounded-lg border bg-muted/40 p-0.5">
                  <button
                    type="button"
                    disabled={!!form.team_id}
                    onclick={() => setScope("me")}
                    class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors disabled:opacity-40 {hostScope ===
                    'me'
                      ? 'bg-background shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'}"
                  >
                    {editingForOwner
                      ? $t("eventTypeEditor.hosts.justOwner", {
                          owner: ownerLabel,
                        })
                      : "Just me"}
                  </button>
                  <button
                    type="button"
                    onclick={() => setScope("people")}
                    class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {hostScope ===
                    'people'
                      ? 'bg-background shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'}"
                  >
                    Multiple hosts
                  </button>
                </div>
              </div>

              {#if hostScope === "people"}
                <!-- Q2 — Assignment -->
                <div class="mt-4 space-y-1.5">
                  <Label>Assignment</Label>
                  <div class="inline-flex rounded-lg border bg-muted/40 p-0.5">
                    <button
                      type="button"
                      onclick={() => (staffing = "rotate")}
                      class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {staffing ===
                      'rotate'
                        ? 'bg-background shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'}"
                    >
                      Round robin
                    </button>
                    <button
                      type="button"
                      onclick={() => (staffing = "together")}
                      class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {staffing ===
                      'together'
                        ? 'bg-background shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'}"
                    >
                      Collective (all attend)
                    </button>
                  </div>
                </div>

                {#if staffing === "rotate"}
                  <div class="mt-4 space-y-3">
                    <div class="space-y-1.5">
                      <Label for="rr-strategy">Round-robin method</Label>
                      <Select.Root
                        type="single"
                        value={rrStrategy}
                        onValueChange={(v) => {
                          if (v) rrStrategy = v as Strategy;
                        }}
                      >
                        <Select.Trigger id="rr-strategy" class=" w-full justify-between">
                          {RR_STRATEGIES.find((s) => s.value === rrStrategy)
                            ?.label ?? $t("common.select")}
                        </Select.Trigger>
                        <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                          {#each RR_STRATEGIES as s}
                            <Select.Item value={s.value} label={s.label}
                              >{s.label}</Select.Item
                            >
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>

                    <p class="text-sm font-medium">Team members</p>
                    {#if rotationHosts.length > 0}
                      <div class="space-y-2">
                        {#each rotationHosts as h, i (h.user_id)}
                          <div
                            class="flex items-center justify-between gap-2 rounded-md border px-3 py-2"
                          >
                            <div class="flex min-w-0 items-center gap-2">
                              {#if rrStrategy === "priority"}
                                <span
                                  class="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground tabular-nums"
                                  >{i + 1}</span
                                >
                              {/if}
                              <div class="min-w-0">
                                <div class="truncate text-sm font-medium">
                                  {h.name}
                                </div>
                                <div
                                  class="truncate text-xs text-muted-foreground"
                                >
                                  {h.email}
                                </div>
                              </div>
                            </div>
                            <div class="flex shrink-0 items-center gap-1">
                              {#if rrStrategy === "priority"}
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="icon"
                                  disabled={i === 0}
                                  aria-label={$t(
                                    "eventTypeEditor.hosts.moveUp",
                                  )}
                                  onclick={() => moveRotation(i, -1)}
                                >
                                  <svg
                                    xmlns="http://www.w3.org/2000/svg"
                                    width="16"
                                    height="16"
                                    viewBox="0 0 24 24"
                                    fill="none"
                                    stroke="currentColor"
                                    stroke-width="2"
                                    stroke-linecap="round"
                                    stroke-linejoin="round"
                                    ><polyline points="18 15 12 9 6 15" /></svg
                                  >
                                </Button>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="icon"
                                  disabled={i === rotationHosts.length - 1}
                                  aria-label={$t(
                                    "eventTypeEditor.hosts.moveDown",
                                  )}
                                  onclick={() => moveRotation(i, 1)}
                                >
                                  <svg
                                    xmlns="http://www.w3.org/2000/svg"
                                    width="16"
                                    height="16"
                                    viewBox="0 0 24 24"
                                    fill="none"
                                    stroke="currentColor"
                                    stroke-width="2"
                                    stroke-linecap="round"
                                    stroke-linejoin="round"
                                    ><polyline points="6 9 12 15 18 9" /></svg
                                  >
                                </Button>
                              {/if}
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onclick={() =>
                                  removePerson("rotation", h.user_id)}
                                >{$t("common.remove")}</Button
                              >
                            </div>
                          </div>
                        {/each}
                      </div>
                    {:else}
                      <p class="text-xs text-muted-foreground">
                        {$t("eventTypeEditor.hosts.rotationEmpty")}
                      </p>
                    {/if}
                    {@render hostPickers("rotation", "rr")}
                  </div>
                {:else}
                  <div class="mt-4 space-y-3">
                    <p class="text-sm font-medium">
                      {$t("eventTypeEditor.hosts.whoAttends")}
                    </p>
                    {#if togetherHosts.length > 0}
                      <div class="space-y-2">
                        {#each togetherHosts as h (h.user_id)}
                          <div
                            class="flex items-center justify-between gap-2 rounded-md border px-3 py-2"
                          >
                            <div class="min-w-0">
                              <div class="truncate text-sm font-medium">
                                {h.name}
                              </div>
                              <div
                                class="truncate text-xs text-muted-foreground"
                              >
                                {h.email}
                              </div>
                            </div>
                            <div class="flex shrink-0 items-center gap-2">
                              <div class="inline-flex rounded-md border p-0.5">
                                <button
                                  type="button"
                                  onclick={() => setOptional(h.user_id, false)}
                                  class="rounded px-2 py-0.5 text-xs font-medium transition-colors {!h.optional
                                    ? 'bg-secondary text-secondary-foreground'
                                    : 'text-muted-foreground hover:text-foreground'}"
                                >
                                  {$t("eventTypeEditor.hosts.required")}
                                </button>
                                <button
                                  type="button"
                                  onclick={() => setOptional(h.user_id, true)}
                                  class="rounded px-2 py-0.5 text-xs font-medium transition-colors {h.optional
                                    ? 'bg-secondary text-secondary-foreground'
                                    : 'text-muted-foreground hover:text-foreground'}"
                                >
                                  {$t("eventTypeEditor.hosts.optional")}
                                </button>
                              </div>
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onclick={() =>
                                  removePerson("together", h.user_id)}
                                >{$t("common.remove")}</Button
                              >
                            </div>
                          </div>
                        {/each}
                      </div>
                    {:else}
                      <p class="text-xs text-muted-foreground">
                        {$t("eventTypeEditor.hosts.togetherEmpty")}
                      </p>
                    {/if}
                    <p class="text-xs text-muted-foreground">
                      {$t("eventTypeEditor.hosts.requiredExplain")}
                      {$t("eventTypeEditor.hosts.optionalExplain")}
                    </p>
                    {@render hostPickers("together", "grp")}
                  </div>
                {/if}
              {/if}
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "confirmation"}
        <!-- Notifications -->
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("eventTypeEditor.tabs.notifications")}
          </h2>
          <div class="rounded-lg border bg-card p-6 space-y-6">
            <!-- Reminders -->
            <div>
              <div class="mb-3 flex items-center gap-1.5">
                <p class="text-sm font-medium">
                  {$t("eventTypeEditor.notifications.reminders")}
                </p>
                {@render infoTip(
                  $t("eventTypeEditor.notifications.remindersHint"),
                )}
              </div>
              <div class="space-y-2">
                {#each reminders as hb, i}
                  <div class="flex items-center gap-2">
                    <Select.Root
                      type="single"
                      value={String(hb)}
                      onValueChange={(v) => {
                        if (!v) return;
                        const n = Number(v);
                        reminders = reminders.map((r, idx) =>
                          idx === i ? n : r,
                        );
                      }}
                    >
                      <Select.Trigger class=" w-full justify-between">
                        {REMINDER_OPTIONS.find((o) => o.value === hb)?.label ??
                          $t("common.select")}
                      </Select.Trigger>
                      <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
                        {#each REMINDER_OPTIONS as opt}
                          <Select.Item
                            value={String(opt.value)}
                            label={opt.label}>{opt.label}</Select.Item
                          >
                        {/each}
                      </Select.Content>
                    </Select.Root>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onclick={() => {
                        reminders = reminders.filter((_, idx) => idx !== i);
                      }}
                    >
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="16"
                        height="16"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        ><line x1="18" y1="6" x2="6" y2="18" /><line
                          x1="6"
                          y1="6"
                          x2="18"
                          y2="18"
                        /></svg
                      >
                    </Button>
                  </div>
                {/each}
                {#if reminders.length < 5}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onclick={() => {
                      const used = new Set(reminders);
                      const next = REMINDER_OPTIONS.find(
                        (o) => !used.has(o.value),
                      );
                      if (next) reminders = [...reminders, next.value];
                    }}
                  >
                    {$t("eventTypeEditor.notifications.addReminder")}
                  </Button>
                {/if}
              </div>
            </div>

            <!-- Custom messages -->
            <div class="border-t pt-5">
              <div class="mb-3 flex items-center gap-1.5">
                <p class="text-sm font-medium">
                  {$t("eventTypeEditor.notifications.customMessages")}
                </p>
                {@render infoTip(
                  $t("eventTypeEditor.notifications.customMessagesHint"),
                )}
              </div>
              <div class="space-y-2">
                {#each [{ key: "confirmation" as const, label: $t("eventTypeEditor.notifications.msg.confirmation") }, { key: "cancellation" as const, label: $t("eventTypeEditor.notifications.msg.cancellation") }, { key: "reschedule" as const, label: $t("eventTypeEditor.notifications.msg.reschedule") }, { key: "reminder" as const, label: $t("eventTypeEditor.notifications.msg.reminder") }] as item}
                  <div class="rounded-md border">
                    <button
                      type="button"
                      class="flex w-full items-center justify-between px-4 py-3 text-sm font-medium hover:bg-muted/30 transition-colors"
                      onclick={() => {
                        msgOpen[item.key] = !msgOpen[item.key];
                      }}
                    >
                      <span>{item.label}</span>
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        class="transition-transform {msgOpen[item.key]
                          ? 'rotate-180'
                          : ''}"><polyline points="6 9 12 15 18 9" /></svg
                      >
                    </button>
                    {#if msgOpen[item.key]}
                      {@const note =
                        item.key === "confirmation"
                          ? msg_confirmation
                          : item.key === "cancellation"
                            ? msg_cancellation
                            : item.key === "reschedule"
                              ? msg_reschedule
                              : msg_reminder}
                      <div class="border-t px-4 pb-4 pt-3 space-y-3">
                        <div class="flex flex-wrap items-center justify-between gap-2">
                          <span class="text-[11px] text-muted-foreground">
                            {#if (item.key === "confirmation" ? html_confirmation : item.key === "cancellation" ? html_cancellation : item.key === "reschedule" ? html_reschedule : html_reminder)}
                              <span class="rounded bg-primary/15 px-1.5 py-0.5 font-medium text-primary">Custom HTML active</span>
                            {:else}
                              Using Calnode styled email template
                            {/if}
                          </span>
                          <Button
                            type="button"
                            size="sm"
                            variant="outline"
                            class="h-7 text-xs"
                            onclick={() => {
                              emailBuilderKind = item.key;
                              emailBuilderOpen = true;
                            }}
                          >
                            Open visual builder (HTML &amp; preview)
                          </Button>
                        </div>
                        <div class="space-y-1.5">
                          <Label class="text-xs text-muted-foreground"
                            >{$t(
                              "eventTypeEditor.notifications.subjectOptional",
                            )}</Label
                          >
                          {#if item.key === "confirmation"}
                            <Input
                              bind:value={subj_confirmation}
                              placeholder={$t(
                                "eventTypeEditor.notifications.subjectPlaceholder.confirmation",
                                { name: form.name },
                              )}
                            />
                          {:else if item.key === "cancellation"}
                            <Input
                              bind:value={subj_cancellation}
                              placeholder={$t(
                                "eventTypeEditor.notifications.subjectPlaceholder.cancellation",
                                { name: form.name },
                              )}
                            />
                          {:else if item.key === "reschedule"}
                            <Input
                              bind:value={subj_reschedule}
                              placeholder={$t(
                                "eventTypeEditor.notifications.subjectPlaceholder.reschedule",
                                { name: form.name },
                              )}
                            />
                          {:else if item.key === "reminder"}
                            <Input
                              bind:value={subj_reminder}
                              placeholder={$t(
                                "eventTypeEditor.notifications.subjectPlaceholder.reminder",
                                { name: form.name },
                              )}
                            />
                          {/if}
                        </div>
                        {#if item.key === "confirmation"}
                          <Textarea
                            bind:value={msg_confirmation}
                            rows={3}
                            placeholder={$t(
                              "eventTypeEditor.notifications.notePlaceholder",
                            )}
                          />
                        {:else if item.key === "cancellation"}
                          <Textarea
                            bind:value={msg_cancellation}
                            rows={3}
                            placeholder={$t(
                              "eventTypeEditor.notifications.notePlaceholder",
                            )}
                          />
                        {:else if item.key === "reschedule"}
                          <Textarea
                            bind:value={msg_reschedule}
                            rows={3}
                            placeholder={$t(
                              "eventTypeEditor.notifications.notePlaceholder",
                            )}
                          />
                        {:else if item.key === "reminder"}
                          <Textarea
                            bind:value={msg_reminder}
                            rows={3}
                            placeholder={$t(
                              "eventTypeEditor.notifications.notePlaceholder",
                            )}
                          />
                        {/if}

                        <!-- Preview toggle -->
                        <button
                          type="button"
                          class="text-xs text-muted-foreground hover:text-foreground underline-offset-2 hover:underline"
                          onclick={() => {
                            previewOpen[item.key] = !previewOpen[item.key];
                          }}
                          >{previewOpen[item.key]
                            ? $t("eventTypeEditor.notifications.hidePreview")
                            : $t(
                                "eventTypeEditor.notifications.showPreview",
                              )}</button
                        >

                        {#if previewOpen[item.key]}
                          <pre
                            class="rounded-md border bg-muted/30 px-4 py-3 text-xs leading-relaxed whitespace-pre-wrap font-mono text-muted-foreground overflow-auto max-h-64">{buildPreview(
                              item.key,
                              note,
                            )}</pre>
                        {/if}

                        <!-- Send test button -->
                        <div class="flex items-center gap-3">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={testSending[item.key]}
                            onclick={() => sendTestEmail(item.key)}
                          >
                            {testSending[item.key]
                              ? $t("eventTypeEditor.notifications.sendingTest")
                              : $t("eventTypeEditor.notifications.sendTest")}
                          </Button>
                          {#if testSent[item.key]}
                            <span class="text-xs text-green-600"
                              >{$t(
                                "eventTypeEditor.notifications.testSent",
                              )}</span
                            >
                          {/if}
                          {#if testError[item.key]}
                            <span class="text-xs text-destructive">
                              {#if testError[item.key] === "Email is not configured on this server — add SMTP settings to enable sending"}
                                <LinkedText
                                  text={$t(
                                    "eventTypeEditor.notifications.smtpNotConfigured",
                                  )}
                                  links={{
                                    smtp: {
                                      href: `${base}/settings/email`,
                                      text: $t(
                                        "eventTypeEditor.notifications.smtpNotConfiguredLink",
                                      ),
                                      class: "underline",
                                    },
                                  }}
                                />
                              {:else}
                                {testError[item.key]}
                              {/if}
                            </span>
                          {/if}
                        </div>
                      </div>
                    {/if}
                  </div>
                {/each}
              </div>
            </div>

            <!-- Assistant greeting -->
            <div class="border-t pt-5">
              <div class="mb-3 flex items-center gap-1.5">
                <p class="text-sm font-medium">
                  {$t("eventTypeEditor.notifications.assistantGreeting")}
                </p>
                {@render infoTip(
                  $t("eventTypeEditor.notifications.assistantGreetingHint"),
                )}
              </div>
              <Textarea
                bind:value={msg_greeting}
                rows={2}
                placeholder={$t(
                  "eventTypeEditor.notifications.greetingPlaceholder",
                )}
              />
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "hosts" && $currentUser?.is_admin && et?.owned !== false}
        <div class="mt-6 space-y-3 border-t pt-5">
          <Label for="transfer-owner"
            >{$t("eventTypeEditor.transfer.title")}</Label
          >
          <p class="text-xs text-muted-foreground">
            Reassign ownership to a saved required host (only available with no
            upcoming bookings).
          </p>
          <Select.Root
            type="single"
            value={transferOwner}
            onValueChange={(v) => (transferOwner = v ?? "")}
          >
            <Select.Trigger id="transfer-owner" disabled={transferHosts.length === 0} class="w-full justify-between">
              {transferHosts.find((h) => h.user_id === transferOwner)?.name ||
                transferHosts.find((h) => h.user_id === transferOwner)?.email ||
                $t("eventTypeEditor.transfer.select")}
            </Select.Trigger>
            <Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">
              {#each transferHosts as host}
                <Select.Item
                  value={host.user_id}
                  label={host.name || host.email}
                  >{host.name || host.email}</Select.Item
                >
              {/each}
            </Select.Content>
          </Select.Root>
          <Button
            type="button"
            variant="outline"
            disabled={!transferOwner || transferring}
            onclick={transferEvent}
            >{transferring
              ? $t("eventTypeEditor.transfer.inProgress")
              : $t("eventTypeEditor.transfer.button")}</Button
          >
        </div>
      {/if}

      {#if activeTab === "payments"}
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("settings.nav.payments")}
          </h2>
          <div class="rounded-lg border bg-card p-6 space-y-4">
            <div class="flex items-center justify-between gap-3">
              <div>
                <p class="text-sm font-medium">Paid booking</p>
                <p class="text-xs text-muted-foreground">
                  Require payment via Stripe when someone books this event.
                </p>
              </div>
              <Switch
                checked={paidEnabled}
                onCheckedChange={togglePaidEnabled}
              />
            </div>
            {#if paidEnabled}
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-4 border-t pt-4">
                <div class="space-y-1.5">
                  <Label for="et-price"
                    >{$t("eventTypeEditor.price.amount")}</Label
                  >
                  <Input
                    id="et-price"
                    type="number"
                    min="0"
                    step="0.01"
                    bind:value={priceMajor}
                    placeholder="0.00"
                  />
                </div>
                <div class="space-y-1.5">
                  <Label for="et-currency"
                    >{$t("eventTypeEditor.price.currency")}</Label
                  >
                  <Input
                    id="et-currency"
                    type="text"
                    maxlength={3}
                    bind:value={form.currency}
                    placeholder={$t(
                      "eventTypeEditor.price.currencyPlaceholder",
                    )}
                  />
                </div>
              </div>
            {/if}
            <p class="text-xs text-muted-foreground">
              <LinkedText
                text={$t("eventTypeEditor.price.hint")}
                links={{
                  stripe: {
                    href: "/admin/settings/payments",
                    text: $t("eventTypeEditor.price.stripeLink"),
                    class: "underline",
                  },
                }}
              />
            </p>
          </div>
        </div>
      {/if}

      {#if activeTab === "questions"}
        <QuestionsPanel
          slug={slug ?? ""}
          allowPhoneCall={allowPhoneCall && isOnlineMeeting(form.location_type)}
          {bookingFormConfig}
          onQuestionsChange={(items) => (previewQuestions = items)}
          onFormConfigChange={(cfg) => (bookingFormConfig = cfg)}
        />
      {/if}

      {#if activeTab === "confirmation"}
        <div class="mb-8">
          <h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">
            Confirmation
          </h2>
          <div class="rounded-lg border bg-card p-6 space-y-4">
            <h3 class="text-sm font-semibold">{$t("eventTypeEditor.overrides.title")}</h3>
            <div class="space-y-1.5">
              <div class="flex items-center justify-between gap-2">
                <div class="flex items-center gap-1.5">
                  <Label for="et-cal-title">{$t("eventTypeEditor.overrides.calendarTitle")}</Label>
                  {@render infoTip($t("eventTypeEditor.overrides.calendarTitleHint"))}
                </div>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  class="h-7 px-2 text-xs"
                  onclick={() => (calTitleModalOpen = true)}
                >
                  &#123; &#125; Variables &amp; preview
                </Button>
              </div>
              <Input
                id="et-cal-title"
                bind:value={form.calendar_event_title}
                placeholder={$t("eventTypeEditor.overrides.calendarTitlePlaceholder")}
                maxlength={200}
              />
            </div>
            <div class="space-y-1.5">
              <div class="flex items-center gap-1.5">
                <Label for="et-redirect-url">{$t("eventTypeEditor.overrides.redirectUrl")}</Label>
                {@render infoTip($t("eventTypeEditor.overrides.redirectUrlHint"))}
              </div>
              <Input
                id="et-redirect-url"
                type="url"
                bind:value={form.redirect_url}
                placeholder={$t("eventTypeEditor.overrides.redirectUrlPlaceholder")}
              />
            </div>
            <div class="space-y-1.5">
              <div class="flex items-center gap-1.5">
                <Label for="et-reply-to">{$t("eventTypeEditor.overrides.replyTo")}</Label>
                {@render infoTip($t("eventTypeEditor.overrides.replyToHint"))}
              </div>
              <Input
                id="et-reply-to"
                type="email"
                bind:value={form.reply_to}
                placeholder={$t("eventTypeEditor.overrides.replyToPlaceholder")}
              />
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "webhooks" && et}
        <WebhooksManager eventTypeId={et.id} eventTypeName={et.name} />
      {/if}

      {#if activeTab === "embed"}
        <EmbedPanel slug={et?.slug ?? ""} />
      {/if}
    </div>

    <!-- Live Interactive Booking Preview (Right, vertically centered, scales with viewport, hidden on < 1280px) -->
    <aside class="hidden xl:sticky xl:top-6 xl:flex xl:min-h-[calc(100vh-9rem)] xl:items-center xl:justify-center">
      <div class="w-full">
        <BookingPreviewCard
          name={form.name}
          description={form.description}
          duration={form.duration_minutes}
          locationType={form.location_type}
          locationValue={form.location_value}
          hostName={$currentUser?.name ?? "Host"}
          hostAvatar={$currentUser?.avatar_url ?? ""}
          mode={activeTab === "questions" ? "form" : activeTab === "confirmation" ? "confirmation" : "calendar"}
          allowPhoneCall={allowPhoneCall && isOnlineMeeting(form.location_type)}
          allowMultipleDurations={form.allow_multiple_durations}
          durationOptions={form.duration_options}
          calendarEventTitle={form.calendar_event_title}
          redirectUrl={form.redirect_url}
          questions={previewQuestions}
          {bookingFormConfig}
        />
      </div>
    </aside>
  </div>

  <EmailTemplateModal
    bind:open={emailBuilderOpen}
    slug={slug ?? ""}
    kind={emailBuilderKind}
    subject={emailBuilderKind === "confirmation" ? subj_confirmation : emailBuilderKind === "cancellation" ? subj_cancellation : emailBuilderKind === "reschedule" ? subj_reschedule : subj_reminder}
    noteBody={emailBuilderKind === "confirmation" ? msg_confirmation : emailBuilderKind === "cancellation" ? msg_cancellation : emailBuilderKind === "reschedule" ? msg_reschedule : msg_reminder}
    htmlBody={emailBuilderKind === "confirmation" ? html_confirmation : emailBuilderKind === "cancellation" ? html_cancellation : emailBuilderKind === "reschedule" ? html_reschedule : html_reminder}
    onApply={({ subject: s, noteBody: n, htmlBody: h }) => {
      if (emailBuilderKind === "confirmation") {
        subj_confirmation = s;
        msg_confirmation = n;
        html_confirmation = h;
      } else if (emailBuilderKind === "cancellation") {
        subj_cancellation = s;
        msg_cancellation = n;
        html_cancellation = h;
      } else if (emailBuilderKind === "reschedule") {
        subj_reschedule = s;
        msg_reschedule = n;
        html_reschedule = h;
      } else {
        subj_reminder = s;
        msg_reminder = n;
        html_reminder = h;
      }
    }}
  />

  <Dialog.Root bind:open={calTitleModalOpen}>
    <Dialog.Content class="max-w-lg">
      <Dialog.Header>
        <Dialog.Title>Custom event name</Dialog.Title>
        <Dialog.Description>
          Customize the title shown on calendar invites. Click any variable below to insert it.
        </Dialog.Description>
      </Dialog.Header>
      <div class="space-y-4 py-1">
        <div class="space-y-1.5">
          <Label for="modal-cal-title">Calendar event name</Label>
          <Input
            id="modal-cal-title"
            bind:value={form.calendar_event_title}
            maxlength={200}
          />
        </div>
        <div class="rounded-lg border bg-muted/20 p-3 space-y-2">
          <p class="text-xs font-semibold">Available variables</p>
          <div class="max-h-44 space-y-1 overflow-y-auto">
            {#each CAL_TITLE_VARS as v (v.token)}
              <button
                type="button"
                onclick={() => {
                  form.calendar_event_title = (form.calendar_event_title ? form.calendar_event_title + " " : "") + v.token;
                }}
                class="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-xs hover:bg-accent"
              >
                <code class="font-mono text-foreground">{v.token}</code>
                <span class="text-muted-foreground">{v.desc}</span>
              </button>
            {/each}
          </div>
        </div>
        <div class="space-y-1.5">
          <p class="text-xs font-semibold">Preview</p>
          <div class="rounded-lg border bg-background p-3">
            <div class="flex items-start gap-3">
              <span class="text-[11px] text-muted-foreground w-12 shrink-0 pt-0.5">9 AM</span>
              <div class="flex-1 rounded-md bg-foreground/90 px-3 py-2 text-background">
                <p class="text-xs font-semibold leading-snug">
                  {(form.calendar_event_title || "{Event} between {Host} and {Scheduler}")
                    .replaceAll("{Event}", form.name || "30-minute call")
                    .replaceAll("{Duration}", `${form.duration_minutes}m`)
                    .replaceAll("{Host}", $currentUser?.name || "Host")
                    .replaceAll("{HostFirst}", ($currentUser?.name || "Host").split(" ")[0])
                    .replaceAll("{Scheduler}", "Jane Doe")
                    .replaceAll("{SchedulerFirst}", "Jane")
                    .replaceAll("{Location}", LOCATION_TYPES.find((l) => l.value === form.location_type)?.label ?? "Google Meet")}
                </p>
                <p class="mt-0.5 text-[10px] opacity-75">9:00 – 9:{String(form.duration_minutes).padStart(2, "0")} AM</p>
              </div>
            </div>
          </div>
        </div>
      </div>
      <Dialog.Footer>
        <Button onclick={() => (calTitleModalOpen = false)}>Done</Button>
      </Dialog.Footer>
    </Dialog.Content>
  </Dialog.Root>

  <Dialog.Root bind:open={unsavedOpen}>
    <Dialog.Content class="max-w-md">
      <Dialog.Header>
        <Dialog.Title>Unsaved changes</Dialog.Title>
        <Dialog.Description>
          You have unsaved changes to this event type. What would you like to do before leaving?
        </Dialog.Description>
      </Dialog.Header>
      <Dialog.Footer class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button variant="ghost" onclick={() => { unsavedOpen = false; pendingNavHref = ""; }}>
          Cancel
        </Button>
        <Button variant="outline" class="text-destructive hover:text-destructive" onclick={discardAndExit}>
          Discard and exit
        </Button>
        <Button onclick={saveAndExit} disabled={etSaving}>
          {etSaving ? "Saving..." : "Save and exit"}
        </Button>
      </Dialog.Footer>
    </Dialog.Content>
  </Dialog.Root>

  <ConfirmDialog
    bind:open={confirmNoneOpen}
    title={$t("eventTypeEditor.confirmNone.title")}
    description={$t("eventTypeEditor.confirmNone.description")}
    confirmText={$t("eventTypeEditor.confirmNone.confirm")}
    cancelText={$t("common.cancel")}
    onConfirm={() => {
      form.invite_delivery = "none";
      noneAcknowledged = true;
    }}
  />
{/if}
