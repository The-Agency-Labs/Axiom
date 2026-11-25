// Season/episode state of one show (TVDB tv or anime), shared by the episode list and the title page's
// action bar so "mark next episode" and the list always agree. Every write goes through mutate():
// the UI flips first, the request is queued when offline, and a failure rolls the flip back.

import { get, mutate } from '#lib/api.ts';
import { toast } from '#lib/toast.svelte.ts';
import type { SeasonDetail, Show, ShowEpisode, ShowSeason } from '#lib/types.ts';

interface Body {
	episodes?: number[];
	watched?: boolean;
	rewatch?: boolean;
}

export class ShowState {
	show = $state<Show | null>(null);
	/** Every season fetched so far, by number. */
	seasons = $state<Record<number, SeasonDetail>>({});
	selected = $state<number | null>(null);
	/** First regular season with unwatched episodes: where "next episode" lives. */
	upSeason = $state<number | null>(null);
	error = $state('');
	loadingSeason = $state(false);

	source: string;
	mediaId: string;
	private opts: { initialSeason?: number | null; onchange?: () => void };

	constructor(source: string, mediaId: string, opts: { initialSeason?: number | null; onchange?: () => void } = {}) {
		this.source = source;
		this.mediaId = mediaId;
		this.opts = opts;
	}

	get base() {
		return `/shows/${this.source}/${this.mediaId}`;
	}
	get current() {
		return this.selected === null ? null : (this.seasons[this.selected] ?? null);
	}

	/** Open on the first regular season that still has unwatched episodes. */
	static pick(seasons: ShowSeason[]) {
		const regular = seasons.filter((s) => !s.is_special && s.episode_count);
		return (
			regular.find((s) => s.watched > 0 && s.watched < s.episode_count)?.season_number ??
			regular.find((s) => s.watched < s.episode_count)?.season_number ??
			regular.at(-1)?.season_number ??
			seasons[0]?.season_number ??
			1
		);
	}

	/** Seasons in display order: regular seasons, specials last. */
	get ordered() {
		const list = this.show?.seasons ?? [];
		return [...list.filter((s) => !s.is_special), ...list.filter((s) => s.is_special)];
	}

	async load() {
		try {
			this.show = await get<Show>(this.base, (d) => (this.show = d));
		} catch {
			this.error = 'Could not load seasons.';
			return;
		}
		this.upSeason = ShowState.pick(this.show.seasons);
		this.selected ??= this.opts.initialSeason ?? this.upSeason;
		await Promise.all([this.loadSeason(this.selected), this.upSeason !== this.selected ? this.loadSeason(this.upSeason) : null]);
	}

	async loadSeason(n: number) {
		if (this.seasons[n]) return;
		this.loadingSeason = n === this.selected;
		try {
			this.seasons[n] = await get<SeasonDetail>(`${this.base}/seasons/${n}`, (d) => (this.seasons[n] = d));
		} catch {
			/* the list shows its own empty state */
		} finally {
			this.loadingSeason = false;
		}
	}

	select(n: number) {
		this.selected = n;
		void this.loadSeason(n);
	}

	/** The episode "mark next" would watch: first aired, unwatched one of the up-next season. */
	get nextUp(): { season: number; episode: ShowEpisode } | null {
		if (this.upSeason === null) return null;
		const data = this.seasons[this.upSeason];
		const ep = data?.episodes.find((e) => !e.watched && e.aired);
		return ep ? { season: this.upSeason, episode: ep } : null;
	}
	/** An unwatched episode exists but has not aired yet. */
	get upcoming(): ShowEpisode | null {
		if (this.upSeason === null) return null;
		return this.seasons[this.upSeason]?.episodes.find((e) => !e.watched && !e.aired) ?? null;
	}

	/** Re-read everything after writes that changed more than we flipped locally. */
	async refresh() {
		const seasons = Object.keys(this.seasons).map(Number);
		try {
			this.show = await get<Show>(this.base);
			this.upSeason = ShowState.pick(this.show.seasons);
			await Promise.all(
				seasons.map(async (n) => (this.seasons[n] = await get<SeasonDetail>(`${this.base}/seasons/${n}`)))
			);
			if (this.upSeason !== null && !this.seasons[this.upSeason]) await this.loadSeason(this.upSeason);
		} catch {
			/* keep what is shown */
		}
	}

