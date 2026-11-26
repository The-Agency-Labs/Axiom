<script lang="ts">
	/**
	 * The release status of a title itself (airing, on hiatus, ended...), not the
	 * user's progress. `onCover` renders the dark pill used over poster art.
	 */
	import { RELEASE_STATUS } from '#lib/media.ts';

	let { status, onCover = false, class: klass = '' }: { status?: string | null; onCover?: boolean; class?: string } =
		$props();
	const info = $derived(status ? RELEASE_STATUS[status] : undefined);
</script>

{#if info}
	<span
		class="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-semibold {onCover
			? 'bg-black/70 text-white backdrop-blur'
			: 'border border-line bg-surface/70 text-muted'} {klass}"
		title="Series status: {info.label}"
	>
		<span class="size-1.5 shrink-0 rounded-full" style="background: {info.color}" aria-hidden="true"></span>{info.label}
	</span>
{/if}
