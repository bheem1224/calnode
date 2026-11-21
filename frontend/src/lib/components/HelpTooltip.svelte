<script lang="ts">
 import CircleHelp from '@lucide/svelte/icons/circle-help';
 import { buttonVariants } from '$lib/components/ui/button';
 import * as Tooltip from '$lib/components/ui/tooltip';
 import { t } from '$lib/i18n';
 let { label, text }: { label: string; text: string } = $props();
</script>

<Tooltip.Provider delayDuration={150}>
 <Tooltip.Root>
  <Tooltip.Trigger type="button" aria-label={$t('common.helpFor', { label })} class={buttonVariants({variant:'ghost',size:'icon',class:'size-5 shrink-0 rounded-full text-muted-foreground'})}>
   <CircleHelp class="size-3.5" aria-hidden="true" />
  </Tooltip.Trigger>
  <Tooltip.Content role="tooltip"><p class="max-w-xs text-xs leading-relaxed">{text}</p></Tooltip.Content>
 </Tooltip.Root>
</Tooltip.Provider>
