<script lang="ts">
	// Render's free tier sleeps the API when idle, so the first request can take a while.
	// Lead with the honest explanation, then keep the wait company with rotating quips.
	const first = 'Spinning up API service, this may take a second…';
	const quips = [
		'Cranking the snowblowers…',
		'Smoothing predictions…',
		'Accumulating the clouds…',
		'Waxing the ensemble members…',
		'Counting snowflakes…',
		'Consulting the weather models…',
		'Grooming the forecast…',
		'Chasing the jet stream…',
		'Measuring the snowpack…',
		'Lowering the snow level…'
	];

	let { interval = 2800 }: { interval?: number } = $props();

	let message = $state(first);

	$effect(() => {
		let last = -1;
		const id = setInterval(() => {
			// Pick a random quip, never the same one twice in a row.
			let next = Math.floor(Math.random() * quips.length);
			if (next === last) next = (next + 1) % quips.length;
			last = next;
			message = quips[next];
		}, interval);
		return () => clearInterval(id);
	});
</script>

<p class="text-sm text-(--text-2)" role="status" aria-live="polite">{message}</p>
