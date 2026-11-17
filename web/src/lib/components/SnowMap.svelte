<script lang="ts" module>
	const DEM_TILES = 'https://s3.amazonaws.com/elevation-tiles-prod/terrarium/{z}/{x}/{y}.png';
	const DEM_MAX_ZOOM = 12;

	// Loaded once for the page: the map library, and one decoded-terrain cache
	// that feeds the hillshade, the contours and the kernel alike.
	let libs: ReturnType<typeof loadLibs> | undefined;
	async function loadLibs() {
		const [{ default: maplibregl }, { default: mlcontour }] = await Promise.all([
			import('maplibre-gl'),
			import('maplibre-contour'),
			import('maplibre-gl/dist/maplibre-gl.css')
		]);
		const dem = new mlcontour.DemSource({
			url: DEM_TILES,
			encoding: 'terrarium',
			maxzoom: DEM_MAX_ZOOM,
			worker: true
		});
		dem.setupMaplibre(maplibregl);
		return { maplibregl, dem };
	}

	// The view and forecast chosen last, kept while moving from place to place.
	const remembered: { mode: 'radar' | 'topo'; model: 'mean' | 'run'; speed: number } = {
		mode: 'radar',
		model: 'mean',
		speed: 1
	};
</script>

<script lang="ts">
	import type { Map as MapLibre, CanvasSource, VectorTileSource, MapMouseEvent } from 'maplibre-gl';
	import { onMount } from 'svelte';
	import { hour, monthDay, weekday, elevation, precipAmount, snowAmount, snowUnit, temp, type System } from '../format';
	import { fetchField, gridFor, type Bounds, type Field, type FieldModel } from '../map/field';
	import { SnowKernel, type RGB } from '../map/kernel';
	import { buildRaster, type DemTiles } from '../map/raster';
	import { bandColours, bandEdges } from '../map/bands';

	let {
		lat,
		lon,
		utcOffsetSeconds,
		system,
		onpick
	}: {
		lat: number;
		lon: number;
		/** Times are shown on the forecast place's clock, like the rest of the page. */
		utcOffsetSeconds: number;
		system: System;
		onpick: (lat: number, lon: number) => void;
	} = $props();

	type Mode = 'radar' | 'topo';

	const MAX_RASTER_WIDTH = 720;
	/** One forecast hour per frame at 1×. */
	const FRAME_MS = 110;
	const SPEEDS = [0.25, 0.5, 1, 2, 4];

	const dark = typeof matchMedia !== 'undefined' && matchMedia('(prefers-color-scheme: dark)').matches;

	// Radar: snow keeps the app's blue; rain is green so the two can't be confused
	// (and hatched, for anyone who can't tell blue from green).
	const ink: { snow: RGB; rain: RGB } = dark
		? { snow: [86, 160, 245], rain: [70, 190, 90] }
		: { snow: [36, 108, 200], rain: [0, 131, 0] };

	// Topo: bands scaled to the biggest total on the map, finer at the low end
	// and coarser at the top (see map/bands.ts).
	const UNIT_CM = { metric: 1, imperial: 2.54 };
	const bands = $derived.by(() => {
		const edges = bandEdges(peakCm / UNIT_CM[system], system);
		const colours = bandColours(edges.length, dark);
		return edges.map((v, i) => ({
			cm: v * UNIT_CM[system],
			label: String(v),
			colour: colours[i],
			// Slightly see-through so the terrain shading still reads underneath.
			alpha: 0.68 + (0.26 * i) / Math.max(edges.length - 1, 1)
		}));
	});
	const css = (c: RGB, alpha = 1) => `rgb(${c.join(' ')} / ${alpha})`;

	let container: HTMLDivElement;
	let mode = $state<Mode>(remembered.mode);
	let model = $state<FieldModel>(remembered.model);
	let t = $state(0);
	let playing = $state(false);
	let busy = $state(true);
	let error = $state('');
	let field = $state.raw<Field | null>(null);
	let kernel = $state.raw<SnowKernel | null>(null);
	let peakCm = $state(0);
	let speed = $state(remembered.speed);
	// A point on the map, with where it currently sits in the map's box (CSS px).
	type Spot = { lat: number; lon: number; x: number; y: number };
	let probe = $state<Spot | null>(null);
	let picked = $state<Spot | null>(null);
	let box = $state({ width: 0, height: 0 });
	let frame = $state({ width: 0, height: 0 });

	// Not reactive: the map and what is currently drawn on it.
	let map: MapLibre | undefined;
	let demTiles: DemTiles | undefined;
	let contourTiles: ((system: System) => string) | undefined;
	let view: (Bounds & { width: number; height: number }) | undefined;
	const canvas = document.createElement('canvas');
	canvas.width = canvas.height = 2;
	const ctx = canvas.getContext('2d')!;
	let generation = 0;

	const from = $derived(kernel?.from ?? 0);
	const last = $derived(Math.max((kernel?.steps ?? 1) - 1, from));
	// Ten days is long enough for a weekday to come round twice, so show the date.
	const timeAt = (step: number) => {
		if (!field) return '';
		const at = new Date((field.start + step * 3600 + utcOffsetSeconds) * 1000).toISOString().slice(0, 16);
		return `${weekday(at)} ${monthDay(at)}, ${hour(at)}`;
	};

	const mercY = (latDeg: number) => Math.log(Math.tan(Math.PI / 4 + (latDeg * Math.PI) / 360));

	// What the forecast says under the pointer (or the last tap).
	const reading = $derived.by(() => {
		if (!kernel || !view || !probe) return null;
		const x = Math.floor(((probe.lon - view.west) / (view.east - view.west)) * view.width);
		const y = Math.floor(
			((mercY(view.north) - mercY(probe.lat)) / (mercY(view.north) - mercY(view.south))) * view.height
		);
		return kernel.at(x, y, t);
	});

	/** Refits the raster to the current view and loads whatever it needs. */
	async function refresh() {
		if (!map || !demTiles || !container) return;
		const mine = ++generation;
		const { clientWidth: w, clientHeight: h } = container;
		if (!w || !h) return;
		// A margin around the view, so short pans don't show a bare edge.
		const pad = 0.12;
		const nw = map.unproject([-w * pad, -h * pad]);
		const se = map.unproject([w * (1 + pad), h * (1 + pad)]);
		const bounds: Bounds = {
			west: nw.lng,
			east: se.lng,
			north: Math.min(nw.lat, 85),
			south: Math.max(se.lat, -85)
		};
		const scale = Math.min(0.5, MAX_RASTER_WIDTH / (w * (1 + 2 * pad)));
		const width = Math.round(w * (1 + 2 * pad) * scale);
		const height = Math.round(h * (1 + 2 * pad) * scale);
		// Terrain tiles at about one tile pixel per raster pixel.
		const demZoom = Math.min(Math.max(Math.floor(map.getZoom() + Math.log2(2 * scale) + 0.3), 0), DEM_MAX_ZOOM);
		const grid = gridFor(bounds);

		busy = true;
		try {
			const [loaded, raster] = await Promise.all([
				fetchField(model, grid),
				buildRaster(bounds, width, height, grid, demZoom, demTiles)
			]);
			if (mine !== generation) return;
			const now = Math.floor((Date.now() / 1000 - loaded.start) / 3600);
			const next = await SnowKernel.create(loaded, raster, now);
			if (mine !== generation) return;

			canvas.width = width;
			canvas.height = height;
			view = { ...bounds, width, height };
			const corners: [number, number][] = [
				[bounds.west, bounds.north],
				[bounds.east, bounds.north],
				[bounds.east, bounds.south],
				[bounds.west, bounds.south]
			];
			const source = map.getSource<CanvasSource>('snow');
			if (source) {
				source.setCoordinates(corners as never);
			} else {
				map.addSource('snow', { type: 'canvas', canvas, coordinates: corners as never, animate: false });
				// Under the place names, over everything else.
				const firstLabel = map.getStyle().layers.find((l) => l.type === 'symbol')?.id;
				map.addLayer(
					{ id: 'snow', type: 'raster', source: 'snow', paint: { 'raster-fade-duration': 0 } },
					firstLabel
				);
			}
			if (!kernel || t < next.from || t > next.steps - 1) t = next.from;
			field = loaded;
			peakCm = next.peakCm();
			kernel = next;
			error = '';
		} catch (e) {
			if (mine !== generation) return;
			error = e instanceof Error ? e.message : 'Could not load the map data';
		} finally {
			if (mine === generation) busy = false;
		}
	}

	// Redraw whenever the hour, the mode, the units or the data change.
	$effect(() => {
		if (!kernel || !map) return;
		const image =
			mode === 'radar'
				? kernel.rate(t, ink.snow, ink.rain)
				: kernel.accumulation(t, bands);
		ctx.putImageData(image, 0, 0);
		// A paused canvas source re-reads its canvas only while "playing".
		const source = map.getSource<CanvasSource>('snow');
		if (!source) return;
		source.play();
		map.once('render', () => source.pause());
		map.triggerRepaint();
	});

	$effect(() => {
		if (!playing) return;
		const timer = setInterval(() => (t = t >= last ? from : t + 1), FRAME_MS / speed);
		return () => clearInterval(timer);
	});

	// Terrain shading and contours belong to the topo view only.
	$effect(() => {
		const visibility = mode === 'topo' ? 'visible' : 'none';
		for (const id of ['hillshade', 'contour-lines', 'contour-labels']) {
			if (map?.getLayer(id)) map.setLayoutProperty(id, 'visibility', visibility);
		}
	});

	$effect(() => {
		const tiles = contourTiles?.(system);
		if (tiles) map?.getSource<VectorTileSource>('contours')?.setTiles([tiles]);
	});

	function setMode(next: Mode) {
		// The topo view opens on the full total rather than an empty first hour.
		if (next === 'topo' && mode !== 'topo' && t === from && !playing) t = last;
		if (next === 'radar' && mode !== 'radar' && t === last && !playing) t = from;
		mode = remembered.mode = next;
	}

	function setModel(next: FieldModel) {
		if (next === model) return;
		model = remembered.model = next;
		refresh();
	}

	onMount(() => {
		let disposed = false;
		let moveTimer: ReturnType<typeof setTimeout>;
		let dispose = () => {};
		(async () => {
			const { maplibregl, dem } = await (libs ??= loadLibs());
			if (disposed) return;
			demTiles = (z, x, y) => dem.getDemTile(z, x, y);
			contourTiles = (sys) =>
				dem.contourProtocolUrl({
					multiplier: sys === 'metric' ? 1 : 3.28084,
					// zoom: [minor, major] interval, in the unit shown
					thresholds:
						sys === 'metric'
							? { 7: [500, 2500], 9: [250, 1000], 10: [100, 500], 12: [50, 250] }
							: { 7: [2000, 10000], 9: [1000, 5000], 10: [500, 2000], 12: [200, 1000] },
					elevationKey: 'ele',
					levelKey: 'level',
					contourLayer: 'contours'
				});

			const m = new maplibregl.Map({
				container,
				style: `https://tiles.openfreemap.org/styles/${dark ? 'dark' : 'positron'}`,
				center: [lon, lat],
				// Roughly 65 km across on a desktop-width map at mid-latitudes.
				zoom: 9.6,
				minZoom: 6,
				maxZoom: 12,
				// The raster is fitted to an upright, flat view.
				// The map sits mid-page: plain scrolling should scroll the page, and
				// every zoom can cost an upstream fetch.
				cooperativeGestures: true,
				dragRotate: false,
				pitchWithRotate: false,
				touchPitch: false,
				renderWorldCopies: false,
				attributionControl: {
					compact: true,
					customAttribution: [
						'Forecast: <a href="https://open-meteo.com/">Open-Meteo</a> (CC BY 4.0)',
						'Terrain: <a href="https://github.com/tilezen/joerd/blob/master/docs/attribution.md">Mapzen Terrain Tiles</a>'
					]
				}
			});
			m.touchZoomRotate.disableRotation();
			m.keyboard.disableRotation();
			m.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right');
			const here = new maplibregl.Marker({ color: css(ink.snow) })
				.setLngLat([lon, lat])
				.addTo(m);
			here.getElement().setAttribute('aria-label', 'Forecast location');

			m.on('load', () => {
				if (disposed) return;
				const firstLabel = m.getStyle().layers.find((l) => l.type === 'symbol')?.id;
				const hidden = { visibility: mode === 'topo' ? 'visible' : 'none' } as const;
				m.addSource('dem', {
					type: 'raster-dem',
					encoding: 'terrarium',
					tiles: [dem.sharedDemProtocolUrl],
					maxzoom: DEM_MAX_ZOOM,
					tileSize: 256
				});
				m.addSource('contours', { type: 'vector', tiles: [contourTiles!(system)], maxzoom: 14 });
				m.addLayer(
					{
						id: 'hillshade',
						type: 'hillshade',
						source: 'dem',
						layout: hidden,
						paint: {
							'hillshade-exaggeration': 0.45,
							'hillshade-shadow-color': dark ? '#000000' : '#5a5f66',
							'hillshade-highlight-color': dark ? '#3a4250' : '#ffffff'
						}
					},
					firstLabel
				);
				m.addLayer(
					{
						id: 'contour-lines',
						type: 'line',
						source: 'contours',
						'source-layer': 'contours',
						layout: hidden,
						paint: {
							'line-color': dark ? 'rgb(240 239 236 / 0.28)' : 'rgb(11 11 11 / 0.26)',
							'line-width': ['match', ['get', 'level'], 1, 1, 0.4]
						}
					},
					firstLabel
				);
				m.addLayer(
					{
						id: 'contour-labels',
						type: 'symbol',
						source: 'contours',
						'source-layer': 'contours',
						filter: ['>', ['get', 'level'], 0],
						layout: {
							...hidden,
							'symbol-placement': 'line',
							'text-size': 10,
							'text-field': ['number-format', ['get', 'ele'], {}],
							'text-font': ['Noto Sans Regular']
						},
						paint: {
							'text-color': dark ? '#c3c2b7' : '#52514e',
							'text-halo-color': dark ? '#0d1015' : '#ffffff',
							'text-halo-width': 1
						}
					},
					firstLabel
				);
				map = m;
				refresh();
			});
			m.on('moveend', () => {
				// Each new view can cost an upstream fetch, so wait for the map to settle.
				clearTimeout(moveTimer);
				moveTimer = setTimeout(refresh, 350);
			});
			const spot = (e: MapMouseEvent): Spot => ({ lat: e.lngLat.lat, lon: e.lngLat.lng, x: e.point.x, y: e.point.y });
			m.on('mousemove', (e: MapMouseEvent) => (probe = spot(e)));
			m.on('mouseout', () => (probe = picked));
			// Keep a tapped point's readout pinned to it while the map moves.
			m.on('move', () => {
				if (!picked) return;
				const at = m.project([picked.lon, picked.lat]);
				const moved = { ...picked, x: at.x, y: at.y };
				if (probe === picked) probe = moved;
				picked = moved;
			});
			m.on('click', (e: MapMouseEvent) => {
				picked = probe = spot(e);
			});
			new ResizeObserver(() => m.resize()).observe(container);
			dispose = () => m.remove();
		})().catch((e) => {
			error = e instanceof Error ? e.message : 'Could not start the map';
			busy = false;
		});
		return () => {
			disposed = true;
			generation++;
			map = undefined; // a late map event must not refresh a removed map
			clearTimeout(moveTimer);
			dispose();
		};
	});

	const toggle = 'rounded-lg px-2.5 py-1.5 whitespace-nowrap';
	const on = 'bg-(--surface-2) font-medium text-(--text)';
	const off = 'text-(--text-2)';
