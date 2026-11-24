<script lang="ts">
  import { onMount } from 'svelte'
  import { Plus, Trash2 } from 'lucide-svelte'
  import PageHeader from '../components/PageHeader.svelte'
  import DataTable from '../components/DataTable.svelte'
  import StatePanel from '../components/StatePanel.svelte'
  import ConfirmDialog from '../components/ConfirmDialog.svelte'
  import AsyncButton from '../components/AsyncButton.svelte'
  import { addCors, deleteCors, listCors } from '../lib/api'
  import { toast } from '../lib/stores'
  import type { CorsDomain } from '../lib/types'

  let { appId }: { appId: string } = $props()
  let items = $state<CorsDomain[]>([])
  let domain = $state('')
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')
  let confirm = $state<CorsDomain | null>(null)
  let busy = $state(false)

  const columns = [
    { key: 'domain', label: '域名' },
    { key: 'created_at', label: '添加时间' },
    { key: 'actions', label: '操作' }
  ]

  async function load() {
    phase = 'loading'
    error = ''
    try {
      items = await listCors(appId)
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载 CORS 失败'
      phase = 'error'
    }
  }
  onMount(load)

  async function add(e: SubmitEvent) {
    e.preventDefault()
    if (!domain.trim() || busy) return
    busy = true
    try {
      await addCors(appId, domain.trim())
      toast.set({ type: 'success', text: 'CORS 域名已添加' })
      domain = ''
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '添加失败' })
    } finally {
      busy = false
    }
  }

  async function remove() {
    if (!confirm || busy) return
    const target = confirm
    confirm = null
    busy = true
    try {
      await deleteCors(appId, target.id)
      toast.set({ type: 'success', text: 'CORS 域名已删除' })
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '删除失败' })
    } finally {
      busy = false
    }
  }
</script>

<PageHeader title="安全" subtitle={`${appId} 的 CORS 白名单`} />

<form class="mb-5 flex gap-2 rounded-lg border border-slate-200 bg-white p-4" onsubmit={add}>
  <input bind:value={domain} placeholder="https://admin.example.com" class="flex-1 rounded border border-slate-300 px-3 py-2 text-sm" />
  <AsyncButton type="submit" variant="primary" icon={Plus} busy={busy} disabled={!domain.trim()} loadingText="添加中…">添加</AsyncButton>
</form>

{#if phase === 'loading'}
  <StatePanel state="loading" />
{:else if phase === 'error'}
  <StatePanel state="error" {error} retry={load} />
{:else if items.length === 0}
  <StatePanel state="empty" />
{:else}
  <DataTable columns={columns} rows={items} rowKey={(r: any) => r.id}>
    {#snippet cell(row: any, key: string)}
      {#if key === 'created_at'}{row.created_at ? new Date(row.created_at * 1000).toLocaleString() : '-'}{:else if key === 'actions'}<button class="text-red-600" onclick={() => { confirm = row }}><Trash2 size={14} /></button>{:else}{row[key] ?? '-'}{/if}
    {/snippet}
  </DataTable>
{/if}

<ConfirmDialog open={!!confirm} title="删除 CORS 域名" message={`确定删除 ${confirm?.domain ?? ''}？`} confirmText="删除" danger onConfirm={() => void remove()} onClose={() => { confirm = null }} />
