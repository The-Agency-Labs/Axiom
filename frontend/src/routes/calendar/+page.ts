import { redirect } from '@sveltejs/kit';

// "Calendar" is now "Upcoming"; keep old links and bookmarks working.
export const load = () => redirect(308, '/upcoming');
