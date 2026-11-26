<script lang="ts">
	/**
	 * The release status of a title itself (airing, on hiatus, ended...), not the
	 * user's progress. Shown on the title page only.
	 */
	import { RELEASE_STATUS } from '#lib/media.ts';

	let { status, class: klass = '' }: { status?: string | null; class?: string } = $props();
	const info = $derived(status ? RELEASE_STATUS[status] : undefined);
</script>

{#if info}
	<span
		class="inline-flex items-center gap-1.5 rounded-full border border-line bg-surface/70 px-2 py-0.5 text-[0.75rem] font-semibold text-muted {klass}"
		title="Series status: {info.label}"
	>
		<span class="size-1.5 shrink-0 rounded-full" style="background: {info.color}" aria-hidden="true"></span>{info.label}
	</span>
{/if}
