<script lang="ts">
	import { searchPlaces, type Place } from '../api';
	import { elevation } from '../format';
	import { prefs } from '../prefs.svelte';

	let { onselect }: { onselect: (place: Place) => void } = $props();

	let query = $state('');
	let results = $state<Place[]>([]);
	let open = $state(false);
	let active = $state(0);
	let failed = $state('');
	let searching = $state(false);

	const peakKinds = new Set(['MT', 'MTS', 'PK', 'PKS', 'VLC', 'RDGE', 'HLL', 'GLCR', 'PASS']);
	const kindLabel = (kind?: string) =>
		!kind ? '' : peakKinds.has(kind) ? 'Peak' : kind.startsWith('PPL') ? 'Town' : 'Place';

	$effect(() => {
		const q = query.trim();
		if (q.length < 2) {
			results = [];
			failed = '';
			return;
		}
		const ctrl = new AbortController();
		const timer = setTimeout(async () => {
			searching = true;
			try {
				results = await searchPlaces(q, ctrl.signal);
				active = 0;
				failed = '';
			} catch (e) {
				if (ctrl.signal.aborted) return;
				results = [];
				failed = e instanceof Error ? e.message : 'Search failed';
			} finally {
				if (!ctrl.signal.aborted) searching = false;
			}
		}, 220);
		return () => {
			clearTimeout(timer);
			ctrl.abort();
		};
	});

	function choose(place: Place) {
		open = false;
		query = '';
		onselect(place);
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') open = false;
		if (!results.length) return;
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			active = (active + 1) % results.length;
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			active = (active - 1 + results.length) % results.length;
		} else if (e.key === 'Enter') {
			e.preventDefault();
			choose(results[active]);
		}
	}
</script>

<div class="relative w-full">
	<svg
		class="pointer-events-none absolute top-1/2 left-3.5 -translate-y-1/2 text-(--muted)"
		width="16"
		height="16"
		viewBox="0 0 16 16"
		fill="none"
		stroke="currentColor"
		stroke-width="1.6"
		stroke-linecap="round"
		aria-hidden="true"
	>
		<circle cx="7" cy="7" r="4.5" />
		<path d="m10.5 10.5 3.5 3.5" />
	</svg>
	<input
		type="search"
		bind:value={query}
		onfocus={() => (open = true)}
		onblur={() => setTimeout(() => (open = false), 120)}
		oninput={() => (open = true)}
		{onkeydown}
		placeholder="Search a town, resort or summit"
		aria-label="Search for a place"
		autocomplete="off"
		class="w-full rounded-xl border border-(--border) bg-(--surface) py-2.5 pr-4 pl-10 text-sm text-(--text) outline-none placeholder:text-(--muted) focus:border-(--accent) focus:ring-2 focus:ring-(--accent-soft)"
	/>

	{#if open && query.trim().length >= 2}
		<ul
			class="absolute z-20 mt-2 max-h-80 w-full overflow-auto rounded-xl border border-(--border) bg-(--surface) p-1 shadow-xl"
			role="listbox"
		>
			{#each results as place, i (place.id ?? `${place.lat},${place.lon}`)}
				<li role="option" aria-selected={i === active}>
					<button
						type="button"
						class={[
							'flex w-full items-baseline justify-between gap-3 rounded-lg px-3 py-2 text-left',
							i === active && 'bg-(--surface-2)'
						]}
						onmousedown={(e) => e.preventDefault()}
						onmouseenter={() => (active = i)}
						onclick={() => choose(place)}
					>
						<span class="min-w-0">
							<span class="block truncate text-sm font-medium">{place.name}</span>
							<span class="block truncate text-xs text-(--text-2)">
								{[place.region, place.country].filter(Boolean).join(', ')}
							</span>
						</span>
						<span class="tabular shrink-0 text-right text-xs text-(--text-2)">
							{kindLabel(place.kind)}
							{#if place.elevation != null}
								· {elevation(place.elevation, prefs.system)}
							{/if}
						</span>
					</button>
				</li>
			{:else}
				<li class="px-3 py-2 text-sm text-(--text-2)">
					{failed || (searching ? 'Searching…' : 'No places found')}
				</li>
			{/each}
		</ul>
	{/if}
</div>
