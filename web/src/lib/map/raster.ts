// Lays a pixel raster over a map view and samples terrain into it.
import type { Bounds, Grid } from './field';
import type { Raster } from './kernel';

const mercY = (lat: number) => {
	const s = Math.sin((lat * Math.PI) / 180);
	return 0.5 - Math.log((1 + s) / (1 - s)) / (4 * Math.PI);
};
const latOf = (y: number) => (Math.atan(Math.sinh(Math.PI * (1 - 2 * y))) * 180) / Math.PI;

/** Fetches one decoded elevation tile (metres, row-major). */
export type DemTiles = (z: number, x: number, y: number) => Promise<{ width: number; data: Float32Array }>;

/**
 * Builds the raster for `bounds` at width × height pixels. Rows are evenly
 * spaced in Web Mercator, because that is how the map stretches an image
 * between its corners. Terrain comes from tiles at `zoom`; any tile that
 * fails to load leaves NaN, which the kernel fills from the forecast grid.
 */
export async function buildRaster(
	bounds: Bounds,
	width: number,
	height: number,
	grid: Grid,
	zoom: number,
	tiles: DemTiles
): Promise<Raster> {
	const gx = new Float32Array(width);
	const gy = new Float32Array(height);
	const y0 = mercY(bounds.north);
	const dy = (mercY(bounds.south) - y0) / height;
	const dx = (bounds.east - bounds.west) / width;
	for (let x = 0; x < width; x++) {
		const g = (bounds.west + (x + 0.5) * dx) / grid.step - grid.i0;
		gx[x] = g >= 0 && g <= grid.nx - 1 ? g : -1;
	}
	for (let y = 0; y < height; y++) {
		const g = latOf(y0 + (y + 0.5) * dy) / grid.step - grid.j0;
		gy[y] = g >= 0 && g <= grid.ny - 1 ? g : -1;
	}

	// Position of each column and row in tile pixels at `zoom`.
	const world = 2 ** zoom;
	const size = 256;
	const colTile = new Int32Array(width);
	const colPx = new Int32Array(width);
	const rowTile = new Int32Array(height);
	const rowPx = new Int32Array(height);
	for (let x = 0; x < width; x++) {
		const p = ((bounds.west + (x + 0.5) * dx + 180) / 360) * world;
		colTile[x] = Math.floor(p);
		colPx[x] = Math.floor((p - colTile[x]) * size);
	}
	for (let y = 0; y < height; y++) {
		const p = Math.min(Math.max((y0 + (y + 0.5) * dy) * world, 0), world - 1e-6);
		rowTile[y] = Math.floor(p);
		rowPx[y] = Math.floor((p - rowTile[y]) * size);
	}

	const wanted = new Map<string, Promise<Float32Array | null>>();
	for (let ty = rowTile[0]; ty <= rowTile[height - 1]; ty++) {
		for (let tx = colTile[0]; tx <= colTile[width - 1]; tx++) {
			const wrapped = ((tx % world) + world) % world;
			wanted.set(
				`${tx}/${ty}`,
				tiles(zoom, wrapped, ty).then(
					(t) => (t.width === size ? t.data : null),
					() => null
				)
			);
		}
	}
	const loaded = new Map<string, Float32Array | null>();
	await Promise.all([...wanted].map(async ([key, tile]) => loaded.set(key, await tile)));

	const dem = new Float32Array(width * height).fill(NaN);
	for (let y = 0; y < height; y++) {
		for (let x = 0; x < width; x++) {
			const tile = loaded.get(`${colTile[x]}/${rowTile[y]}`);
			// Sea floor is in the tiles too; nothing falls below the surface.
			if (tile) dem[y * width + x] = Math.max(tile[rowPx[y] * size + colPx[x]], 0);
		}
	}
	return { width, height, gx, gy, dem };
}
