<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { fetchForecast, placeParams, type Forecast, type Place } from '../lib/api';
	import DayStrip from '../lib/components/DayStrip.svelte';
	import HourlyChart from '../lib/components/HourlyChart.svelte';
	import SnowMap from '../lib/components/SnowMap.svelte';
	import Section from '../lib/components/Section.svelte';
	import Search from '../lib/components/Search.svelte';
	import StormCard from '../lib/components/StormCard.svelte';
	import { dayHour, elevation, modelName, pct, snowRange, precipRange } from '../lib/format';
	import { prefs, remember, setSystem } from '../lib/prefs.svelte';

	// Starting points for a first visit. Elevation is left to the terrain lookup.
	const suggestions: Place[] = [
		{ name: 'Mount Rainier', region: 'Washington', lat: 46.85283, lon: -121.76049, elevation: 4392, kind: 'MT' },
		{ name: 'Whistler', region: 'British Columbia', lat: 50.11817, lon: -122.95396, elevation: 673, kind: 'PPL' },
		{ name: 'Chamonix', region: 'France', lat: 45.9237, lon: 6.8694, kind: 'PPL' },
		{ name: 'Zermatt', region: 'Switzerland', lat: 46.0207, lon: 7.7491, kind: 'PPL' },
		{ name: 'Niseko', region: 'Japan', lat: 42.8048, lon: 140.6874, kind: 'PPL' },
		{ name: 'Alta', region: 'Utah', lat: 40.5888, lon: -111.638, kind: 'PPL' }
	];

	let data = $state<Forecast | null>(null);
	let loading = $state(false);
	let error = $state('');
	let bandId = $state('');

	// The URL is the source of truth for which place is shown, so links are shareable.
	const place = $derived.by((): Place | null => {
		const sp = page.url.searchParams;
		const lat = Number(sp.get('lat'));
		const lon = Number(sp.get('lon'));
		if (!sp.has('lat') || !sp.has('lon') || Number.isNaN(lat) || Number.isNaN(lon)) return null;
		return {
			name: sp.get('name') ?? `${lat.toFixed(3)}, ${lon.toFixed(3)}`,
			region: sp.get('region') ?? undefined,
			kind: sp.get('kind') ?? undefined,
			elevation: sp.has('elevation') ? Number(sp.get('elevation')) : undefined,
			lat,
			lon
		};
	});

	$effect(() => {
		const target = place;
		if (!target) {
			data = null;
			error = '';
			return;
		}
		const ctrl = new AbortController();
		loading = true;
		error = '';
		fetchForecast(target, ctrl.signal)
			.then((f) => {
				data = f;
				remember(target);
			})
			.catch((e) => {
				if (ctrl.signal.aborted) return;
				data = null;
				error = e instanceof Error ? e.message : 'Could not load the forecast';
			})
			.finally(() => {
				if (!ctrl.signal.aborted) loading = false;
			});
		return () => ctrl.abort();
	});

	function select(p: Place) {
		goto(`?${placeParams(p)}`);
	}

	const canLocate = typeof navigator !== 'undefined' && 'geolocation' in navigator;
	let locating = $state(false);
	let locateError = $state('');

	// Only ever called from a click, so the browser's permission prompt is the user's choice.
	function locate() {
		locating = true;
		locateError = '';
		navigator.geolocation.getCurrentPosition(
			(pos) => {
				locating = false;
				// Two decimals is about a kilometre: well inside one model grid cell, and it
				// keeps the exact position out of the URL and stops GPS jitter from filling
				// the recent list with near-duplicates.
				const lat = Math.round(pos.coords.latitude * 100) / 100;
				const lon = Math.round(pos.coords.longitude * 100) / 100;
				select({ name: 'My location', lat, lon });
			},
			(err) => {
				locating = false;
				locateError =
					err.code === err.PERMISSION_DENIED
						? 'Location sharing is turned off for this site. Allow it in your browser, or search for a place instead.'
						: 'Could not find your location. Try again, or search for a place instead.';
			},
			{ enableHighAccuracy: false, timeout: 10_000, maximumAge: 5 * 60_000 }
		);
	}

	const bandIndex = $derived(
		data ? Math.max(data.bands.findIndex((b) => b.id === bandId), 0) : 0
	);
	const bands = $derived(data?.bands ?? []);
	const band = $derived(data?.bands[bandIndex]);
	const headline = $derived(data?.storms.find((s) => s.likelihood >= 0.5));
	const totalMembers = $derived(data?.models.reduce((a, mdl) => a + mdl.members, 0) ?? 0);
