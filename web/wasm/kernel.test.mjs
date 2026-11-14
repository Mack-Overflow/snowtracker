// Checks the compiled kernel: `pnpm test:wasm`.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const here = (p) => new URL(p, import.meta.url);
const { instance } = await WebAssembly.instantiate(
	await readFile(here('../src/lib/map/kernel.wasm')),
	{ env: { abort: () => { throw new Error('kernel aborted'); } } }
);
const k = instance.exports;

// 1. The rain/snow rule matches the Go implementation bit for bit.
const vectors = JSON.parse(
	await readFile(here('../../api/internal/compute/testdata/snow_vectors.json'), 'utf8')
);
assert.ok(vectors.length > 50);
for (const v of vectors) {
	assert.equal(k.snowFraction(v.tempC), Math.fround(v.snowShare), `snowFraction(${v.tempC})`);
	assert.equal(k.snowCmPerMm(v.tempC), Math.fround(v.snowCmPerMm), `snowCmPerMm(${v.tempC})`);
}

// 2. A 3x3 grid over a 5x5 raster: 1 mm/h everywhere for three hours, 1.5 °C at
// sea level, terrain rising 1000 m per raster column from west to east.
const [nx, ny, nt, w, h] = [3, 3, 3, 5, 5];
k.configure(nx, ny, nt, w, h);
const mem = () => k.memory.buffer;
new Int16Array(mem(), k.precipInPtr(), nt * nx * ny).fill(100);
new Int16Array(mem(), k.tempInPtr(), nt * nx * ny).fill(150);
new Float32Array(mem(), k.elevPtr(), nx * ny).fill(0);
new Float32Array(mem(), k.gxPtr(), w).set([0, 0.5, 1, 1.5, 2]);
new Float32Array(mem(), k.gyPtr(), h).set([-1, 0.5, 1, 1.5, 2]); // top row off grid
const dem = new Float32Array(mem(), k.demPtr(), w * h);
for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) dem[y * w + x] = x * 1000;
dem[2 * w] = NaN; // unknown terrain falls back to the grid's own elevation (0 m)
k.prepare();

const accum = () => new Float32Array(mem(), k.accumPtr(), w * h);
const expected = (elevation, hours) => {
	const t = Math.fround(1.5 - elevation * 0.0065);
	return hours * k.snowFraction(t) * k.snowCmPerMm(t);
};
for (let t = 0; t < nt; t++) k.accumulate(t, 1);
for (let x = 0; x < w; x++) {
	assert.equal(accum()[x], 0, 'off-grid row stays empty');
	assert.ok(Math.abs(accum()[w + x] - expected(x * 1000, 3)) < 1e-4, `column ${x} after 3 h`);
}
assert.ok(Math.abs(accum()[2 * w] - expected(0, 3)) < 1e-4, 'NaN terrain used grid elevation');
assert.ok(accum()[w + 4] > accum()[w + 1], 'higher terrain gets more snow');

k.accumulate(2, -1);
assert.ok(Math.abs(accum()[w + 3] - expected(3000, 2)) < 1e-4, 'stepping back removes an hour');
k.resetAccum();
assert.equal(accum()[w + 3], 0);

// 3. Rendering: the off-grid row is transparent, wet pixels are not, and
// rain (1.5 °C at sea level is mostly rain) is hatched while snow is solid.
k.renderRate(0, 0x1e140a, 0x3c3228); // snow 10,20,30; rain 40,50,60
const out = new Uint8Array(mem(), k.outPtr(), w * h * 4);
assert.equal(out[3], 0, 'off grid is transparent');
const px = (x, y) => out.subarray((y * w + x) * 4, (y * w + x) * 4 + 4);
assert.deepEqual([...px(4, 1)].slice(0, 3), [10, 20, 30]);
assert.equal(px(4, 1)[3], px(4, 4)[3], 'snow is a solid fill');
assert.notEqual(px(0, 1)[3], px(0, 3)[3], 'rain is hatched'); // (x+y)&7 = 1 vs 3
assert.deepEqual([...px(0, 1)].slice(0, 3), [40, 50, 60], 'rain has its own colour');
assert.ok(k.sample(4, 1, 0) && k.sampleSnowCm() > 1 && k.sampleRainMm() === 0);
assert.equal(k.sample(0, 0, 0), 0);

for (let t = 0; t < nt; t++) k.accumulate(t, 1);
new Float32Array(mem(), k.levelsPtr(), 2).set([1, 4]);
new Uint32Array(mem(), k.palettePtr(), 2).set([0x80010203, 0xff3264c8]);
k.renderAccum(2);
assert.equal(px(0, 1)[3], 0, 'below the first band is transparent');
assert.deepEqual([...px(4, 1)], [200, 100, 50, 255], 'top band takes the last palette colour');

// 4. Speed on a realistic size: a full ten-day accumulation.
{
	const [nx, ny, nt, w, h] = [13, 11, 240, 720, 450];
	k.configure(nx, ny, nt, w, h);
	new Int16Array(mem(), k.precipInPtr(), nt * nx * ny).fill(50);
	new Int16Array(mem(), k.tempInPtr(), nt * nx * ny).fill(-300);
	new Float32Array(mem(), k.gxPtr(), w).forEach((_, x, a) => (a[x] = (x / w) * (nx - 1)));
	new Float32Array(mem(), k.gyPtr(), h).forEach((_, y, a) => (a[y] = (y / h) * (ny - 1)));
	k.prepare();
	const t0 = performance.now();
	for (let t = 0; t < nt; t++) k.accumulate(t, 1);
	const all = performance.now() - t0;
	const t1 = performance.now();
	k.renderRate(5, 1, 2);
	console.log(
		`kernel ok: ${w}x${h} px, 240 h accumulated in ${all.toFixed(0)} ms ` +
			`(${(all / nt).toFixed(2)} ms/h), one frame ${(performance.now() - t1).toFixed(2)} ms`
	);
}
