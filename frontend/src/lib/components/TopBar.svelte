<script lang="ts">
	import { CalendarRange } from '@lucide/svelte';
	import { page } from '$app/state';

	const active = $derived(page.url.pathname === '/upcoming' || page.url.pathname.startsWith('/upcoming/'));
</script>

<!-- Top bar: the app mark on phones (the sidebar has it on desktop) and quick links on the right. -->
<header
	class="sticky top-[env(safe-area-inset-top)] z-20 flex h-12 items-center justify-between gap-3 border-b border-line bg-canvas/85 px-4 backdrop-blur-xl sm:px-6 lg:px-10"
>
	<a href="/" class="flex items-center gap-2 lg:invisible" aria-label="Axiom home">
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
