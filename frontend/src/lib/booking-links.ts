import { get } from 'svelte/store';
import { currentUser } from './stores';
import type { SingleUseLink } from './api';
export function bookingLink(slug: string): string {
    return `${publicOrigin()}/book/${encodeURIComponent(slug)}`;
}
export function singleUseLink(link: Pick<SingleUseLink, 'url' | 'token'>): string {
    return /^https?:\/\//i.test(link.url || '') ? link.url : `${publicOrigin()}/t/${encodeURIComponent(link.token)}`;
}
function publicOrigin(): string {
    return (get(currentUser)?.public_base_url || window.location.origin).replace(/\/$/, '');
}
