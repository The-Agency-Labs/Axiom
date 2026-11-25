// Theme: 'system' follows the OS, 'dark'/'light' are manual. Stored in localStorage;
// app.html applies it before first paint, this module keeps it reactive afterwards.

export type ThemePref = 'system' | 'dark' | 'light';

const read = (): ThemePref => {
	try {
		const v = localStorage.getItem('theme');
		if (v === 'dark' || v === 'light') return v;
	} catch {
		/* storage unavailable */
	}
	return 'system';
};

export const theme = $state({ pref: 'system' as ThemePref, resolved: 'dark' as 'dark' | 'light' });

function apply() {
	const light = matchMedia('(prefers-color-scheme: light)').matches;
	theme.resolved = theme.pref === 'system' ? (light ? 'light' : 'dark') : theme.pref;
	document.documentElement.dataset.theme = theme.resolved;
	document
		.querySelector('meta[name=theme-color]')
		?.setAttribute('content', theme.resolved === 'dark' ? '#09090b' : '#f6f6f8');
}

let started = false;
export function initTheme() {
	if (started) return;
	started = true;
	theme.pref = read();
	apply();
	matchMedia('(prefers-color-scheme: light)').addEventListener('change', apply);
}

export function setTheme(pref: ThemePref) {
	theme.pref = pref;
	try {
		if (pref === 'system') localStorage.removeItem('theme');
		else localStorage.setItem('theme', pref);
	} catch {
		/* storage unavailable */
	}
	apply();
}
