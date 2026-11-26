// A title an app tracks (Jellyfin, Kindle, Paperback, Steam) takes its progress
// from that app. A hand-made change is refused by the server; the user is asked
// here, one question at a time, and the request is repeated only if they say so.

export const override = $state({ sources: null as string[] | null });

let answer: ((yes: boolean) => void) | null = null;
let queue: Promise<unknown> = Promise.resolve();

export function confirmOverride(sources: string[]): Promise<boolean> {
	const asked = queue.then(
		() =>
			new Promise<boolean>((resolve) => {
				answer = resolve;
				override.sources = sources;
			})
	);
	queue = asked;
	return asked;
}

export function answerOverride(yes: boolean) {
	const resolve = answer;
	answer = null;
	override.sources = null;
	resolve?.(yes);
}
