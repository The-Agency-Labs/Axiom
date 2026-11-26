<script lang="ts">
	import { CalendarRange } from '@lucide/svelte';
	import { page } from '$app/state';

	const active = $derived(page.url.pathname === '/upcoming' || page.url.pathname.startsWith('/upcoming/'));
</script>

<!-- Phone top bar: the app mark and Upcoming. Wider screens have both in the sidebar.
     It is solid and reaches the top of the screen, so nothing scrolls past under the clock. -->
<header
	class="sticky top-0 z-20 flex h-(--topbar) items-center justify-between gap-3 border-b border-line bg-canvas px-4 pt-(--topbar-pad) sm:px-6 lg:hidden"
>
	<a href="/" class="flex items-center gap-2" aria-label="Axiom home">
		<img src="/icon.svg" alt="" class="size-7 rounded-md" />
		<span class="font-semibold tracking-tight">Axiom</span>
	</a>
	<nav aria-label="Quick links">
		<a
			href="/upcoming"
			aria-current={active ? 'page' : undefined}
			class="inline-flex min-h-9 items-center gap-2 rounded-full px-3 text-sm font-medium transition {active
				? 'bg-ink/8 text-ink'
				: 'text-muted hover:bg-ink/5 hover:text-ink'}"
		>
			<CalendarRange class="size-4" /> Upcoming
		</a>
	</nav>
</header>