	/** Mark/unmark specific episodes of a season. `label` is shown in toasts and the offline queue. */
	write(season: number, numbers: number[], body: Body, label: string, undo = true) {
		const data = this.seasons[season];
		if (!data || !numbers.length) return;
		const watched = body.watched !== false;
		const eps = data.episodes.filter((e) => numbers.includes(e.episode_number));
		const summary = this.show?.seasons.find((s) => s.season_number === season);
		const before = {
			eps: eps.map((e) => ({ e, watched: e.watched, count: e.watch_count, at: e.watched_at })),
			watched: data.watched,
			summary: summary?.watched,
			total: this.show?.completion.watched
		};

		mutate<SeasonDetail>({
			path: `${this.base}/seasons/${season}/episodes`,
			body: { episodes: numbers, watched, ...(body.rewatch ? { rewatch: true } : {}) },
			label,
			optimistic: () => {
				let changed = 0;
				for (const e of eps) {
					if (watched) {
						if (!e.watched) changed++;
						e.watched = true;
						e.watch_count = body.rewatch || e.watch_count ? e.watch_count + 1 : 1;
						e.watched_at = new Date().toISOString();
					} else if (e.watched) {
						changed--;
						e.watched = false;
						e.watch_count = 0;
						e.watched_at = null;
					}
				}
				data.watched += changed;
				if (summary) summary.watched += changed;
				if (this.show && season !== 0) this.show.completion.watched += changed;
				return () => {
					for (const b of before.eps) Object.assign(b.e, { watched: b.watched, watch_count: b.count, watched_at: b.at });
					data.watched = before.watched;
					if (summary && before.summary !== undefined) summary.watched = before.summary;
					if (this.show && before.total !== undefined) this.show.completion.watched = before.total;
				};
			},
			onSuccess: async (fresh) => {
				this.seasons[season] = fresh;
				await this.refresh();
				this.opts.onchange?.();
				if (undo) {
					const first = numbers.length === 1 ? `E${numbers[0]}` : `${numbers.length} episodes`;
					toast(`${first} ${watched ? 'watched' : 'unmarked'}`, {
						kind: 'success',
						action: {
							label: 'Undo',
							run: () =>
								this.write(season, numbers, { watched: !watched }, `${first} ${watched ? 'unmarked' : 'watched'}`, false)
						}
					});
				}
			}
		});
	}

	toggle(season: number, ep: ShowEpisode) {
		this.write(season, [ep.episode_number], { watched: !ep.watched }, `E${ep.episode_number} ${ep.watched ? 'unmarked' : 'watched'}`);
	}
	rewatch(season: number, ep: ShowEpisode) {
		this.write(season, [ep.episode_number], { watched: true, rewatch: true }, `E${ep.episode_number} rewatched`);
	}
	/** Mark every aired episode up to and including `number`. */
	upTo(season: number, number: number) {
		const data = this.seasons[season];
		if (!data) return;
		const list = data.episodes.filter((e) => e.episode_number <= number && !e.watched && e.aired).map((e) => e.episode_number);
		this.write(season, list, { watched: true }, `Season ${season} up to E${number}`);
	}
	markSeason(season: number, watched: boolean) {
		const data = this.seasons[season];
		if (!data) return;
		const list = data.episodes.filter((e) => (watched ? !e.watched && e.aired : e.watched)).map((e) => e.episode_number);
		this.write(season, list, { watched }, watched ? `Season ${season} watched` : `Season ${season} unmarked`);
	}
	markNext() {
		const next = this.nextUp;
		if (next) this.toggle(next.season, next.episode);
	}

	/** Episodes before `number` that are aired and unwatched (for the catch-up prompt). */
	missingBefore(season: number, number: number) {
		return (this.seasons[season]?.episodes ?? []).filter((e) => e.episode_number < number && !e.watched && e.aired).length;
	}
}
