// Snow-total bands for the topo map, scaled to the storm on screen.
//
// Band edges run from one unit up to the storm's peak on a log-like scale,
// rounded to readable numbers and never closer than one unit apart. A small
// storm therefore steps unit by unit (1, 2, 3 … 10 in), while a big one spends
// its bands where the differences matter (… 10, 12, 15, 20, 25, 30, 40, 50 in):
// the gap between 10 and 20 gets more colours than the gap between 40 and 50.

import type { RGB } from './kernel';

/** Readable mantissas; edges past ten units are rounded to one of these × 10ⁿ. */
const NICE = [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10];
/** The kernel takes at most 16 bands; one goes to the trace band. */
const EDGES = 15;
/** Below this peak (in units) the scale stops shrinking, so a dusting isn't
 *  painted in the deepest colours. */
const MIN_PEAK = { imperial: 6, metric: 15 };
/** One unit: the smallest gap between edges. */
const STEP = { imperial: 1, metric: 2 };
/** Where the scale turns from even steps to logarithmic ones, in units. Well
 *  below it edges are about evenly spaced; well above, about evenly spaced
 *  in ratio. */
const KNEE = { imperial: 6, metric: 15 };

function nice(v: number, step: number): number {
	if (v < 10 * step) return Math.max(step, Math.round(v / step) * step);
	const mag = 10 ** Math.floor(Math.log10(v));
	const m = v / mag;
	let best = NICE[0];
	for (const n of NICE) if (Math.abs(n - m) < Math.abs(best - m)) best = n;
	return best * mag;
}

/** Band edges in display units (inches or cm), rising, from a trace band up to
 *  the storm's peak. Spacing is even in log(1 + v / knee). */
export function bandEdges(peak: number, system: 'metric' | 'imperial'): number[] {
	const step = STEP[system];
	const knee = KNEE[system];
	const top = Math.max(peak, MIN_PEAK[system]);
	const span = Math.log1p(top / knee);
	const edges = [step / 2];
	for (let i = 1; i < EDGES; i++) {
		const v = Math.min(nice(knee * Math.expm1((span * i) / (EDGES - 1)), step), top);
		if (v > edges[edges.length - 1]) edges.push(v);
	}
	// The top edge is the peak itself, rounded down to a readable number.
	const last = edges.length - 1;
	const floor = edges[last] >= 10 * step ? Math.round(edges[last]) : Math.floor(edges[last] / step) * step;
	if (floor > edges[last - 1]) edges[last] = floor;
	else edges.pop();
	return edges;
}

// Lightness runs one way only, so deeper always reads as more (darker on the
// light map, brighter on the dark one); the hue sweeps blue → violet →
// magenta so neighbouring bands stay apart even when there are many.
const RAMP: Record<'light' | 'dark', RGB[]> = {
	light: [
		[205, 232, 255],
		[150, 204, 252],
		[96, 170, 245],
		[52, 128, 228],
		[44, 88, 204],
		[72, 54, 182],
		[112, 36, 160],
		[148, 24, 130],
		[150, 14, 86],
		[104, 6, 46]
	],
	dark: [
		[8, 54, 118],
		[14, 82, 160],
		[28, 116, 204],
		[56, 150, 240],
		[110, 160, 255],
		[156, 150, 255],
		[198, 140, 250],
		[236, 140, 228],
		[255, 168, 214],
		[255, 214, 236]
	]
};

/** `count` colours spread evenly along the ramp. */
export function bandColours(count: number, dark: boolean): RGB[] {
	const ramp = RAMP[dark ? 'dark' : 'light'];
	return Array.from({ length: count }, (_, i) => {
		const at = count === 1 ? ramp.length - 1 : (i / (count - 1)) * (ramp.length - 1);
		const lo = Math.floor(at);
		const hi = Math.min(lo + 1, ramp.length - 1);
		const f = at - lo;
		return ramp[lo].map((c, k) => Math.round(c + (ramp[hi][k] - c) * f)) as RGB;
	});
}
