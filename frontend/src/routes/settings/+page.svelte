<script lang="ts">
	import { ChevronLeft, Globe, Palette } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import IntegrationsCard from '#lib/components/settings/IntegrationsCard.svelte';
	import MediaTypesCard from '#lib/components/settings/MediaTypesCard.svelte';
	import PasswordCard from '#lib/components/settings/PasswordCard.svelte';
	import TokensCard from '#lib/components/settings/TokensCard.svelte';
	import UsersCard from '#lib/components/settings/UsersCard.svelte';
	import { loadSettings, saveSettings, settings } from '#lib/settings.svelte.ts';
	import { setTheme, theme, type ThemePref } from '#lib/theme.svelte.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, Card, EmptyState, Skeleton, Tabs } from '#lib/ui/index.ts';

	let timezone = $state('');
	let savingTz = $state(false);
	let themePref = $state<string>(theme.pref);

	const zones = $derived.by(() => {
		try {
			return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf('timeZone');
		} catch {
			return [];
		}
	});

	onMount(async () => {
		await loadSettings(true);
		timezone = settings.data?.timezone ?? '';
	});

	async function saveTimezone(e: SubmitEvent) {
		e.preventDefault();
		savingTz = true;
		try {
			await saveSettings({ timezone: timezone.trim() });
			toast('Timezone saved', { kind: 'success' });
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not save', { kind: 'error' });
		} finally {
			savingTz = false;
		}
	}
</script>

<svelte:head><title>Settings · Axiom</title></svelte:head>

<a href="/more" class="-ml-2 mb-1 inline-flex min-h-11 items-center gap-1 rounded-lg px-2 text-sm text-muted hover:text-ink lg:hidden">
	<ChevronLeft class="size-4" /> More
</a>
<h1 class="mb-6 text-3xl font-semibold tracking-tight">Settings</h1>

{#if !settings.data}
	{#if settings.loaded}
		<EmptyState title="Settings are unavailable" description="Check your connection and try again." />
	{:else}
		<div class="space-y-4"><Skeleton class="h-40 w-full" /><Skeleton class="h-64 w-full" /></div>
	{/if}
{:else}
	<div class="grid items-start gap-4 lg:grid-cols-2">
		<div class="space-y-4">
			<Card title="Appearance" icon={Palette}>
				<Tabs
					label="Theme"
					full
					bind:value={themePref}
					options={[
						{ value: 'system', label: 'System' },
						{ value: 'light', label: 'Light' },
						{ value: 'dark', label: 'Dark' }
					]}
					onchange={(v) => setTheme(v as ThemePref)}
				/>
			</Card>

			<MediaTypesCard />

			<Card title="Time zone" description="Used for air dates, the calendar feed and daily rollover." icon={Globe}>
				<form onsubmit={saveTimezone} class="space-y-3">
					<input
						class="input"
						list="zones"
						bind:value={timezone}
						aria-label="Time zone"
						placeholder="e.g. America/Toronto"
						autocapitalize="none"
						autocomplete="off"
						spellcheck="false"
					/>
					<datalist id="zones">{#each zones as zone (zone)}<option value={zone}></option>{/each}</datalist>
					<Button type="submit" variant="primary" loading={savingTz}>Save time zone</Button>
				</form>
			</Card>
		</div>

		<div class="space-y-4">
			<IntegrationsCard />
			<TokensCard />
			<PasswordCard />
			{#if settings.data.is_admin}
				<UsersCard me={settings.data.username} />
			{/if}
		</div>
	</div>
{/if}
