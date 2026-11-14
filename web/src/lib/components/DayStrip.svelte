<script lang="ts">
	import type { Day, Storm } from '../api';
	import { dayHour, dayNum, pct, precipRange, snowRange, temp, weekday, type System } from '../format';
	import KindIcon from './KindIcon.svelte';

	let {
		days,
		storms,
		bandIndex,
		system
	}: { days: Day[]; storms: Storm[]; bandIndex: number; system: System } = $props();

	// Position of a wall-clock time along the strip, 0..1. Each column is a
	// whole calendar day, even when today is already partly over.
	function position(time: string): number {
		const i = days.findIndex((d) => d.date === time.slice(0, 10));
		if (i < 0) return time.slice(0, 10) < days[0].date ? 0 : 1;
		return (i + Number(time.slice(11, 13)) / 24) / days.length;
	}

	const spans = $derived(
		storms.map((s) => {
			const left = position(s.start);
			const right = s.openEnded ? 1 : position(s.end);
			return { storm: s, left, width: Math.max(right - left, 0.012) };
		})
	);
</script>

<div class="overflow-x-auto">
	<div class="min-w-[680px]">
		<!-- Storm windows drawn across the days they cover -->
		<div class="relative mb-2 h-6" aria-hidden="true">
			{#each spans as { storm, left, width } (storm.id)}
				<div
					class="absolute top-0 flex h-6 items-center overflow-hidden rounded-md bg-(--accent) px-2 text-xs font-medium whitespace-nowrap text-(--on-accent)"
					style:left="calc({left * 100}% + 1px)"
					style:width="calc({width * 100}% - 2px)"
					style:opacity={0.45 + 0.55 * storm.likelihood}
					title="{dayHour(storm.start)} to {dayHour(storm.end)}, {pct(storm.likelihood)} likely"
				>
					{#if width > 0.09}{pct(storm.likelihood)} likely{/if}
				</div>
			{/each}
		</div>

		<ol class="grid gap-1.5" style:grid-template-columns="repeat({days.length}, minmax(0, 1fr))">
			{#each days as day, i (day.date)}
				{@const band = day.bands[bandIndex]}
				{@const wet = day.precipProb >= 0.2}
				{@const snowy = band.kind !== 'rain' && band.snowCm.p90 >= 1}
				<li
					class="flex flex-col items-center gap-2 rounded-xl border border-(--border) bg-(--surface) px-1 py-3 text-center"
				>
					<div>
						<p class="text-xs text-(--text-2)">{i === 0 ? 'Today' : weekday(day.date)}</p>
						<p class="tabular text-sm font-semibold">{dayNum(day.date)}</p>
					</div>

					<div
						class="flex h-16 w-5 items-end rounded-sm bg-(--surface-2)"
						role="img"
						aria-label="{pct(day.precipProb)} chance of precipitation"
					>
						<div
							class="w-full rounded-t bg-(--accent)"
							style:height="{Math.round(day.precipProb * 100)}%"
						></div>
					</div>
					<p class="tabular text-sm font-semibold">{pct(day.precipProb)}</p>

					<div class="flex min-h-9 flex-col items-center justify-start gap-0.5 text-xs">
						{#if wet}
							<span class="flex items-center gap-1 text-(--text)">
								<KindIcon kind={band.kind} size={12} />
								{band.kind === 'snow' ? 'Snow' : band.kind === 'rain' ? 'Rain' : 'Mix'}
							</span>
							<span class="tabular text-(--text-2)">
								{snowy ? snowRange(band.snowCm, system) : precipRange(day.precipMm, system)}
							</span>
						{:else}
							<span class="text-(--muted)">Dry</span>
						{/if}
					</div>

					<p class="tabular text-xs text-(--text-2)">
						<span class="text-(--text)">{temp(band.tempMaxC, system)}</span>
						/ {temp(band.tempMinC, system)}
					</p>
				</li>
			{/each}
		</ol>
	</div>
</div>
