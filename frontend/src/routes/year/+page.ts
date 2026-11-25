import { redirect } from '@sveltejs/kit';

export function load() {
	redirect(307, `/year/${new Date().getFullYear()}`);
}
