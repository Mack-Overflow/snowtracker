export type Place = {
	id?: number;
	name: string;
	region?: string;
	country?: string;
	lat: number;
	lon: number;
	elevation?: number;
	kind?: string;
};

export type Quantiles = { p10: number; p50: number; p90: number };
export type Kind = 'snow' | 'mixed' | 'rain';

export type Band = { id: string; label: string; elevation: number; modelled: boolean };

export type Day = {
	date: string;
	hours: number;
	members: number;
	precipProb: number;
	heavyProb: number;
	precipMm: Quantiles;
	bands: {
		snowCm: Quantiles;
		snowProb: number;
		rainMm: number;
		kind: Kind;
		tempMinC: number;
		tempMaxC: number;
	}[];
};

export type Storm = {
	id: string;
	start: string;
	end: string;
	peak: string;
	hours: number;
	likelihood: number;
	byModel: Record<string, number>;
	members: number;
	precipMm: Quantiles;
	intensity: 'light' | 'moderate' | 'heavy';
	snowLevelM: number | null;
	underway: boolean;
	openEnded: boolean;
	bands: { snowCm: Quantiles; rainMm: number; kind: Kind }[];
};

export type Forecast = {
	location: {
		name: string;
		kind?: string;
		lat: number;
		lon: number;
		elevation: number;
		timezone: string;
		utcOffsetSeconds: number;
	};
	generatedAt: string;
	source: { provider: string; attribution: string };
	bands: Band[];
	notes: string[];
	models: { name: string; members: number; horizonHours: number }[];
	hourly: {
		times: string[];
		wetProb: number[];
		meanMm: number[];
		p90Mm: number[];
		bands: { tempC: number[]; snowShare: number[] }[];
	};
	days: Day[];
	storms: Storm[];
};

async function get<T>(url: string, signal?: AbortSignal): Promise<T> {
	const res = await fetch(url, { signal });
	const body = await res.json().catch(() => null);
	if (!res.ok) throw new Error(body?.error ?? `Request failed (${res.status})`);
	return body as T;
}

export async function searchPlaces(q: string, signal?: AbortSignal): Promise<Place[]> {
	const data = await get<{ results: Place[] }>(`/api/search?q=${encodeURIComponent(q)}`, signal);
	return data.results;
}

export function placeParams(p: Place): URLSearchParams {
	const sp = new URLSearchParams({ lat: String(p.lat), lon: String(p.lon), name: p.name });
	if (p.region) sp.set('region', p.region);
	if (p.kind) sp.set('kind', p.kind);
	if (p.elevation != null) sp.set('elevation', String(p.elevation));
	return sp;
}

export function fetchForecast(p: Place, signal?: AbortSignal): Promise<Forecast> {
	return get<Forecast>(`/api/forecast?${placeParams(p)}`, signal);
}
