import { resolve } from '$app/paths';
// Resolve the configured application root in one place.
export const base = resolve('/').replace(/\/$/, '');
