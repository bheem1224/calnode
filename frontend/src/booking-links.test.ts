import { afterEach, expect, test } from 'vitest';
import { get } from 'svelte/store';
import { currentUser } from '$lib/stores';
import { bookingLink, singleUseLink } from '$lib/booking-links';
const original = get(currentUser);
afterEach(() => currentUser.set(original));
test('copied links use the configured public host across entry points', () => {
    currentUser.set({ ...original!, public_base_url: 'https://book.example.test/' });
    expect(bookingLink('intro')).toBe('https://book.example.test/book/intro');
    expect(singleUseLink({ token: 'opaque', url: '' })).toBe('https://book.example.test/t/opaque');
    expect(singleUseLink({ token: 'opaque', url: '/t/opaque' })).toBe('https://book.example.test/t/opaque');
    expect(singleUseLink({ token: 'opaque', url: 'https://links.example.test/t/opaque' })).toBe('https://links.example.test/t/opaque');
});