</script>

<div class="mx-auto flex min-h-screen max-w-5xl flex-col gap-8 px-4 py-6 sm:px-6">
	<header class="flex flex-col gap-3 sm:flex-row sm:items-center">
		<a href="/" class="flex shrink-0 items-center gap-2 text-base font-semibold tracking-tight">
			<span class="grid h-7 w-7 place-items-center rounded-lg bg-(--accent) text-(--on-accent)">
				<svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
					<path d="M8 1.5c2.6 3.3 4.5 5.8 4.5 8.2a4.5 4.5 0 0 1-9 0c0-2.4 1.9-4.9 4.5-8.2Z" />
				</svg>
			</span>
			Pow Tracker
		</a>
		<div class="flex flex-1 items-center gap-3">
			<Search onselect={select} />
			<div
				class="flex shrink-0 rounded-xl border border-(--border) bg-(--surface) p-0.5 text-xs"
				role="group"
				aria-label="Units"
			>
				{#each [['metric', 'cm · °C'], ['imperial', 'in · °F']] as const as [sys, label] (sys)}
					<button
						type="button"
						aria-pressed={prefs.system === sys}
						onclick={() => setSystem(sys)}
						class={[
							'rounded-lg px-2.5 py-2 whitespace-nowrap',
							prefs.system === sys ? 'bg-(--surface-2) font-medium text-(--text)' : 'text-(--text-2)'
						]}
					>
						{label}
					</button>
				{/each}
			</div>
		</div>
	</header>

	{#if !place}
		<main class="flex flex-1 flex-col justify-center gap-8 pb-24">
			<div>
				<h1 class="max-w-xl text-3xl font-semibold tracking-tight sm:text-4xl">
					When is the next storm, and how sure are we?
				</h1>
				<p class="mt-3 max-w-xl text-(--text-2)">
					Ten-day storm outlooks for towns, mid-mountain and summits anywhere, built from every
					run of several ensemble weather models.
				</p>
			</div>
			{#each [['Recent', prefs.recent], ['Try', suggestions]] as const as [title, places] (title)}
				{#if places.length}
					<section>
						<h2 class="mb-2 text-xs font-medium tracking-wide text-(--text-2) uppercase">{title}</h2>
						<div class="flex flex-wrap gap-2">
							{#if title === 'Try' && canLocate}
								<button
									type="button"
									onclick={locate}
									disabled={locating}
									class="flex items-center gap-1.5 rounded-full border border-(--accent) bg-(--surface) px-4 py-2 text-sm disabled:opacity-60"
								>
									<svg
										width="14"
										height="14"
										viewBox="0 0 16 16"
										fill="none"
										stroke="currentColor"
										stroke-width="1.5"
										stroke-linecap="round"
										aria-hidden="true"
									>
										<circle cx="8" cy="8" r="4.5" />
										<circle cx="8" cy="8" r="1" fill="currentColor" />
										<path d="M8 1v2.5M8 12.5V15M1 8h2.5M12.5 8H15" />
									</svg>
									{locating ? 'Locating…' : 'Use my location'}
								</button>
							{/if}
							{#each places as p (`${p.lat},${p.lon}`)}
								<button
									type="button"
									onclick={() => select(p)}
									class="rounded-full border border-(--border) bg-(--surface) px-4 py-2 text-sm hover:border-(--accent)"
								>
									{p.name}
									{#if p.region}<span class="text-(--text-2)">· {p.region}</span>{/if}
								</button>
							{/each}
						</div>
						{#if title === 'Try'}
							<p class="mt-2 text-sm text-(--text-2)" aria-live="polite">{locateError}</p>
						{/if}
					</section>
				{/if}
			{/each}
		</main>
	{:else if error}
		<main class="rounded-2xl border border-(--border) bg-(--surface) p-6">
			<h1 class="text-lg font-semibold">Couldn't load {place.name}</h1>
			<p class="mt-1 text-sm text-(--text-2)">{error}</p>
			<button
				type="button"
				class="mt-4 rounded-lg bg-(--accent) px-4 py-2 text-sm font-medium text-(--on-accent)"
				onclick={() => location.reload()}
			>
				Try again
			</button>
		</main>
	{:else if !data || !band}
		<main class="animate-pulse space-y-4" aria-busy="true" aria-label="Loading forecast">
			<div class="h-10 w-64 rounded-lg bg-(--surface-2)"></div>
			<div class="h-48 rounded-2xl bg-(--surface-2)"></div>
			<div class="h-56 rounded-2xl bg-(--surface-2)"></div>
		</main>
	{:else}
		<main class={['flex flex-col gap-8', loading && 'opacity-60']}>
			<section>
				<div>
					<h1 class="text-3xl font-semibold tracking-tight">{place.name}</h1>
					<p class="mt-1 text-sm text-(--text-2)">
						{#if place.region}{place.region} ·{/if}
						{#if headline}
							{@const at = headline.bands[bandIndex]}
							{headline.underway ? 'Storm underway' : `Next storm ${dayHour(headline.start)}`},
							{pct(headline.likelihood)} likely,
							{at.kind !== 'rain' && at.snowCm.p90 >= 1
								? `${snowRange(at.snowCm, prefs.system)} of snow`
								: precipRange(headline.precipMm, prefs.system)}
						{:else}
							No storm is more likely than not in the next {data.days.length} days
						{/if}
					</p>
				</div>

			</section>

			<Section id="storms" title="Storms">
				{#snippet actions()}
					{#if bands.length > 1}
						<div
							class="flex rounded-xl border border-(--border) bg-(--surface) p-1"
							role="group"
							aria-label="Elevation"
						>
							{#each bands as b, i (b.id)}
								<button
									type="button"
									aria-pressed={i === bandIndex}
									onclick={() => (bandId = b.id)}
									class={[
										'rounded-lg px-3 py-1.5 text-left',
										i === bandIndex ? 'bg-(--accent) text-(--on-accent)' : 'text-(--text-2)'
									]}
								>
									<span class="block text-sm font-medium">{b.label}</span>
									<span class="tabular block text-xs opacity-80">
										{elevation(b.elevation, prefs.system)}
									</span>
								</button>
							{/each}
						</div>
					{:else}
						<p class="tabular text-sm text-(--text-2)">{band ? elevation(band.elevation, prefs.system) : ''}</p>
					{/if}
				{/snippet}
				{#if data.storms.length}
					<div class="flex flex-wrap gap-3">
						{#each data.storms as storm (storm.id)}
							<StormCard {storm} {bandIndex} {band} system={prefs.system} />
						{/each}
					</div>
				{:else}
					<p class="rounded-2xl border border-(--border) bg-(--surface) p-5 text-sm text-(--text-2)">
						Nothing organised on the horizon. See the daily chances below for stray showers.
					</p>
				{/if}
			</Section>

			<Section id="map" title="Map" note="tap anywhere to forecast that spot">
				{#key `${data.location.lat},${data.location.lon}`}
					<SnowMap
						lat={data.location.lat}
						lon={data.location.lon}
						utcOffsetSeconds={data.location.utcOffsetSeconds}
						system={prefs.system}
						onpick={(lat, lon) => select({ name: `${lat.toFixed(3)}, ${lon.toFixed(3)}`, lat, lon })}
					/>
				{/key}
			</Section>

			<Section
				id="days"
				title="Daily chance of precipitation"
				note="amounts at {band.label.toLowerCase()}"
			>
				<DayStrip days={data.days} storms={data.storms} {bandIndex} system={prefs.system} />
			</Section>

			<Section id="hourly" title="Hour by hour" note="share of model runs that are wet">
				<div class="rounded-2xl border border-(--border) bg-(--surface) p-3">
					<HourlyChart
						hourly={data.hourly}
						storms={data.storms}
						{bandIndex}
						system={prefs.system}
					/>
				</div>
			</Section>

			<footer class="space-y-1 border-t border-(--border) pt-4 text-xs text-(--text-2)">
				{#each data.notes as note (note)}
					<p>{note}</p>
				{/each}
				<p>
					{totalMembers} model runs:
					{data.models.map((mdl) => `${modelName(mdl.name)} ${mdl.members}`).join(', ')}. Ranges
					span the 10th to 90th percentile.
					{#if data.bands.some((b) => b.modelled)}
						Mid-mountain and other derived elevations are modelled from nearby terrain. All
						elevations share the same precipitation and differ only in temperature, so snow
						amounts are estimates.
					{/if}
				</p>
				<p>{data.source.attribution}. Times are local to {data.location.timezone}.</p>
			</footer>
		</main>
	{/if}
</div>
