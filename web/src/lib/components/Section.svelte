<script lang="ts">
	import type { Snippet } from 'svelte';
	import { prefs, toggleSection } from '../prefs.svelte';

	let {
		id,
		title,
		note,
		actions,
		children
	}: {
		/** Stable name: used for the heading id and to remember whether it is collapsed. */
		id: string;
		title: string;
		/** Secondary text after the title. */
		note?: string;
		/** Controls shown at the right of the heading while the section is open. */
		actions?: Snippet;
		children: Snippet;
	} = $props();

	const open = $derived(!prefs.collapsed.includes(id));
</script>

<section aria-labelledby="{id}-h">
	<div class={['flex flex-wrap items-center gap-x-4 gap-y-2', open && 'mb-3']}>
	<h2 id="{id}-h" class="min-w-0 flex-1 text-sm font-semibold">
		<button
			type="button"
			aria-expanded={open}
			aria-controls="{id}-body"
			onclick={() => toggleSection(id)}
			class="-mx-2 flex w-[calc(100%+1rem)] items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-(--surface-2)"
		>
			<svg
				width="12"
				height="12"
				viewBox="0 0 12 12"
				fill="none"
				stroke="currentColor"
				stroke-width="1.75"
				stroke-linecap="round"
				stroke-linejoin="round"
				aria-hidden="true"
				class={['shrink-0 text-(--muted) transition-transform', open && 'rotate-90']}
			>
				<path d="m4.5 2.5 3.5 3.5-3.5 3.5" />
			</svg>
			<span>
				{title}
				{#if note}<span class="font-normal text-(--text-2)">· {note}</span>{/if}
			</span>
		</button>
	</h2>
	{#if actions && open}{@render actions()}{/if}
	</div>
	<!-- Collapsed content is removed, not hidden, so a closed map loads nothing. -->
	{#if open}
		<div id="{id}-body">{@render children()}</div>
	{/if}
</section>
