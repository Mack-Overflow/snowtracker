<script lang="ts">
	import type { Forecast, Storm } from '../api';
	import { hour, monthDay, pct, precipAmount, temp, weekday, type System } from '../format';

	let {
		hourly,
		storms,
		bandIndex,
		system
	}: { hourly: Forecast['hourly']; storms: Storm[]; bandIndex: number; system: System } = $props();

	const H = 200;
	const m = { top: 12, right: 10, bottom: 24, left: 38 };
	let width = $state(800);
	let hover = $state<number | null>(null);

	const n = $derived(hourly.times.length);
	const pw = $derived(Math.max(width - m.left - m.right, 10));
	const ph = H - m.top - m.bottom;
	const x = (i: number) => m.left + (n > 1 ? (i / (n - 1)) * pw : 0);
	const y = (p: number) => m.top + (1 - p) * ph;

	const line = $derived(
		hourly.wetProb.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(p).toFixed(1)}`).join('')
	);
	const area = $derived(`${line}L${x(n - 1).toFixed(1)},${y(0)}L${x(0)},${y(0)}Z`);

	// Midnights split the axis into days.
	const days = $derived.by(() => {
		const starts = [0];
		hourly.times.forEach((t, i) => {
			if (i > 0 && t.endsWith('T00:00')) starts.push(i);
		});
		return starts.map((s, k) => ({ start: s, end: starts[k + 1] ?? n - 1, time: hourly.times[s] }));
	});

	const windows = $derived(
		storms.map((s) => {
			const a = Math.max(hourly.times.indexOf(s.start), 0);
			const b = s.openEnded ? n - 1 : hourly.times.indexOf(s.end);
			return { id: s.id, a, b: b < 0 ? n - 1 : b };
		})
	);

	function onmove(e: PointerEvent) {
		const rect = (e.currentTarget as SVGElement).getBoundingClientRect();
		const i = Math.round(((e.clientX - rect.left - m.left) / pw) * (n - 1));
		hover = Math.min(Math.max(i, 0), n - 1);
	}

	const tip = $derived.by(() => {
		if (hover == null) return null;
		const band = hourly.bands[bandIndex];
		const share = band.snowShare[hover];
		return {
			x: x(hover),
			y: y(hourly.wetProb[hover]),
			flip: x(hover) > width - 190,
			time: hourly.times[hover],
			prob: hourly.wetProb[hover],
			mean: hourly.meanMm[hover],
			temp: band.tempC[hover],
			kind: share >= 0.8 ? 'snow' : share <= 0.2 ? 'rain' : 'rain/snow mix'
		};
	});
</script>

<div class="relative" bind:clientWidth={width}>
	<svg
		{width}
		height={H}
		role="img"
		aria-label="Hourly chance of precipitation"
		onpointermove={onmove}
		onpointerleave={() => (hover = null)}
		class="block touch-pan-y"
	>
		{#each windows as w (w.id)}
			<rect
				x={x(w.a)}
				y={m.top}
				width={Math.max(x(w.b) - x(w.a), 2)}
				height={ph}
				rx="4"
				fill="var(--accent-soft)"
				opacity="0.45"
			/>
		{/each}

		{#each [0, 0.5, 1] as p (p)}
			<line x1={m.left} x2={m.left + pw} y1={y(p)} y2={y(p)} stroke="var(--border)" />
			<text x={m.left - 8} y={y(p) + 4} text-anchor="end" font-size="11" fill="var(--muted)">
				{pct(p)}
			</text>
		{/each}

		{#each days as d, k (d.start)}
			{#if k > 0}
				<line
					x1={x(d.start)}
					x2={x(d.start)}
					y1={m.top}
					y2={m.top + ph}
					stroke="var(--border)"
					stroke-dasharray="2 4"
				/>
			{/if}
			{#if x(d.end) - x(d.start) > 34}
				<text
					x={(x(d.start) + x(d.end)) / 2}
					y={H - 6}
					text-anchor="middle"
					font-size="11"
					fill="var(--muted)"
				>
					{weekday(d.time)}
				</text>
			{/if}
		{/each}

		<path d={area} fill="var(--accent)" opacity="0.14" />
		<path d={line} fill="none" stroke="var(--accent)" stroke-width="2" stroke-linejoin="round" />

		{#if tip}
			<line x1={tip.x} x2={tip.x} y1={m.top} y2={m.top + ph} stroke="var(--muted)" />
			<circle cx={tip.x} cy={tip.y} r="4.5" fill="var(--accent)" stroke="var(--surface)" stroke-width="2" />
		{/if}
	</svg>

	{#if tip}
		<div
			class="pointer-events-none absolute top-2 z-10 w-44 rounded-lg border border-(--border) bg-(--surface) px-3 py-2 text-xs shadow-lg"
			style:left={tip.flip ? `${tip.x - 188}px` : `${tip.x + 12}px`}
		>
			<p class="text-(--text-2)">{weekday(tip.time)} {monthDay(tip.time)}, {hour(tip.time)}</p>
			<p class="tabular mt-1 text-base font-semibold">{pct(tip.prob)} chance</p>
			<p class="tabular mt-1 text-(--text-2)">
				{precipAmount(tip.mean, system)}/h avg · {temp(tip.temp, system)} · {tip.kind}
			</p>
		</div>
	{/if}
</div>
