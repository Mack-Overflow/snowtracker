// Loads the WebAssembly snow kernel (web/wasm/kernel.ts) and feeds it.
import wasmUrl from './kernel.wasm?url';
import type { Field } from './field';

type Exports = {
	memory: WebAssembly.Memory;
	configure(nx: number, ny: number, steps: number, w: number, h: number): void;
	precipInPtr(): number;
	tempInPtr(): number;
	elevPtr(): number;
	gxPtr(): number;
	gyPtr(): number;
	demPtr(): number;
	accumPtr(): number;
	outPtr(): number;
	levelsPtr(): number;
	palettePtr(): number;
	prepare(): void;
	accumulate(t: number, sign: number): void;
	resetAccum(): void;
	renderRate(t: number, snow: number, rain: number): void;
	renderAccum(n: number): void;
	sample(x: number, y: number, t: number): number;
	sampleSnowCm(): number;
	sampleRainMm(): number;
	sampleTempC(): number;
};

export type RGB = [number, number, number];
/** One accumulation band: everything from `cm` up to the next band. */
export type Band = { cm: number; colour: RGB; alpha: number };

const pack = ([r, g, b]: RGB, alpha = 0) =>
	(r | (g << 8) | (b << 16) | (Math.round(alpha * 255) << 24)) >>> 0;

/** What the map raster covers: pixel size plus, per column and row, where it
 *  falls on the forecast grid (negative = outside it) and the terrain. */
export type Raster = {
	width: number;
	height: number;
	gx: Float32Array;
	gy: Float32Array;
	/** Metres per pixel, row-major from the top; NaN where unknown. */
	dem: Float32Array;
};

let instance: Promise<Exports> | undefined;

function exportsOnce(): Promise<Exports> {
	instance ??= WebAssembly.instantiateStreaming(fetch(wasmUrl), {
		env: {
			abort() {
				throw new Error('snow kernel aborted');
			}
		}
	}).then((r) => r.instance.exports as unknown as Exports);
	return instance;
}

/** One loaded field on one raster. `through` is the last hour included in the
 *  accumulation, or `from - 1` when it is empty. */
export class SnowKernel {
	private through: number;

	private constructor(
		private k: Exports,
		private raster: Raster,
		readonly steps: number,
		/** First hour that counts towards accumulation. */
		readonly from: number
	) {
		this.through = from - 1;
	}

	static async create(field: Field, raster: Raster, from: number): Promise<SnowKernel> {
		const k = await exportsOnce();
		const cells = field.nx * field.ny;
		k.configure(field.nx, field.ny, field.steps, raster.width, raster.height);
		// configure() may grow the memory, so views are made only after it.
		const buf = k.memory.buffer;
		new Int16Array(buf, k.precipInPtr(), cells * field.steps).set(field.precip);
		new Int16Array(buf, k.tempInPtr(), cells * field.steps).set(field.temp);
		new Float32Array(buf, k.elevPtr(), cells).set(field.elevation);
		new Float32Array(buf, k.gxPtr(), raster.width).set(raster.gx);
		new Float32Array(buf, k.gyPtr(), raster.height).set(raster.gy);
		new Float32Array(buf, k.demPtr(), raster.width * raster.height).set(raster.dem);
		k.prepare();
		return new SnowKernel(k, raster, field.steps, Math.min(Math.max(from, 0), field.steps - 1));
	}

	private pixels(): ImageData {
		const { width, height } = this.raster;
		const out = new Uint8ClampedArray(this.k.memory.buffer, this.k.outPtr(), width * height * 4);
		return new ImageData(new Uint8ClampedArray(out), width, height);
	}

	/** Precipitation rate during hour t, snow in one colour and rain in another. */
	rate(t: number, snow: RGB, rain: RGB): ImageData {
		this.k.renderRate(t, pack(snow), pack(rain));
		return this.pixels();
	}

	/** Brings the running total to cover the first hour through hour t. */
	private sumThrough(t: number) {
		// Scrubbing usually moves a few hours, so add or remove just those.
		if (t < this.through && t - this.from < this.through - t) {
			this.k.resetAccum();
			this.through = this.from - 1;
		}
		while (this.through < t) this.k.accumulate(++this.through, 1);
		while (this.through > t) this.k.accumulate(this.through--, -1);
	}

	/** Snow that falls from the first hour through hour t, drawn in `bands`
	 *  (rising, at most 16). */
	accumulation(t: number, bands: Band[]): ImageData {
		this.sumThrough(t);
		const buf = this.k.memory.buffer;
		new Float32Array(buf, this.k.levelsPtr(), bands.length).set(bands.map((b) => b.cm));
		new Uint32Array(buf, this.k.palettePtr(), bands.length).set(bands.map((b) => pack(b.colour, b.alpha)));
		this.k.renderAccum(bands.length);
		return this.pixels();
	}

	/** The most snow any pixel gets over the whole forecast, in cm. */
	peakCm(): number {
		this.sumThrough(this.steps - 1);
		const { width, height } = this.raster;
		const accum = new Float32Array(this.k.memory.buffer, this.k.accumPtr(), width * height);
		let peak = 0;
		for (let i = 0; i < accum.length; i++) if (accum[i] > peak) peak = accum[i];
		return peak;
	}

	/** Values under one raster pixel. `snowCm` and `rainMm` are for hour t
	 *  alone; `totalCm` is the snow from the first hour through hour t, and
	 *  `runTotalCm` the snow over the whole forecast. */
	at(x: number, y: number, t: number) {
		const { width, height } = this.raster;
		if (x < 0 || y < 0 || x >= width || y >= height || !this.k.sample(x, y, 0)) return null;
		// One pixel over every hour is cheap, and leaves the map's running total alone.
		let runTotalCm = 0;
		for (let hour = this.from; hour < this.steps; hour++) {
			this.k.sample(x, y, hour);
			runTotalCm += this.k.sampleSnowCm();
		}
		this.k.sample(x, y, t);
		this.sumThrough(t);
		const buf = this.k.memory.buffer;
		const px = y * width + x;
		return {
			snowCm: this.k.sampleSnowCm(),
			rainMm: this.k.sampleRainMm(),
			tempC: this.k.sampleTempC(),
			runTotalCm,
			totalCm: new Float32Array(buf, this.k.accumPtr() + px * 4, 1)[0],
			elevation: new Float32Array(buf, this.k.demPtr() + px * 4, 1)[0]
		};
	}
}
