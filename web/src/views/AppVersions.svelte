<script lang="ts">
  import { onMount } from 'svelte'
  import { RotateCcw } from 'lucide-svelte'
  import PageHeader from '../components/PageHeader.svelte'
  import DataTable from '../components/DataTable.svelte'
  import StatePanel from '../components/StatePanel.svelte'
  import ConfirmDialog from '../components/ConfirmDialog.svelte'
  import AsyncButton from '../components/AsyncButton.svelte'
  import { listDeployments, listVersions, rollbackApp } from '../lib/api'
  import { toast } from '../lib/stores'
  import type { DeployHistoryItem, VersionItem } from '../lib/types'

  let { appId }: { appId: string } = $props()
  let versions = $state<VersionItem[]>([])
  let history = $state<DeployHistoryItem[]>([])
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')
  let confirmRollback = $state(false)
  let rolling = $state(false)

  const versionColumns = [
    { key: 'version', label: '版本' },
    { key: 'mode', label: '模式' },
    { key: 'port', label: '端口' },
    { key: 'created_at', label: '创建时间' }
  ]
  const historyColumns = [
    { key: 'event', label: '事件' },
    { key: 'from_status', label: '原状态' },
    { key: 'to_status', label: '新状态' },
    { key: 'reason', label: '说明' },
    { key: 'created_at', label: '时间' }
  ]

  async function load() {
    phase = 'loading'
    error = ''
    try {
      const [v, h] = await Promise.all([listVersions(appId), listDeployments(appId)])
      versions = v.map((row: any) => ({
        ...row,
        version: row.version ?? row.Version,
        mode: row.mode ?? row.Mode,
        port: row.port ?? row.Port,
        created_at: row.created_at ?? row.CreatedAt
      }))
      history = h.map((row: any) => ({
        id: row.id ?? row.ID,
        event: row.event ?? row.Event,
        from_status: row.from_status ?? row.FromStatus,
        to_status: row.to_status ?? row.ToStatus,
        reason: row.reason ?? row.Reason,
        created_at: row.created_at ?? row.CreatedAt,
        app_id: row.app_id ?? row.AppID
      }))
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载版本失败'
      phase = 'error'
    }
  }
  onMount(load)

  async function rollback() {
    if (rolling) return
    confirmRollback = false
    rolling = true
    try {
      const result = await rollbackApp(appId)
      toast.set({ type: 'success', text: `已回滚到 ${result.version ?? '上一个版本'}` })
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '回滚失败' })
    } finally {
      rolling = false
    }
  }
</script>

<PageHeader title="版本" subtitle={`${appId} 的版本与部署历史`} />

<div class="mb-4">
  <AsyncButton icon={RotateCcw} busy={rolling} loadingText="回滚中…" onclick={() => { confirmRollback = true }}>回滚到上一个版本</AsyncButton>
</div>

{#if phase === 'loading'}
  <StatePanel state="loading" />
{:else if phase === 'error'}
  <StatePanel state="error" {error} retry={load} />
{:else}
  <div class="space-y-6">
    <section>
      <h2 class="mb-2 text-sm font-semibold text-slate-700">版本列表</h2>
      {#if versions.length === 0}<StatePanel state="empty" />{:else}
        <DataTable columns={versionColumns} rows={versions} rowKey={(r: any) => String(r.version)}>
          {#snippet cell(row: any, key: string)}
            {#if key === 'created_at'}{row.created_at ? new Date(row.created_at * 1000).toLocaleString() : '-'}{:else}{row[key] ?? '-'}{/if}
          {/snippet}
        </DataTable>
      {/if}
    </section>
    <section>
      <h2 class="mb-2 text-sm font-semibold text-slate-700">部署与运维历史</h2>
      {#if history.length === 0}<StatePanel state="empty" />{:else}
        <DataTable columns={historyColumns} rows={history} rowKey={(r: any) => r.id ?? r.event ?? String(r.created_at ?? '')}>
          {#snippet cell(row: any, key: string)}
            {#if key === 'created_at'}{row.created_at ? new Date(row.created_at * 1000).toLocaleString() : '-'}{:else}{row[key] ?? '-'}{/if}
          {/snippet}
        </DataTable>
      {/if}
    </section>
  </div>
{/if}

<ConfirmDialog open={confirmRollback} title="回滚应用" message={`将 ${appId} 回滚到上一个版本？当前服务会短暂切换。`} confirmText="回滚" danger onConfirm={() => void rollback()} onClose={() => { confirmRollback = false }} />
