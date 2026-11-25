<script lang="ts">
	/** Loading placeholder. Give it the final size (class="h-40 w-full" / aspect-[2/3]) to avoid layout shift. */
	let {
		shape = 'rect',
		lines = 1,
		class: className = ''
	}: { shape?: 'rect' | 'circle' | 'text'; lines?: number; class?: string } = $props();
</script>

{#if shape === 'text'}
	<div class="space-y-2 {className}" aria-hidden="true">
		{#each Array(lines) as _, i (i)}
			<div class="skeleton h-3.5 rounded-md" style="width: {i === lines - 1 && lines > 1 ? '60%' : '100%'}"></div>
		{/each}
	</div>
{:else}
	<div class="skeleton {shape === 'circle' ? 'rounded-full' : 'rounded-xl'} {className}" aria-hidden="true"></div>
{/if}
