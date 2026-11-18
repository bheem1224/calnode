<script lang="ts">
 import { onMount } from 'svelte';import { api } from '$lib/api';import { t } from '$lib/i18n';import { Button } from '$lib/components/ui/button';
 let error=$state(''),token=$state(''),busy=$state(false);
 onMount(()=>{token=new URLSearchParams(window.location.hash.slice(1)).get('token')||'';history.replaceState(null,'',window.location.pathname);});
 async function recover(){busy=true;try{await api.post('/v1/auth/recovery/claim',{token});token='';window.location.assign('/admin/auth/enroll');}catch(e){error=e instanceof Error?e.message:$t('nativeAuth.failed');}finally{busy=false;}}
</script>
<div class="flex min-h-screen items-center justify-center p-4"><div class="w-full max-w-sm space-y-4 rounded-xl border bg-card p-6"><h1 class="text-xl font-semibold">{$t('nativeAuth.recoverTitle')}</h1><p class="text-sm text-muted-foreground">{$t('nativeAuth.recoverHint')}</p>{#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}<Button type="button" disabled={busy||!token} onclick={recover}>{$t('nativeAuth.recoverStart')}</Button></div></div>
