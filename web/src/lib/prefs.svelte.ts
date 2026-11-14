import type { Place } from './api';
import type { System } from './format';

function read<T>(key: string, fallback: T): T {
	try {
		const raw = localStorage.getItem(key);
		return raw ? (JSON.parse(raw) as T) : fallback;
	} catch {
		return fallback;
	}
}

function write(key: string, value: unknown) {
	try {
		localStorage.setItem(key, JSON.stringify(value));
	} catch {
		// Private mode or blocked storage: preferences just don't persist.
	}
}

const defaultSystem: System =
	typeof navigator !== 'undefined' && navigator.language === 'en-US' ? 'imperial' : 'metric';

export const prefs = $state({
	system: read<System>('units', defaultSystem),
	recent: read<Place[]>('recent', []),
	/** Ids of the page sections the user has collapsed. */
	collapsed: read<string[]>('collapsed', [])
});

export function toggleSection(id: string) {
	prefs.collapsed = prefs.collapsed.includes(id)
		? prefs.collapsed.filter((c) => c !== id)
		: [...prefs.collapsed, id];
	write('collapsed', prefs.collapsed);
}

export function setSystem(system: System) {
	prefs.system = system;
	write('units', system);
}

export function remember(place: Place) {
	const same = (a: Place) => a.lat === place.lat && a.lon === place.lon;
	prefs.recent = [place, ...prefs.recent.filter((p) => !same(p))].slice(0, 6);
	write('recent', prefs.recent);
}
