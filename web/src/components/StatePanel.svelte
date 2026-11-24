<script lang="ts">
  let { state, error = '', retry }: { state: 'loading' | 'empty' | 'error'; error?: string; retry?: () => void } = $props()
</script>

{#if state === 'loading'}
  <div class="flex justify-center py-16 text-sm text-slate-500">加载中…</div>
{:else if state === 'empty'}
  <div class="flex justify-center py-16 text-sm text-slate-500">暂无数据</div>
{:else}
  <div class="flex flex-col items-center gap-3 py-16 text-sm text-red-600">
    <span>{error || '加载失败'}</span>
    {#if retry}
      <button class="rounded border border-red-300 px-3 py-1 hover:bg-red-50" onclick={retry}>重试</button>
    {/if}
  </div>
{/if}
