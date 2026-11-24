<script lang="ts">
  import { Plus, Trash2 } from 'lucide-svelte'
  import type { EnvItem } from '../lib/types'

  let { items = [], mode = 'env', onSave }: {
    items?: EnvItem[]
    mode?: 'env' | 'secret'
    onSave: (rows: EnvItem[]) => Promise<void> | void
  } = $props()

  let rows = $state<EnvItem[]>([])
  let busy = $state(false)

  $effect(() => {
    rows = items.map((item) => ({ ...item }))
  })

  function addRow() {
    rows = [...rows, { key: '', value: '', is_secret: mode === 'secret' }]
  }

  function removeRow(index: number) {
    rows = rows.filter((_, i) => i !== index)
  }

  async function save() {
    if (busy) return
    busy = true
    try {
      const payload = rows.filter((row) => row.key.trim()).map((row) => ({ ...row, key: row.key.trim() }))
      await onSave(payload)
    } finally {
      busy = false
    }
  }
</script>

<div class="space-y-2">
  {#each rows as row, i (i)}
    <div class="flex flex-wrap items-center gap-2">
      <input aria-label={`键-${i}`} bind:value={row.key} placeholder="KEY" class="w-40 rounded border border-slate-300 px-2 py-1.5 text-sm" />
      <input
        aria-label={`值-${i}`}
        type={mode === 'secret' || row.is_secret ? 'password' : 'text'}
        bind:value={row.value}
        placeholder={row.value === '<encrypted>' ? '留空不修改' : 'value'}
        class="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1.5 text-sm"
      />
      {#if mode === 'env'}
        <label class="flex items-center gap-1 text-xs text-slate-600">
          <input type="checkbox" bind:checked={row.is_secret} aria-label={`密钥-${i}`} /> 密钥
        </label>
      {/if}
      <button class="rounded p-1.5 text-slate-400 hover:bg-red-50 hover:text-red-600" aria-label={`删除-${i}`} onclick={() => removeRow(i)}>
        <Trash2 size={14} />
      </button>
    </div>
  {/each}

  <div class="flex gap-2 pt-2">
    <button class="flex items-center gap-1 rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50" onclick={addRow}>
      <Plus size={14} /> 添加一行
    </button>
    <button class="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-40" disabled={busy} onclick={save}>
      {busy ? '保存中…' : '保存'}
    </button>
  </div>
</div>
