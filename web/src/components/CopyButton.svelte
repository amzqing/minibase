<script lang="ts">
  let { text = '', label = '复制' }: { text?: string; label?: string } = $props()
  let done = $state(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      done = true
      setTimeout(() => { done = false }, 1500)
    } catch {
      done = false
    }
  }
</script>

<button class="rounded border border-slate-300 bg-white px-2 py-1 text-xs text-slate-600 hover:bg-slate-50" onclick={copy}>
  {done ? '已复制' : label}
</button>
