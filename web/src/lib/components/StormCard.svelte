<script lang="ts">
	import type { Band, Storm } from '../api';
	import {
		dayHour,
		elevation,
		kindLabel,
		modelName,
		pct,
		precipRange,
		snowRange,
		type System
	} from '../format';
	import KindIcon from './KindIcon.svelte';

	let {
		storm,
		bandIndex,
		band,
		system
	}: { storm: Storm; bandIndex: number; band: Band; system: System } = $props();

	const at = $derived(storm.bands[bandIndex]);
	// Below ~1 cm at the top of the range, snow isn't the story: show liquid.
	const snowy = $derived(at.kind !== 'rain' && at.snowCm.p90 >= 1);
	const models = $derived(Object.entries(storm.byModel).sort(([a], [b]) => a.localeCompare(b)));
</script>

<article
	class="flex min-w-72 flex-1 flex-col gap-4 rounded-2xl border border-(--border) bg-(--surface) p-5"
>
	<header class="flex items-start justify-between gap-3">
		<div>
			<p class="text-xs font-medium tracking-wide text-(--text-2) uppercase">
				{storm.intensity} storm
			</p>
			<h3 class="mt-1 text-base font-semibold">
				{storm.underway ? 'Now' : dayHour(storm.start)} → {dayHour(storm.end)}{storm.openEnded
					? '+'
					: ''}
			</h3>
		</div>
		<div class="text-right">
			<p class="tabular text-3xl leading-none font-semibold">{pct(storm.likelihood)}</p>
			<p class="mt-1 text-xs text-(--text-2)">likely</p>
		</div>
	</header>

	<div class="flex items-center gap-2 text-(--text)">
		<KindIcon kind={at.kind} size={18} />
		<p class="text-lg font-medium">
			{snowy ? snowRange(at.snowCm, system) : precipRange(storm.precipMm, system)}
			<span class="text-sm font-normal text-(--text-2)">
				{snowy ? 'snow' : kindLabel[at.kind].toLowerCase()} at {band.label.toLowerCase()}
			</span>
		</p>
	</div>

	<dl class="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
		<dt class="text-(--text-2)">Liquid total</dt>
		<dd class="tabular text-right">{precipRange(storm.precipMm, system)}</dd>
		{#if storm.snowLevelM != null}
			<dt class="text-(--text-2)">Snow line</dt>
			<dd class="tabular text-right">~{elevation(Math.max(0, storm.snowLevelM), system)}</dd>
		{/if}
		<dt class="text-(--text-2)">Peak</dt>
		<dd class="tabular text-right">{dayHour(storm.peak)}</dd>
	</dl>

	<div class="mt-auto border-t border-(--border) pt-3">
		<p class="mb-2 text-xs text-(--text-2)">Model agreement</p>
		<ul class="space-y-1.5">
			{#each models as [name, share] (name)}
				<li class="flex items-center gap-2 text-xs">
					<span class="w-14 shrink-0 text-(--text-2)">{modelName(name)}</span>
					<span class="h-1.5 flex-1 overflow-hidden rounded-full bg-(--surface-2)">
						<span class="block h-full rounded-full bg-(--accent)" style:width={pct(share)}></span>
					</span>
					<span class="tabular w-9 shrink-0 text-right">{pct(share)}</span>
				</li>
			{/each}
		</ul>
	</div>
</article>
