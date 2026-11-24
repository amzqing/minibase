<script lang="ts">
  import { toast } from '../lib/stores'

  function close() { toast.set(null) }
</script>

{#if $toast}
  <div class="fixed bottom-6 right-6 z-50 flex items-center gap-3 rounded-lg border bg-white px-4 py-3 shadow-lg"
       class:border-green-300={$toast.type === 'success'}
       class:border-red-300={$toast.type === 'error'}>
    <span class="text-sm" class:text-green-700={$toast.type === 'success'} class:text-red-700={$toast.type === 'error'}>
      {$toast.text}
    </span>
    <button class="text-slate-400 hover:text-slate-600" onclick={close} aria-label="关闭">✕</button>
  </div>
{/if}
