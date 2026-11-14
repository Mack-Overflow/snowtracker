// Gridded forecasts for the map, as served by /api/field.

export type FieldModel = 'mean' | 'run';

/** A snapped lat/lon grid: origin and size in whole steps. */
export type Grid = { step: number; i0: number; j0: number; nx: number; ny: number };

export type Field = Grid & {
	steps: number;
	/** Unix seconds of step 0; steps are hourly. */
	start: number;
	elevation: Float32Array;
	/** Hundredths of a mm per hour, [step][row south→north][column west→east]. */
	precip: Int16Array;
	/** Hundredths of a °C at `elevation`. */
	temp: Int16Array;
};

export type Bounds = { west: number; south: number; east: number; north: number };

// These mirror the limits in api/internal/httpapi/field.go. Grids have an
// even origin and an odd size so that nearby views ask for the same grid and
// share a cached fetch: upstream bills one call per grid point.
const STEPS = [0.25, 0.5, 1, 2, 4];
const MAX_SIDE = 13;
const MAX_POINTS = 144;
const MAX_LAT = 85;

const evenDown = (n: number) => n - (((n % 2) + 2) % 2);

/** The finest grid that covers the bounds within the server's size limits. */
export function gridFor(b: Bounds): Grid {
	let grid!: Grid;
	for (const step of STEPS) {
		const lim = evenDown(Math.floor(MAX_LAT / step));
		const i0 = evenDown(Math.floor(b.west / step));
		const j0 = Math.max(evenDown(Math.floor(b.south / step)), -lim);
		let nx = Math.ceil(b.east / step) - i0 + 1;
		let ny = Math.min(Math.ceil(b.north / step), lim) - j0 + 1;
		nx = Math.max(nx + (1 - (nx % 2)), 3);
		ny = Math.max(ny + (1 - (ny % 2)), 3);
		grid = { step, i0, j0, nx, ny };
		if (nx <= MAX_SIDE && ny <= MAX_SIDE && nx * ny <= MAX_POINTS) return grid;
	}
	// Too wide even for the coarsest step: keep the middle of the view.
	const nx = Math.min(grid.nx, MAX_SIDE);
	const ny = Math.min(grid.ny, 11);
	return {
		...grid,
		i0: evenDown(grid.i0 + Math.floor((grid.nx - nx) / 2)),
		j0: evenDown(grid.j0 + Math.floor((grid.ny - ny) / 2)),
		nx,
		ny
	};
}

const cache = new Map<string, Promise<Field>>();

export function fetchField(model: FieldModel, g: Grid): Promise<Field> {
	const url = `/api/field?model=${model}&step=${g.step}&i0=${g.i0}&j0=${g.j0}&nx=${g.nx}&ny=${g.ny}`;
	let hit = cache.get(url);
	if (!hit) {
		hit = load(url, g);
		cache.set(url, hit);
		// The server refreshes fields on its own schedule; don't pin old ones here.
		const drop = () => cache.get(url) === hit && cache.delete(url);
		hit.then(() => setTimeout(drop, 30 * 60_000), drop);
	}
	return hit;
}

async function load(url: string, g: Grid): Promise<Field> {
	const res = await fetch(url);
	if (!res.ok) {
		const body = await res.json().catch(() => null);
		throw new Error(body?.error ?? `Map data request failed (${res.status})`);
	}
	const buf = await res.arrayBuffer();
	const head = new DataView(buf);
	const magic = String.fromCharCode(...new Uint8Array(buf, 0, 4));
	const [nx, ny, steps] = [head.getUint16(4, true), head.getUint16(6, true), head.getUint16(8, true)];
	const cells = nx * ny;
	if (magic !== 'PCF1' || nx !== g.nx || ny !== g.ny || buf.byteLength !== 32 + 4 * cells + 4 * cells * steps) {
		throw new Error('Map data came back in an unexpected format');
	}
	return {
		...g,
		steps,
		start: head.getFloat64(24, true),
		elevation: new Float32Array(buf, 32, cells),
		precip: new Int16Array(buf, 32 + 4 * cells, cells * steps),
		temp: new Int16Array(buf, 32 + 4 * cells + 2 * cells * steps, cells * steps)
	};
}
