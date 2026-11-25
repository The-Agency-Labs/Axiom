import { redirect } from '@sveltejs/kit';
import { get } from '#lib/api.ts';
import { titleHref } from '#lib/media.ts';
import type { Item } from '#lib/types.ts';

// Old URL (a library entry id): look the entry up and send the visitor to the one title route.
export const load = async ({ params }) => {
	// The Now feed reports TV seasons as "season"; they live under tv (or anime) entries.
	const kinds = params.type === 'season' || params.type === 'episode' ? ['tv', 'anime'] : [params.type];
	for (const kind of kinds) {
		let entry: { item: Item } | null = null;
		try {
			entry = await get<{ item: Item }>(`/library/${kind}/${params.id}?include_metadata=false`);
		} catch {
			/* not this kind, or gone */
		}
		if (entry) redirect(307, titleHref(entry.item, kind, Number(params.id)));
	}
	redirect(307, `/library/${kinds[0]}`);
};
