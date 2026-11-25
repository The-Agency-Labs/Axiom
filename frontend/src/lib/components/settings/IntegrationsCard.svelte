<script lang="ts">
	import { Plug } from '@lucide/svelte';
	import { post } from '#lib/api.ts';
	import { saveSettings, settings } from '#lib/settings.svelte.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, Card, CopyField } from '#lib/ui/index.ts';

	let jellyfinUsers = $state(settings.data?.jellyfin_users ?? '');
	let watched = $state(settings.data?.watched_percent ?? 90);
	let saving = $state(false);
	let confirming = $state<'webhook' | 'calendar' | null>(null);
	let regenerating = $state(false);

	const webhookUrl = $derived(settings.data ? location.origin + settings.data.webhook_url_path : '');
	const calendarUrl = $derived(settings.data ? location.origin + settings.data.calendar_url_path : '');

	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			await saveSettings({ jellyfin_users: jellyfinUsers, watched_percent: Number(watched) });
			jellyfinUsers = settings.data?.jellyfin_users ?? jellyfinUsers;
			watched = settings.data?.watched_percent ?? watched;
			toast('Integration settings saved', { kind: 'success' });
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not save', { kind: 'error' });
		} finally {
			saving = false;
		}
	}

	/** Two-step: the first tap arms the button, the second regenerates (the old URL stops working). */
	async function regenerate(which: 'webhook' | 'calendar') {
		if (confirming !== which) {
			confirming = which;
			setTimeout(() => confirming === which && (confirming = null), 5000);
			return;
		}
		confirming = null;
		regenerating = true;
		try {
			const paths = await post<{ webhook_url_path: string; calendar_url_path: string }>(
				which === 'webhook' ? '/settings/webhook-token' : '/settings/calendar-token'
			);
			if (settings.data) {
				settings.data.webhook_url_path = paths.webhook_url_path ?? settings.data.webhook_url_path;
				settings.data.calendar_url_path = paths.calendar_url_path ?? settings.data.calendar_url_path;
			}
			toast(`New ${which} URL created. Update it wherever you used the old one.`, { kind: 'success' });
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not regenerate', { kind: 'error' });
		} finally {
			regenerating = false;
		}
	}
</script>

<Card id="integrations" title="Integrations" description="Jellyfin matching and the private URLs for webhooks and calendar apps." icon={Plug}>
	<form onsubmit={save} class="space-y-4">
		<label class="block">
			<span class="mb-1 block text-sm font-medium">Jellyfin users</span>
			<input
				class="input"
				bind:value={jellyfinUsers}
				placeholder="alice, bob"
				autocapitalize="none"
				autocomplete="off"
				spellcheck="false"
			/>
			<span class="mt-1 block text-xs text-muted">Comma-separated Jellyfin usernames whose plays count as yours.</span>
		</label>
		<label class="block">
			<span class="mb-1 block text-sm font-medium">Count as watched at {watched}%</span>
			<input type="range" min="50" max="100" step="1" bind:value={watched} class="h-11 w-full accent-[var(--color-brand)]" />
		</label>
		<Button type="submit" variant="primary" loading={saving}>Save</Button>
	</form>

	<div class="mt-6 space-y-5 border-t border-line pt-5">
		<div>
			<h3 class="text-sm font-medium">Jellyfin webhook URL</h3>
			<p class="mb-2 text-xs text-muted">Paste into the Jellyfin Webhook plugin (event: Playback progress/stop).</p>
			<CopyField value={webhookUrl} label="webhook URL" />
			<Button class="mt-2" variant={confirming === 'webhook' ? 'danger' : 'secondary'} size="sm" loading={regenerating} onclick={() => regenerate('webhook')}>
				{confirming === 'webhook' ? 'Tap again to confirm' : 'Regenerate webhook URL'}
			</Button>
		</div>
		<div>
			<h3 class="text-sm font-medium">Calendar feed URL</h3>
			<p class="mb-2 text-xs text-muted">Subscribe to this from Apple Calendar, Google Calendar or any iCal app.</p>
			<CopyField value={calendarUrl} label="calendar URL" />
			<Button class="mt-2" variant={confirming === 'calendar' ? 'danger' : 'secondary'} size="sm" loading={regenerating} onclick={() => regenerate('calendar')}>
				{confirming === 'calendar' ? 'Tap again to confirm' : 'Regenerate calendar URL'}
			</Button>
		</div>
	</div>
</Card>
