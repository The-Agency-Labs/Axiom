import { session } from './api';

export const auth = $state({ ready: false, username: null as string | null });

export async function refreshAuth() {
	try {
		const data = await session();
		auth.username = data.authenticated ? data.username : null;
	} catch {
		auth.username = null;
	} finally {
		auth.ready = true;
	}
}
