<script lang="ts">
	import { onMount } from 'svelte';
	import { apiBase, wsUrl, createMachine, type Machine } from '$lib/boring';

	type Phase = 'booting' | 'connecting' | 'live' | 'done' | 'error';

	let { onClose }: { onClose?: () => void } = $props();

	// A rotating set of tasks so repeat viewers see something different. `label`
	// is shown as the opening caption; `goal` is sent to the agent.
	const TASKS = [
		{
			label: 'compute 47 × 89 on the calculator',
			goal: 'Use the on-screen calculator (the xcalc window) to compute 47 times 89 by clicking its buttons: 4, 7, *, 8, 9, =. Do NOT use the terminal. When the answer appears on the calculator display, tell me the number.'
		},
		{
			label: 'add 128 + 256 on the calculator',
			goal: 'Use the on-screen calculator (the xcalc window) to add 128 and 256 by clicking its buttons: 1, 2, 8, +, 2, 5, 6, =. Do NOT use the terminal. Read the answer from the display and tell me.'
		},
		{
			label: 'print a big ASCII "BORING" banner in the terminal',
			goal: 'Click the terminal window to focus it, then type the command  figlet -c BORING  and run it to print a big ASCII banner. Then run  date . Tell me what appeared.'
		},
		{
			label: 'print a big ASCII "HELLO" banner in the terminal',
			goal: 'Click the terminal window to focus it, then type the command  figlet HELLO  and run it to print a big ASCII banner. Then tell me what it drew.'
		}
	];
	const task = TASKS[Math.floor(Math.random() * TASKS.length)];
	const GOAL = task.goal;
	const TTL = 240;
	const MAX_ATTEMPTS = 10;

	let phase = $state<Phase>('booting');
	let machine = $state<Machine | null>(null);
	let error = $state('');
	let caption = $state(`The AI will ${task.label}. Booting a computer…`);
	let log = $state<{ kind: string; text: string }[]>([]);

	let screen: HTMLDivElement;
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	let rfb: any = null;
	let ws: WebSocket | null = null;
	let attempts = 0;
	let disposed = false;
	let agentStarted = false;

	onMount(() => {
		void launch();
		return () => close();
	});

	async function launch() {
		try {
			machine = await createMachine('desktop', TTL);
			phase = 'connecting';
			caption = 'Starting the display…';
			// Let X paint before noVNC's first full frame; the agent starts on connect.
			setTimeout(connectVNC, 4500);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
			phase = 'error';
		}
	}

	function teardownRfb() {
		try {
			rfb?.disconnect();
		} catch {
			/* ignore */
		}
		rfb = null;
		// eslint-disable-next-line svelte/no-dom-manipulating
		if (screen) screen.innerHTML = '';
	}

	async function connectVNC() {
		if (disposed || !machine) return;
		attempts += 1;
		const { default: RFB } = await import('@novnc/novnc');
		if (disposed) return;
		teardownRfb();
		try {
			rfb = new RFB(screen, wsUrl(`/v1/machines/${machine.id}/vnc`), {});
			rfb.scaleViewport = true;
			rfb.resizeSession = false;
			rfb.background = '#000';
			rfb.viewOnly = true; // the AI drives; the human just watches
			rfb.addEventListener('connect', () => {
				// x11vnc accepts before the apps finish painting; give X a moment so
				// the agent's first screenshot shows the desktop, not a black frame.
				if (!disposed) setTimeout(startAgent, 2500);
			});
			rfb.addEventListener('disconnect', () => {
				if (disposed) return;
				if (!agentStarted && attempts < MAX_ATTEMPTS) setTimeout(connectVNC, 1500);
			});
		} catch {
			if (attempts < MAX_ATTEMPTS) setTimeout(connectVNC, 1500);
		}
	}

	function startAgent() {
		if (agentStarted || disposed || !machine) return;
		agentStarted = true;
		phase = 'live';
		caption = 'The AI is looking at the screen…';
		ws = new WebSocket(wsUrl(`/v1/machines/${machine.id}/agent?goal=${encodeURIComponent(GOAL)}`));
		ws.onmessage = (e) => {
			let m: { type: string; text?: string };
			try {
				m = JSON.parse(e.data);
			} catch {
				return;
			}
			if (m.type === 'say' && m.text) {
				caption = m.text;
				log = [...log, { kind: 'say', text: m.text }].slice(-6);
			} else if (m.type === 'action' && m.text) {
				log = [...log, { kind: 'action', text: m.text }].slice(-6);
			} else if (m.type === 'done') {
				phase = 'done';
				caption = m.text || 'The AI finished the task.';
			} else if (m.type === 'error') {
				phase = 'error';
				error = m.text || 'the agent stopped unexpectedly';
			}
		};
		ws.onclose = () => {
			if (phase === 'live') {
				phase = 'done';
				caption = 'The AI finished.';
			}
		};
	}

	export function close() {
		disposed = true;
		try {
			ws?.close();
		} catch {
			/* ignore */
		}
		ws = null;
		try {
			rfb?.disconnect();
		} catch {
			/* ignore */
		}
		rfb = null;
		if (machine) {
			void fetch(`${apiBase}/v1/machines/${machine.id}`, { method: 'DELETE' }).catch(() => {});
		}
		machine = null;
		onClose?.();
	}

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') close();
	}
</script>

<svelte:window onkeydown={onKey} />

<div class="w-full max-w-3xl">
	<div
		class="flex items-center justify-between rounded-t-geist-lg border border-line bg-surface px-4 py-2.5 font-mono text-[12px]"
	>
		<div class="flex items-center gap-2 text-ink-muted">
			{#if phase === 'booting' || phase === 'connecting'}
				<span class="size-1.5 animate-pulse rounded-full bg-ink-subtle"></span>preparing a computer…
			{:else if phase === 'live'}
				<span class="size-1.5 animate-pulse rounded-full bg-accent"></span>
				<span class="text-ink">an AI is using this computer</span>
			{:else if phase === 'done'}
				<span class="size-1.5 rounded-full bg-success"></span>finished
			{:else if phase === 'error'}
				<span class="size-1.5 rounded-full bg-danger"></span>
				<span class="text-danger">{error}</span>
			{/if}
		</div>
		<button class="text-ink-subtle transition-colors hover:text-ink" onclick={close}>esc ✕</button>
	</div>
	<div
		class="relative overflow-hidden border-x border-line bg-black"
		class:hidden={phase === 'error'}
	>
		<div bind:this={screen} class="aspect-[16/10] w-full"></div>
		{#if phase !== 'live' && phase !== 'done'}
			<div
				class="pointer-events-none absolute inset-0 flex items-center justify-center font-mono text-[12px] text-ink-subtle"
			>
				allocating a computer…
			</div>
		{/if}
	</div>
	<!-- caption strip: the AI narrates what it's doing -->
	<div
		class="flex items-start gap-2.5 rounded-b-geist-lg border border-t-0 border-line bg-surface px-4 py-3 font-mono text-[12px]"
		class:hidden={phase === 'error'}
	>
		<span class="mt-px shrink-0 text-accent">✦</span>
		<span class="leading-relaxed text-ink-muted">{caption}</span>
	</div>
</div>
