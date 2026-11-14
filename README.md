# Precip Calendar

Ten-day storm outlooks for towns, mid-mountain and summits anywhere in the world.
It answers two questions: when is the next storm, and how likely is it?

## Run it

Two terminals. Needs Go 1.26+ and Node 22 with pnpm.

```sh
cd api && go run ./cmd/server        # API on :8787
cd web && pnpm install && pnpm dev   # UI on :5173, proxies /api to :8787
```

Single binary serving the built UI:

```sh
cd web && pnpm build
cd ../api && WEB_DIR=../web/build go run ./cmd/server   # everything on :8787
```

Tests: `cd api && go test ./...`, `cd web && pnpm check` and `cd web && pnpm test:wasm`.

| Variable      | Default        | Meaning                                              |
| ------------- | -------------- | ---------------------------------------------------- |
| `ADDR`        | `:8787`        | Listen address                                       |
| `HOT_TTL`     | `30m`          | How long a forecast stays in memory before archiving |
| `ARCHIVE_DIR` | `data/archive` | Where stale forecasts are written (gzip JSON)        |
| `FIELD_TTL`   | `3h`           | How long a map grid stays in memory (never archived) |
| `WEB_DIR`     | unset          | Built frontend to serve                              |

## How it works

```
web (SvelteKit SPA, Tailwind)  ──/api──▶  api (Go, Gin)
                                           ├─ provider   fetch raw ensemble members (Open-Meteo)
                                           ├─ terrain    choose elevation bands from a terrain ring
                                           ├─ compute    probabilities, storm windows, rain/snow
                                           └─ store      in-memory hot store → gzip archive on disk
```

**Data.** Every member of several ensemble models is fetched for the point: GEFS
(31 runs) and ECMWF (51) everywhere, plus ICON-EU in Europe and GEM in North
America. Open-Meteo is keyless; its free tier is for non-commercial use and
requires attribution (CC BY 4.0).

**Likelihood** is counted, not taken from a field: a storm that is "65% likely"
is one that 65% of the model runs produce.

**Storm windows** are stretches of hours where at least 35% of runs are wet,
merged across gaps of up to 6 hours. Each gets a likelihood, a 10th–90th
percentile total, per-model agreement and a snow line.

**Elevation bands.** Terrain is sampled at 80 points out to 10 km. A peak gets
Summit / Mid-mountain / Base; a town gets itself plus the high ground above it.
Pass `base` and `summit` (metres) to `/api/forecast` to set them yourself.

**Rain or snow** is worked out per run and per hour from temperature, moved to
each band with a 6.5 °C/km lapse rate: all snow at 0 °C or below, all rain at
2 °C or above, 10:1 snow depth rising to 15:1 in cold air.

**The map** sits on each place's page and has two views of the same forecast.
*Radar* plays precipitation hour by hour: snow in blue, rain in hatched green.
*Topo* adds up snowfall from now to the chosen hour, in ten tiers running
from pale blue to deep violet, and lays it over shaded terrain and contour
lines. Rain is left out of this view. Either view can show the average of the 51 ECMWF
runs or one deterministic run. Tapping the map opens the forecast for that spot.

```
/api/field ─▶ coarse grid of precipitation + temperature (binary, ≤144 points)
terrain tiles ─▶ elevation of every map pixel
        └──▶ WASM kernel: per pixel, per hour: interpolate, lapse the
             temperature to the pixel's height, apply the rain/snow rule
                 └──▶ RGBA raster ─▶ MapLibre canvas layer
```

The kernel is AssemblyScript in `web/wasm/kernel.ts`, compiled to
`web/src/lib/map/kernel.wasm`, which is committed so a plain `pnpm dev` works.
After editing the kernel run `pnpm wasm` (or `pnpm test:wasm`, which also
checks it). It repeats the rain/snow rule from `api/internal/compute`; both
are tested against `api/internal/compute/testdata/snow_vectors.json`.

Open-Meteo bills one call per grid point, against 600 a minute and 10,000 a
day. So the map asks for small grids snapped to a fixed lattice (0.25° when
zoomed in, coarser as you zoom out), the server keeps each for `FIELD_TTL`,
and a fresh view costs roughly 45 to 140 calls. The quota is shared with search
and the point forecasts, so heavy panning on a cold cache can hit the
per-minute limit and make all three fail until it resets.

Basemap: OpenFreeMap (OpenStreetMap data). Terrain: Mapzen Terrain Tiles on AWS.

### Limits worth knowing

- The models have a ~25 km grid (13 km for ICON-EU). All bands at a place share
  the same precipitation and differ only in temperature, so there is no
  orographic enhancement: big mountains usually get more than shown.
- The map is a forecast, not measured radar. Its rain/snow line follows real
  terrain, but the amount of precipitation is still the coarse grid's, and the
  ensemble average smooths out bursts that individual runs place differently.
- Derived bands are estimates from terrain, not stations or resort data.
- The archive is write-only for now. Nothing reads it back yet.

## API

- `GET /api/search?q=whistler` — towns, peaks and other named places.
- `GET /api/field?model=mean&step=0.5&i0=-248&j0=92&nx=9&ny=5` — a forecast grid
  for the map, as binary (layout documented in `api/internal/httpapi/field.go`).
  `model` is `mean` or `run`; `i0`/`j0` are the west/south edge in steps (even),
  `nx`/`ny` the size in points (odd, at most 144 in total).
- `GET /api/forecast?lat=46.85&lon=-121.76&name=Mount+Rainier&kind=MT&elevation=4392`
  — bands, days, storms and hourly series. `elevation`, `kind`, `base` and
  `summit` are optional.

## Not built yet

- Measured radar and satellite layers, and a likelihood map (share of runs
  with snow) to go with the amounts: that needs every member on a grid, which
  is too many upstream calls on the free tier.
- S3 archiver: implement `store.Archiver` next to `store.Disk`.
- Reading the archive back to show how a storm's forecast has changed over time.
- A second `provider.Provider` (national services, direct GRIB decoding).
- Saved places beyond the browser's local list, and alerts.
