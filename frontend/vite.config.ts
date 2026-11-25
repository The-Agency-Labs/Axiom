import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// The Django backend. In dev, Vite proxies API calls to it so the session
// cookie and CSRF token stay same-origin.
const backend = process.env.ENCOMPASS_BACKEND ?? 'http://127.0.0.1:8765';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			adapter: adapter({ pages: 'build', assets: 'build', fallback: '200.html' })
		})
	],
	server: {
		port: 5173,
		proxy: {
			'/api': { target: backend, changeOrigin: false }
		}
	}
});
