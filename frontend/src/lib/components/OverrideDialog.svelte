<script lang="ts">
	import { answerOverride, override } from '#lib/override.svelte.ts';
	import { Button, Sheet } from '#lib/ui/index.ts';

	let open = $state(false);
	let name = $state('');
	$effect(() => {
		if (override.sources) name = override.sources.join(' and ');
		open = !!override.sources;
	});
</script>

<!-- Closing the sheet any other way counts as "no". -->
<Sheet bind:open title="Tracked by {name}" onclose={() => answerOverride(false)}>
	<p class="text-[0.9375rem] text-muted">
		{name} keeps this one up to date by itself, so a change made here may not match it. Change it in Axiom anyway?
	</p>
	<div class="mt-4 flex flex-col gap-2">
		<Button variant="primary" full onclick={() => answerOverride(false)}>Leave it to {name}</Button>
		<Button variant="secondary" full onclick={() => answerOverride(true)}>Change anyway</Button>
	</div>
</Sheet>
