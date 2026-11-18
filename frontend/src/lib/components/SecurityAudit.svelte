<script lang="ts">
 import { onMount } from 'svelte';
 import { api } from '$lib/api';
 import { t,localeTag } from '$lib/i18n';
 let items=$state<{actor_user_id:string;action:string;target_id:string;created_at:string}[]>([]),error=$state('');
 onMount(async()=>{try{items=(await api.get<{items:typeof items}>('/v1/security/audit')).items;}catch{error=$t('permissions.loadError');}});
</script>
<section class="mt-8 space-y-3">
 <h2 class="text-sm font-semibold uppercase tracking-wide text-muted-foreground">{$t('permissions.audit')}</h2>
 {#if error}<p class="text-sm text-destructive" role="alert">{error}</p>{/if}
 <div class="max-h-80 divide-y overflow-y-auto rounded-lg border bg-card">
  {#each items as entry}<div class="grid gap-1 p-3 text-xs sm:grid-cols-[10rem_1fr]"><time class="text-muted-foreground">{new Date(entry.created_at).toLocaleString(localeTag())}</time><div class="min-w-0 space-y-1"><p class="font-medium">{entry.action}</p><p class="break-all text-muted-foreground">{entry.actor_user_id} → {entry.target_id}</p></div></div>{:else}<p class="p-4 text-sm text-muted-foreground">{$t('permissions.auditEmpty')}</p>{/each}
 </div>
</section>
