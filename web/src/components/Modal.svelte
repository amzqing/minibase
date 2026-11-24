<script lang="ts">
  import { X } from 'lucide-svelte'
  let { open = false, title = '', onClose, children }: {
    open?: boolean
    title?: string
    onClose: () => void
    children: any
  } = $props()
</script>

{#if open}
  <div class="fixed inset-0 z-40 flex items-center justify-center bg-slate-900/40 p-4">
    <div class="max-h-[85vh] w-full max-w-lg overflow-auto rounded-lg bg-white shadow-xl" role="dialog" aria-modal="true" aria-label={title} tabindex="-1">
      <div class="flex items-center justify-between border-b border-slate-200 px-5 py-3">
        <h2 class="text-base font-semibold text-slate-800">{title}</h2>
        <button class="text-slate-400 hover:text-slate-600" onclick={onClose} aria-label="关闭"><X size={16} /></button>
      </div>
      <div class="p-5">{@render children()}</div>
    </div>
  </div>
{/if}
