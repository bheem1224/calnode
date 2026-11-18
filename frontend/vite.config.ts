import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';
import tailwindcss from '@tailwindcss/vite';

const apiProxy = {
	target: 'http://localhost:3010',
	changeOrigin: false,
	configure: (proxy: any) => {
		proxy.on('proxyReq', (proxyReq: any, req: any) => {
			if (req.headers.host) {
				proxyReq.setHeader('host', req.headers.host);
			}
		});
	}
};

export default defineConfig({
	plugins: [tailwindcss(), sveltekit({
		preprocess: vitePreprocess(),
		adapter: adapter({ fallback: '200.html' }),
		paths: { base: '/admin' },
		alias: { $lib: './src/lib' }
	})],
	server: {
		proxy: {
			'/v1': apiProxy,
			'/book': apiProxy,
			'/t': apiProxy,
			'/manage': apiProxy,
			'/u': apiProxy,
			'/team': apiProxy,
			'/booking.css': apiProxy,
            '/phone-input.js': apiProxy,
			'/embed.js': apiProxy
		}
	}
});
