<script lang="ts">
	import { Monitor, Moon, Sun } from '@lucide/svelte';
	import { setTheme, theme, type ThemePref } from '#lib/theme.svelte.ts';

	/** Cycles system -> light -> dark. Icon button; the Settings page has the full 3-way picker. */
	const order: ThemePref[] = ['system', 'light', 'dark'];
	const icons = { system: Monitor, light: Sun, dark: Moon };
	const Icon = $derived(icons[theme.pref]);
	const next = () => setTheme(order[(order.indexOf(theme.pref) + 1) % order.length]);
</script>

<button
	type="button"
	onclick={next}
	class="grid size-11 place-items-center rounded-full text-faint transition hover:text-ink"
	title="Theme: {theme.pref}"
	aria-label="Theme: {theme.pref}. Tap to change."
>
	<Icon class="size-4" />
</button>
