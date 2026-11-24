<script lang="ts">
  import { onMount } from 'svelte'
  import { Plus, RefreshCw, Search } from 'lucide-svelte'
  import PageHeader from '../components/PageHeader.svelte'
  import DataTable from '../components/DataTable.svelte'
  import StatusBadge from '../components/StatusBadge.svelte'
  import StatePanel from '../components/StatePanel.svelte'
  import DeployDialog from '../components/DeployDialog.svelte'
  import AsyncButton from '../components/AsyncButton.svelte'
  import { listApps } from '../lib/api'
  import { appPath, navigate } from '../lib/router'
  import { apps, toast } from '../lib/stores'
  import type { App } from '../lib/types'

  let keyword = $state('')
  let status = $state('all')
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')
  let deployOpen = $state(false)
  let reloading = $state(false)

  const columns = [
    { key: 'app_name', label: '应用名' },
    { key: 'status', label: '状态' },
    { key: 'current_version', label: '版本' },
    { key: 'port', label: '端口' },
    { key: 'access', label: '访问' }
  ]

  async function load() {
    if (reloading) return
    reloading = true
    error = ''
    try {
      apps.set(await listApps())
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载失败'
      phase = 'error'
    } finally {
      reloading = false
    }
  }

  onMount(load)

  let filtered = $derived(
    $apps.filter((a: App) => {
      const kw = keyword.trim().toLowerCase()
      return (!kw || a.app_name.toLowerCase().includes(kw)) && (status === 'all' || a.status === status)
    })
  )
</script>

<PageHeader title="应用管理" subtitle="所有应用都在这里，点一行进入详情" />

<div class="mb-5 flex flex-wrap items-center gap-3">
  <div class="relative">
    <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
    <input bind:value={keyword} placeholder="搜索应用名"
      class="w-56 rounded border border-slate-300 bg-white py-2 pl-9 pr-3 text-sm focus:border-blue-500 focus:outline-none" />
  </div>

  <select bind:value={status}
    class="rounded border border-slate-300 bg-white px-3 py-2 text-sm focus:border-blue-500 focus:outline-none">
    <option value="all">全部状态</option>
    <option value="运行中">运行中</option>
    <option value="已暂停">已暂停</option>
    <option value="已停止">已停止</option>
    <option value="失败">失败</option>
    <option value="不健康">不健康</option>
  </select>

  <AsyncButton icon={RefreshCw} busy={reloading && phase === 'ready'} loadingText="刷新中…" onclick={() => void load()}>刷新</AsyncButton>

  <button onclick={() => { deployOpen = true }} class="ml-auto flex items-center gap-1 rounded bg-blue-600 px-3 py-2 text-sm text-white hover:bg-blue-700">
    <Plus size={14} /> 部署应用
  </button>
</div>

{#if phase === 'loading'}
  <StatePanel state="loading" />
{:else if phase === 'error'}
  <StatePanel state="error" {error} retry={load} />
{:else if filtered.length === 0}
  <StatePanel state="empty" />
{:else}
  <DataTable columns={columns} rows={filtered} rowKey={(r: any) => r.id ?? r.app_name} rowClick={(r: any) => navigate(appPath(r.id ?? r.app_name))}>
    {#snippet cell(row: any, key: string)}
      {#if key === 'status'}
        <StatusBadge status={row.status} />
      {:else if key === 'access'}
        <a href={row.access_url ?? '#'} target="_blank" rel="noreferrer" class="text-blue-600 hover:underline"
           onclick={(e) => e.stopPropagation()}>打开</a>
      {:else}
        {row[key] ?? '-'}
      {/if}
    {/snippet}
  </DataTable>
{/if}

<DeployDialog open={deployOpen} onClose={() => { deployOpen = false }} onDeployed={load} />
