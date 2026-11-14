// Snow map kernel, compiled to WebAssembly by AssemblyScript (`pnpm wasm`).
//
// The forecast arrives on a coarse grid (tens of km). For every pixel of the
// map raster and every forecast hour this interpolates precipitation and
// temperature, moves the temperature to the pixel's real terrain height, and
// applies the same rain/snow rule as api/internal/compute. That is a few
// hundred thousand pixels times up to 240 hours, which is why it lives here.
//
// JS owns the data flow: it calls configure(), copies arrays into the buffers
// the pointer getters expose, then calls prepare(). No allocation happens
// after configure().

const LAPSE: f32 = 0.0065; // °C per metre, as in compute.LapseRate
const MISSING: i16 = -32768;

let nx: i32 = 0;
let ny: i32 = 0;
let nt: i32 = 0;
let w: i32 = 0;
let h: i32 = 0;

let pPrecipIn: usize = 0; // i16[nt*ny*nx], hundredths of a mm
let pTempIn: usize = 0; // i16[nt*ny*nx], hundredths of a °C at pElev
let pElev: usize = 0; // f32[ny*nx], metres
let pPrecip: usize = 0; // f32[nt*ny*nx], mm per hour
let pSeaTemp: usize = 0; // f32[nt*ny*nx], °C reduced to sea level
let pGx: usize = 0; // f32[w], grid column of each raster column; <0 = off grid
let pGy: usize = 0; // f32[h], grid row of each raster row
let pDem: usize = 0; // f32[h*w], metres; NaN = unknown
let pAccum: usize = 0; // f32[h*w], cm of snow summed so far
let pOut: usize = 0; // u8[h*w*4], RGBA
let pLevels: usize = 0; // f32[16], accumulation band thresholds in cm
let pPalette: usize = 0; // u32[16], one RGBA colour per band

// Results of sample().
let sSnow: f32 = 0;
let sRain: f32 = 0;
let sTemp: f32 = 0;

export function configure(gridX: i32, gridY: i32, steps: i32, width: i32, height: i32): void {
  heap.reset();
  nx = gridX;
  ny = gridY;
  nt = steps;
  w = width;
  h = height;
  const cells: usize = <usize>(nx * ny);
  const planes: usize = cells * <usize>nt;
  const pixels: usize = <usize>(w * h);
  pPrecipIn = heap.alloc(planes * 2);
  pTempIn = heap.alloc(planes * 2);
  pElev = heap.alloc(cells * 4);
  pPrecip = heap.alloc(planes * 4);
  pSeaTemp = heap.alloc(planes * 4);
  pGx = heap.alloc(<usize>w * 4);
  pGy = heap.alloc(<usize>h * 4);
  pDem = heap.alloc(pixels * 4);
  pAccum = heap.alloc(pixels * 4);
  pOut = heap.alloc(pixels * 4);
  pLevels = heap.alloc(16 * 4);
  pPalette = heap.alloc(16 * 4);
}

export function precipInPtr(): usize { return pPrecipIn; }
export function tempInPtr(): usize { return pTempIn; }
export function elevPtr(): usize { return pElev; }
export function gxPtr(): usize { return pGx; }
export function gyPtr(): usize { return pGy; }
export function demPtr(): usize { return pDem; }
export function accumPtr(): usize { return pAccum; }
export function outPtr(): usize { return pOut; }
export function levelsPtr(): usize { return pLevels; }
export function palettePtr(): usize { return pPalette; }

/** Share of precipitation falling as snow at t °C. Mirrors compute.SnowFraction. */
export function snowFraction(t: f32): f32 {
  if (t <= 0) return 1;
  if (t >= 2) return 0;
  return (2 - t) / 2;
}

/** Depth of new snow in cm per mm of liquid. Mirrors compute.SnowCmPerMm. */
export function snowCmPerMm(t: f32): f32 {
  if (t >= -2) return 1.0;
  if (t <= -12) return 1.5;
  return 1.0 + ((-2 - t) / 10) * 0.5;
}

// Bilinear sample of one grid plane. i/j is the south-west cell corner.
// @ts-ignore: decorator
@inline
function bilerp(plane: usize, i: i32, j: i32, fx: f32, fy: f32): f32 {
  const a = plane + ((<usize>(j * nx + i)) << 2);
  const b = a + ((<usize>nx) << 2);
  const s = load<f32>(a) + (load<f32>(a, 4) - load<f32>(a)) * fx;
  const n = load<f32>(b) + (load<f32>(b, 4) - load<f32>(b)) * fx;
  return s + (n - s) * fy;
}

// Splits a grid coordinate into a cell index and a fraction, keeping the cell
// inside the grid so the +1 neighbours exist.
// @ts-ignore: decorator
@inline
function cell(g: f32, n: i32): i32 {
  const i = <i32>g;
  return i > n - 2 ? n - 2 : i;
}

/**
 * Call once the inputs, the raster's grid coordinates and the terrain are in
 * place. Decodes the fixed-point inputs, reduces temperature to sea level so
 * it interpolates cleanly across terrain, fills unknown terrain from the
 * forecast grid's own elevations, and clears the accumulation.
 */
