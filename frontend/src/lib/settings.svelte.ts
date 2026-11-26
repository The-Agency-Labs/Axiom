// The signed-in user's settings (GET /api/v1/settings), shared by the shell and pages.
// Navigation only ever offers media types the user has enabled.

import { get, patch } from './api.ts';
import { LIBRARY_TYPES } from './media.ts';
import type { MediaType } from './types.ts';

export interface MediaSetting {
	kind: MediaType;
	enabled: boolean;
	sort: string;
	status: string;
}

export interface UserSettings {
	username: string;
	is_admin: boolean;
	timezone: string;
	language: string;
	display_name: string;
	avatar: string;
	media_types: MediaSetting[];
	jellyfin_users: string;
	watched_percent: number;
	webhook_url_path: string;
	calendar_url_path: string;
}

export const settings = $state<{ data: UserSettings | null; loaded: boolean }>({ data: null, loaded: false });

const CACHE_KEY = 'encompass-settings';

/** Fetch settings; falls back to the last copy in localStorage when offline. */
export async function loadSettings(force = false) {
	if (settings.loaded && !force) return;
	try {
		settings.data = await get<UserSettings>('/settings');
		try {
			localStorage.setItem(CACHE_KEY, JSON.stringify(settings.data));
		} catch {
			/* storage unavailable */
		}
	} catch {
		try {
			const saved = localStorage.getItem(CACHE_KEY);
			if (saved && !settings.data) settings.data = JSON.parse(saved);
		} catch {
			/* nothing saved */
		}
	}
	settings.loaded = true;
}

export function resetSettings() {
	settings.data = null;
	settings.loaded = false;
}

/** PATCH settings and adopt the server's view of them. */
export async function saveSettings(change: Record<string, unknown>) {
	settings.data = await patch<UserSettings>('/settings', change);
	try {
		localStorage.setItem(CACHE_KEY, JSON.stringify(settings.data));
	} catch {
		/* storage unavailable */
	}
	return settings.data;
}

/** Is this media type switched on? (Everything is on until settings load.) */
export function isEnabled(kind: MediaType) {
	const row = settings.data?.media_types.find((m) => m.kind === kind);
	return row ? row.enabled : true;
}

/** Library sections to show in navigation, in order: only enabled media types. */
export function enabledTypes(): MediaType[] {
	return LIBRARY_TYPES.filter(isEnabled);
}

/** The user's default sort/status for a type (used by library pages). */
export function typeDefaults(kind: MediaType) {
	return settings.data?.media_types.find((m) => m.kind === kind);
}

/** What to call the user: their display name, else their username. */
export function shownName(fallback: string | null) {
	return settings.data?.display_name || fallback || '';
}