</script>

<div class="overflow-hidden rounded-2xl border border-(--border) bg-(--surface)">
	<div class="flex flex-wrap items-center gap-2 border-b border-(--border) p-2 text-xs">
		<div class="flex rounded-xl border border-(--border) p-0.5" role="group" aria-label="Map view">
			{#each [['radar', 'Radar'], ['topo', 'Topo']] as const as [id, label] (id)}
				<button type="button" aria-pressed={mode === id} onclick={() => setMode(id)} class={[toggle, mode === id ? on : off]}>
					{label}
				</button>
			{/each}
		</div>
		<div class="flex rounded-xl border border-(--border) p-0.5" role="group" aria-label="Forecast shown">
			{#each [['mean', 'Average of 51 runs'], ['run', 'Single run']] as const as [id, label] (id)}
				<button type="button" aria-pressed={model === id} onclick={() => setModel(id)} class={[toggle, model === id ? on : off]}>
					{label}
				</button>
			{/each}
		</div>
		<p class="ml-auto text-(--text-2)" aria-live="polite">
			{#if error}
				{error}
			{:else if busy}
				Loading…
			{:else if mode === 'radar'}
				Forecast precipitation, hour by hour
			{:else}
				Snowfall adding up from now, on real terrain
			{/if}
		</p>
	</div>

	<div class="relative">
		<div
			bind:this={container}
			bind:clientWidth={frame.width}
			bind:clientHeight={frame.height}
			class="h-[26rem] w-full sm:h-[30rem]"
		></div>

		{#if reading && probe}
			<!-- Follows the pointer (or stays on the last tap); flips at the edges. -->
			<div
				bind:clientWidth={box.width}
				bind:clientHeight={box.height}
				class="tabular pointer-events-none absolute z-10 rounded-lg border border-(--border) bg-(--surface) px-3 py-2 text-xs whitespace-nowrap shadow-lg"
				style:left="{probe.x + 14 + box.width > frame.width ? probe.x - 14 - box.width : probe.x + 14}px"
				style:top="{probe.y + 14 + box.height > frame.height ? probe.y - 14 - box.height : probe.y + 14}px"
			>
				{#if mode === 'topo'}
					<p class="text-base font-semibold">{snowAmount(reading.totalCm, system)}</p>
					<p class="text-(--text-2)">of snow by {timeAt(t)}</p>
					{#if t < last}
						<p class="text-(--text-2)">
							<span class="font-medium text-(--text)">{snowAmount(reading.runTotalCm, system)}</span>
							by the end, {timeAt(last)}
						</p>
					{/if}
				{:else if reading.snowCm + reading.rainMm < 0.05}
					<p class="text-base font-semibold">Dry</p>
					<p class="text-(--text-2)">{temp(reading.tempC, system)} at {timeAt(t)}</p>
				{:else if reading.snowCm >= reading.rainMm}
					<p class="text-base font-semibold">Snow, {snowAmount(reading.snowCm, system)}/h</p>
					<p class="text-(--text-2)">{temp(reading.tempC, system)} at {timeAt(t)}</p>
				{:else}
					<p class="text-base font-semibold">Rain, {precipAmount(reading.rainMm, system)}/h</p>
					<p class="text-(--text-2)">{temp(reading.tempC, system)} at {timeAt(t)}</p>
				{/if}
				<p class="mt-1 border-t border-(--border) pt-1 text-(--text-2)">
					Elevation <span class="font-medium text-(--text)">{elevation(reading.elevation, system)}</span>
				</p>
			</div>
		{/if}

		{#if picked}
			<button
				type="button"
				onclick={() => picked && onpick(Number(picked.lat.toFixed(4)), Number(picked.lon.toFixed(4)))}
				class="tabular absolute bottom-8 left-1/2 -translate-x-1/2 rounded-full bg-(--accent) px-4 py-2 text-xs font-medium text-(--on-accent) shadow-lg"
			>
				Open the forecast for {picked.lat.toFixed(2)}, {picked.lon.toFixed(2)}
			</button>
		{/if}
	</div>

	<div class="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-(--border) p-3 text-xs">
		<div class="flex min-w-64 flex-1 items-center gap-3">
			<button
				type="button"
				onclick={() => (playing = !playing)}
				disabled={!kernel}
				aria-label={playing ? 'Pause' : 'Play'}
				class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-(--accent) text-(--on-accent) disabled:opacity-40"
			>
				<svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true">
					{#if playing}
						<path d="M2 1h3v10H2zM7 1h3v10H7z" />
					{:else}
						<path d="M2.5 1 11 6l-8.5 5z" />
					{/if}
				</svg>
			</button>
			<input
				type="range"
				min={from}
				max={last}
				step="1"
				bind:value={t}
				disabled={!kernel}
				aria-label="Forecast time"
				aria-valuetext={timeAt(t)}
				class="min-w-0 flex-1 accent-(--accent)"
			/>
			<span class="tabular w-44 shrink-0 font-medium">
				{#if kernel}{mode === 'topo' ? 'Through ' : ''}{timeAt(t)}{/if}
			</span>
		</div>

		<div class="flex rounded-xl border border-(--border) p-0.5" role="group" aria-label="Playback speed">
			{#each SPEEDS as s (s)}
				<button
					type="button"
					aria-pressed={speed === s}
					onclick={() => (speed = remembered.speed = s)}
					class={['tabular rounded-lg px-2 py-1 whitespace-nowrap', speed === s ? on : off]}
				>
					{s === 0.25 ? '¼' : s === 0.5 ? '½' : s}×
				</button>
			{/each}
		</div>

		<div class="flex flex-wrap items-center gap-2 text-(--text-2)">
			{#if mode === 'topo'}
				<span>Snow, {snowUnit(system)}</span>
				<div class="flex">
					{#each bands as b (b.cm)}
						<span class="tabular w-6">
							<span class="block h-3" style:background={css(b.colour, b.alpha)}></span>
							<span class="block pt-0.5 leading-none">{b.label}</span>
						</span>
					{/each}
				</div>
			{:else}
				<span class="flex items-center gap-1.5">
					<span class="h-4 w-6 rounded-sm" style:background={css(ink.snow)}></span> Snow
				</span>
				<span class="flex items-center gap-1.5">
					<span
						class="h-4 w-6 rounded-sm"
						style:background="repeating-linear-gradient(135deg, {css(ink.rain)} 0 3px, {css(ink.rain, 0.45)} 3px 8px)"
					></span>
					Rain
				</span>
				<span class="flex items-center gap-1.5">
					Light
					<span class="h-4 w-14 rounded-sm" style:background="linear-gradient(90deg, {css(ink.snow, 0.08)}, {css(ink.snow)})"></span>
					Heavy
				</span>
			{/if}
		</div>
	</div>

	<p class="border-t border-(--border) px-3 py-2 text-xs text-(--text-2)">
		This is a forecast drawn like radar, not measured radar.
		{model === 'mean'
			? 'It shows the average of the 51 ECMWF runs, which is the best single guess at totals but smooths out where and when the heaviest bursts fall.'
			: 'It shows one model run: a sharp, plausible picture, but only one of many possible outcomes.'}
		Rain or snow is decided at each spot from its real terrain height. The amount of precipitation
		still comes from a grid of about 25 km, so peaks usually get more than shown.
	</p>
</div>
