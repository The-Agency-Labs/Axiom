<script lang="ts">
	import { Layers } from '@lucide/svelte';
	import { MEDIA } from '#lib/media.ts';
	import { saveSettings, settings } from '#lib/settings.svelte.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import type { MediaType } from '#lib/types.ts';
	import { Card, Toggle } from '#lib/ui/index.ts';

	const SORTS: [string, string][] = [
		['year', 'Year finished'],
		['activity', 'Activity'],
		['score', 'Score'],
		['title', 'Title'],
		['progress', 'Progress'],
		['start_date', 'Start date'],
		['end_date', 'End date']
	];
	const STATUSES = ['All', 'In progress', 'Planning', 'Paused', 'Dropped', 'Completed'];

	async function change(kind: MediaType, patch: { enabled?: boolean; sort?: string; status?: string }) {
		try {
			await saveSettings({ media_types: [{ kind, ...patch }] });
		} catch (e) {
			toast(e instanceof Error ? e.message : 'Could not save', { kind: 'error' });
		}
	}
</script>

<Card
	title="Media types"
	description="Turn off what you don't track; it disappears from navigation. Defaults apply when you open a library."
	icon={Layers}
	flush
>
	<ul class="divide-y divide-line">
		{#each settings.data?.media_types ?? [] as row (row.kind)}
			{@const meta = MEDIA[row.kind]}
			<li class="px-4 py-2">
				<div class="flex items-center gap-3">
					<span
						class="grid size-9 shrink-0 place-items-center rounded-xl"
						style="background: color-mix(in oklab, {meta.accent} 16%, transparent); color: {meta.accent}"
					>
						<meta.icon class="size-[18px]" />
					</span>
					<span class="min-w-0 flex-1 truncate font-medium">{meta.plural}</span>
					<Toggle
						checked={row.enabled}
						label="Show {meta.plural}"
						onchange={(v) => change(row.kind, { enabled: v })}
					/>
				</div>
				{#if row.enabled}
					<div class="mt-1 mb-2 grid grid-cols-2 gap-2">
						<label class="block">
							<span class="mb-1 block text-xs text-muted">Default sort</span>
							<select
								class="input"
								value={row.sort}
								onchange={(e) => change(row.kind, { sort: e.currentTarget.value })}
							>
								{#each SORTS as [value, label] (value)}<option {value}>{label}</option>{/each}
							</select>
						</label>
						{#if row.kind !== 'game'}<label class="block">
							<span class="mb-1 block text-xs text-muted">Default status</span>
							<select
								class="input"
								value={row.status}
								onchange={(e) => change(row.kind, { status: e.currentTarget.value })}
							>
								{#each STATUSES as value (value)}<option {value}>{value}</option>{/each}
							</select>
						</label>{/if}
					</div>
				{/if}
			</li>
		{/each}
	</ul>
</Card>
