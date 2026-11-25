import { redirect } from '@sveltejs/kit';
import { titleHref } from '#lib/media.ts';

// Old URL (a provider id, e.g. from search results): the one title route handles tracked and untracked titles.
export const load = ({ params }) => {
	redirect(307, titleHref({ source: params.source, media_id: params.id }, params.type));
};
