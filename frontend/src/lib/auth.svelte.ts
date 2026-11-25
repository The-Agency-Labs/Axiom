import { isNetworkError, session } from './api.ts';

export const auth = $state({ ready: false, username: null as string | null });

const KEY = 'encompass-user';

export async function refreshAuth() {
	try {
		const data = await session();
		auth.username = data.authenticated ? data.username : null;
		try {
			if (auth.username) localStorage.setItem(KEY, auth.username);
			else localStorage.removeItem(KEY);
		} catch {
			/* storage unavailable */
		}
	} catch (e) {
		// Offline at launch: trust the last signed-in user so the cached app still opens.
		let saved: string | null = null;
		try {
			saved = localStorage.getItem(KEY);
		} catch {
			/* storage unavailable */
		}
		auth.username = isNetworkError(e) ? saved : null;
	} finally {
		auth.ready = true;
	}
}
