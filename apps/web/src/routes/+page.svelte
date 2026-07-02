<script lang="ts">
	import Computer from '$lib/Computer.svelte';

	let active = $state(false);

	function onKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !active) {
			const el = document.activeElement;
			// ignore Enter inside inputs/textareas/the terminal itself
			if (el && ['INPUT', 'TEXTAREA'].includes(el.tagName)) return;
			if (el?.closest('.xterm')) return;
			active = true;
		}
	}
</script>

<svelte:head>
	<title>Boring Computers</title>
	<meta name="description" content="Computers that are refreshingly boring." />
</svelte:head>

<svelte:window onkeydown={onKeydown} />

<div class="flex min-h-screen flex-col items-center justify-center gap-8 bg-black px-5 py-16">
	<h1
		class="text-center text-[clamp(1rem,3vw,2rem)] font-semibold whitespace-nowrap tracking-[-0.03em] text-ink"
	>
		Computers that are
		<span class="text-ink-subtle">refreshingly boring.</span>
	</h1>

	{#if active}
		<Computer onClose={() => (active = false)} />
	{:else}
		<button
			onclick={() => (active = true)}
			class="group inline-flex items-center gap-2 font-mono text-[13px] text-ink-subtle transition-colors hover:text-ink focus-visible:outline-none"
		>
			<kbd
				class="rounded-[5px] border border-line bg-surface px-1.5 py-0.5 text-ink-muted transition-colors group-hover:border-white/25"
				>⏎</kbd
			>
			<span
				>Press <span class="text-ink-muted group-hover:text-ink">enter</span> to get a computer</span
			>
			<span class="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse bg-ink-subtle align-middle"></span>
		</button>
	{/if}
</div>
