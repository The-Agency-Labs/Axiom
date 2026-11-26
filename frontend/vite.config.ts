import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// The Axiom Go server. In dev, Vite proxies API calls to it so the session
// cookie and CSRF token stay same-origin.
const backend = process.env.AXIOM_BACKEND ?? 'http://127.0.0.1:8765';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			adapter: adapter({ pages: 'build', assets: 'build', fallback: '200.html' })
		})
	],
	server: {
		port: 5173,
		// Dev only: the production build is served by the Go server itself.
		proxy: {
			'/api': { target: backend, changeOrigin: false },
			'/kosync': { target: backend, changeOrigin: false },
			'/webhook': { target: backend, changeOrigin: false },
			'/calendar/': { target: backend, changeOrigin: false }
		}
	}
});
