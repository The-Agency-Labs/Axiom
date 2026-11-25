// Thin client for the Encompass JSON API (session cookie + CSRF header).

let csrfToken = '';

export class ApiError extends Error {
	constructor(
		public status: number,
		message: string
	) {
		super(message);
	}
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const headers: Record<string, string> = { Accept: 'application/json' };
	if (body !== undefined) headers['Content-Type'] = 'application/json';
	if (method !== 'GET') {
		if (!csrfToken) await session();
		headers['X-CSRFToken'] = csrfToken;
	}
	const response = await fetch(`/api/v1${path}`, {
		method,
		headers,
		credentials: 'same-origin',
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (!response.ok) {
		let detail = response.statusText;
		try {
			detail = (await response.json()).detail ?? detail;
		} catch {
			/* not JSON */
		}
		throw new ApiError(response.status, typeof detail === 'string' ? detail : 'Request failed');
	}
	return response.json() as Promise<T>;
}

export const get = <T>(path: string) => request<T>('GET', path);
export const post = <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {});
export const del = <T>(path: string) => request<T>('DELETE', path);

export interface Session {
	csrf: string;
	authenticated: boolean;
	username: string | null;
}

export async function session(): Promise<Session> {
	const data = await request<Session>('GET', '/auth/csrf');
	csrfToken = data.csrf;
	return data;
}

export async function login(username: string, password: string) {
	await session();
	const data = await request<{ username: string; csrf: string }>('POST', '/auth/login', {
		username,
		password
	});
	csrfToken = data.csrf;
	return data;
}

export async function logout() {
	await post('/auth/logout');
	csrfToken = '';
}

export function qs(params: Record<string, string | number | boolean | null | undefined>) {
	const search = new URLSearchParams();
	for (const [key, value] of Object.entries(params)) {
		if (value !== null && value !== undefined && value !== '') search.set(key, String(value));
	}
	const text = search.toString();
	return text ? `?${text}` : '';
}
