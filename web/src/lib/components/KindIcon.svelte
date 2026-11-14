<script lang="ts">
	import type { Kind } from '../api';

	let { kind, size = 14 }: { kind: Kind; size?: number } = $props();
</script>

<svg
	width={size}
	height={size}
	viewBox="0 0 16 16"
	fill="none"
	stroke="currentColor"
	stroke-width="1.5"
	stroke-linecap="round"
	stroke-linejoin="round"
	aria-hidden="true"
>
	{#if kind === 'snow'}
		<path d="M8 1.5v13M2.4 4.75l11.2 6.5M13.6 4.75 2.4 11.25" />
	{:else if kind === 'rain'}
		<path d="M8 2c2.2 2.8 3.8 4.9 3.8 7a3.8 3.8 0 0 1-7.6 0c0-2.1 1.6-4.2 3.8-7Z" />
	{:else}
		<path d="M4.5 2v7M1.5 3.75l6 3.5M7.5 3.75l-6 3.5" />
		<path d="M11.5 7.5c1.3 1.7 2.2 2.9 2.2 4.2a2.2 2.2 0 0 1-4.4 0c0-1.3.9-2.5 2.2-4.2Z" />
	{/if}
</svg>