export function prepare(): void {
  const cells = nx * ny;
  for (let t = 0; t < nt; t++) {
    for (let c = 0; c < cells; c++) {
      const k = <usize>(t * cells + c);
      const p = load<i16>(pPrecipIn + (k << 1));
      const temp = load<i16>(pTempIn + (k << 1));
      store<f32>(pPrecip + (k << 2), p == MISSING ? 0 : <f32>p / 100);
      // A missing temperature becomes warm enough that nothing counts as snow.
      store<f32>(
        pSeaTemp + (k << 2),
        temp == MISSING ? 99 : <f32>temp / 100 + load<f32>(pElev + (<usize>c << 2)) * LAPSE
      );
    }
  }
  for (let y = 0; y < h; y++) {
    const gy = load<f32>(pGy + (<usize>y << 2));
    for (let x = 0; x < w; x++) {
      const px = <usize>(y * w + x) << 2;
      store<f32>(pAccum + px, 0);
      const gx = load<f32>(pGx + (<usize>x << 2));
      if (gx < 0 || gy < 0) continue;
      const dem = load<f32>(pDem + px);
      if (dem != dem) {
        const i = cell(gx, nx);
        const j = cell(gy, ny);
        store<f32>(pDem + px, bilerp(pElev, i, j, gx - <f32>i, gy - <f32>j));
      }
    }
  }
}

/** Adds (sign = 1) or removes (sign = -1) hour t's snowfall from the accumulation. */
export function accumulate(t: i32, sign: f32): void {
  const plane = (<usize>(t * nx * ny)) << 2;
  const precip = pPrecip + plane;
  const seaTemp = pSeaTemp + plane;
  for (let y = 0; y < h; y++) {
    const gy = load<f32>(pGy + (<usize>y << 2));
    if (gy < 0) continue;
    const j = cell(gy, ny);
    const fy = gy - <f32>j;
    for (let x = 0; x < w; x++) {
      const gx = load<f32>(pGx + (<usize>x << 2));
      if (gx < 0) continue;
      const i = cell(gx, nx);
      const fx = gx - <f32>i;
      const p = bilerp(precip, i, j, fx, fy);
      if (p <= 0) continue;
      const px = <usize>(y * w + x) << 2;
      const temp = bilerp(seaTemp, i, j, fx, fy) - load<f32>(pDem + px) * LAPSE;
      if (temp >= 2) continue;
      const cm = load<f32>(pAccum + px) + sign * p * snowFraction(temp) * snowCmPerMm(temp);
      store<f32>(pAccum + px, cm > 0 ? cm : 0);
    }
  }
}

/** Clears the accumulation. */
export function resetAccum(): void {
  memory.fill(pAccum, 0, <usize>(w * h) << 2);
}

/**
 * Draws hour t's precipitation rate. Opacity carries intensity. Snow and rain
 * each get their own colour (packed 0x00BBGGRR); rain is also hatched, so the
 * two stay apart for readers who can't tell the colours apart.
 */
export function renderRate(t: i32, snow: u32, rain: u32): void {
  const plane = (<usize>(t * nx * ny)) << 2;
  const precip = pPrecip + plane;
  const seaTemp = pSeaTemp + plane;
  for (let y = 0; y < h; y++) {
    const gy = load<f32>(pGy + (<usize>y << 2));
    const j = cell(gy, ny);
    const fy = gy - <f32>j;
    for (let x = 0; x < w; x++) {
      const px = <usize>(y * w + x) << 2;
      const gx = load<f32>(pGx + (<usize>x << 2));
      let alpha: f32 = 0;
      let rgb = snow;
      if (gx >= 0 && gy >= 0) {
        const i = cell(gx, nx);
        const fx = gx - <f32>i;
        const p = bilerp(precip, i, j, fx, fy);
        if (p >= 0.02) {
          // Fades in from nothing, so the edge of a shower is soft even where
          // the grid is coarse; 4 mm/h and above is fully inked.
          alpha = 0.9 * Mathf.min(1, Mathf.sqrt(p / 4));
          const temp = bilerp(seaTemp, i, j, fx, fy) - load<f32>(pDem + px) * LAPSE;
          if (snowFraction(temp) < 0.5) {
            rgb = rain;
            if (((x + y) & 7) > 2) alpha *= 0.45;
          }
        }
      }
      store<u32>(pOut + px, (rgb & 0xffffff) | (<u32>(alpha * 255) << 24));
    }
  }
}

/**
 * Draws the accumulation as stepped bands. `count` thresholds (cm, rising)
 * are read from levelsPtr() and one RGBA colour per band from palettePtr().
 * Anything below the first threshold is left clear.
 */
export function renderAccum(count: i32): void {
  const pixels = w * h;
  for (let k = 0; k < pixels; k++) {
    const px = <usize>k << 2;
    const cm = load<f32>(pAccum + px);
    let band = 0;
    while (band < count && cm >= load<f32>(pLevels + (<usize>band << 2))) band++;
    store<u32>(pOut + px, band == 0 ? 0 : load<u32>(pPalette + (<usize>(band - 1) << 2)));
  }
}

/** Evaluates hour t at one pixel; read the result with the sample* getters. */
export function sample(x: i32, y: i32, t: i32): bool {
  const gx = load<f32>(pGx + (<usize>x << 2));
  const gy = load<f32>(pGy + (<usize>y << 2));
  if (gx < 0 || gy < 0) return false;
  const plane = (<usize>(t * nx * ny)) << 2;
  const i = cell(gx, nx);
  const j = cell(gy, ny);
  const p = bilerp(pPrecip + plane, i, j, gx - <f32>i, gy - <f32>j);
  sTemp = bilerp(pSeaTemp + plane, i, j, gx - <f32>i, gy - <f32>j)
    - load<f32>(pDem + (<usize>(y * w + x) << 2)) * LAPSE;
  const sf = snowFraction(sTemp);
  sSnow = p * sf * snowCmPerMm(sTemp);
  sRain = p * (1 - sf);
  return true;
}

export function sampleSnowCm(): f32 { return sSnow; }
export function sampleRainMm(): f32 { return sRain; }
export function sampleTempC(): f32 { return sTemp; }
