import { redirect } from '@sveltejs/kit';

// "Sources" is now Settings → Integrations; keep old links and bookmarks working.
export const load = () => redirect(308, '/settings?tab=integrations');
