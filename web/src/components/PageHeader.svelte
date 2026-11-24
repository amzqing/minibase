<script lang="ts">
  let { title, subtitle = '' }: { title: string; subtitle?: string } = $props()
</script>

<header class="mb-6">
  <h1 class="text-2xl font-semibold text-slate-800">{title}</h1>
  {#if subtitle}
    <p class="mt-1 text-sm text-slate-500">{subtitle}</p>
  {/if}
</header>
