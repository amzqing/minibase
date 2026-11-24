<script lang="ts">
  import { onMount } from 'svelte'
  import { Activity, ClipboardList, KeyRound, SlidersHorizontal } from 'lucide-svelte'
  import PageHeader from '../components/PageHeader.svelte'
  import DataTable from '../components/DataTable.svelte'
  import StatePanel from '../components/StatePanel.svelte'
  import ConfirmDialog from '../components/ConfirmDialog.svelte'
  import CopyButton from '../components/CopyButton.svelte'
  import AsyncButton from '../components/AsyncButton.svelte'
  import { getLogControl, getSystemUsage, listAuditLogs, rotateToken, setLogControl, systemHealth } from '../lib/api'
  import { saveToken, toast } from '../lib/stores'
  import type { AuditLog, LogControlStatus } from '../lib/types'
  import StatusBadge from '../components/StatusBadge.svelte'

  let health = $state<{ status: string } | null>(null)
  let usage = $state<Record<string, unknown> | null>(null)
  let audit = $state<AuditLog[]>([])
  let logStatus = $state<LogControlStatus>({})
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')

  let scope = $state<'control' | 'app'>('control')
  let appName = $state('')
  let enabled = $state(true)
  let duration = $state(0)
  let level = $state<'all' | 'error'>('error')
  let rotateConfirm = $state(false)
  let newToken = $state('')
  let savingControl = $state(false)
  let rotating = $state(false)
  let reloading = $state(false)

  const auditColumns = [
    { key: 'event', label: '事件' },
    { key: 'to_status', label: '结果' },
    { key: 'reason', label: '说明' },
    { key: 'created_at', label: '时间' }
  ]

  async function load() {
    reloading = true
    error = ''
    try {
      const [h, u, a, l] = await Promise.all([
        systemHealth(), getSystemUsage(), listAuditLogs(100), getLogControl()
      ])
      health = h
      usage = u
      audit = (a ?? []).map((row: any) => ({
        id: row.id ?? row.ID,
        event: row.event ?? row.Event,
        to_status: row.to_status ?? row.ToStatus,
        reason: row.reason ?? row.Reason,
        created_at: row.created_at ?? row.CreatedAt
      }))
      logStatus = l ?? {}
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载系统信息失败'
      phase = 'error'
    } finally {
      reloading = false
    }
  }
  onMount(load)

  async function submitLogControl(e: SubmitEvent) {
    e.preventDefault()
    if (savingControl) return
    savingControl = true
    try {
      await setLogControl({
        scope,
        app_name: scope === 'app' ? appName.trim() : undefined,
        enabled,
        duration_sec: Number(duration),
        level
      })
      toast.set({ type: 'success', text: '日志开关已更新' })
      logStatus = await getLogControl()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '设置失败' })
    } finally {
      savingControl = false
    }
  }

  async function rotate() {
    if (rotating) return
    rotateConfirm = false
    rotating = true
    try {
      const result = await rotateToken()
      saveToken(result.token)
      newToken = result.token
      toast.set({ type: 'success', text: 'Token 已轮换，请保存新 Token' })
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : 'Token 轮换失败' })
    } finally {
      rotating = false
    }
  }
</script>

<PageHeader title="系统设置" subtitle="运行状态、资源用量、审计、日志开关与管理员 Token" />

{#if phase === 'loading'}
  <StatePanel state="loading" />
{:else if phase === 'error'}
  <StatePanel state="error" {error} retry={load} />
{:else}
  <div class="space-y-5">
    <div class="grid gap-4 md:grid-cols-2">
      <section class="rounded-lg border border-slate-200 bg-white p-4">
        <h2 class="mb-2 flex items-center gap-1 text-sm font-semibold text-slate-700"><Activity size={14} /> Core 状态</h2>
        <StatusBadge status={health?.status === 'ok' ? '运行中' : '失败'} />
      </section>
      <section class="rounded-lg border border-slate-200 bg-white p-4">
        <h2 class="mb-2 text-sm font-semibold text-slate-700">资源用量</h2>
        <pre class="overflow-auto rounded bg-slate-50 p-3 text-xs text-slate-600">{JSON.stringify(usage, null, 2)}</pre>
      </section>
    </div>

    <section class="rounded-lg border border-slate-200 bg-white p-4">
      <h2 class="mb-3 flex items-center gap-1 text-sm font-semibold text-slate-700"><SlidersHorizontal size={14} /> 日志开关</h2>
      <form class="grid gap-3 md:grid-cols-5" onsubmit={submitLogControl}>
        <select bind:value={scope} class="rounded border border-slate-300 px-2 py-1.5 text-sm"><option value="control">control</option><option value="app">app</option></select>
        <input bind:value={appName} disabled={scope !== 'app'} placeholder="应用名" class="rounded border border-slate-300 px-2 py-1.5 text-sm disabled:bg-slate-100" />
        <select bind:value={level} class="rounded border border-slate-300 px-2 py-1.5 text-sm"><option value="error">error</option><option value="all">all</option></select>
        <input bind:value={duration} type="number" placeholder="自动关闭秒数" class="rounded border border-slate-300 px-2 py-1.5 text-sm" />
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={enabled} /> 开启</label>
        <AsyncButton type="submit" variant="primary" busy={savingControl} loadingText="保存中…" class="md:col-span-5">保存日志开关</AsyncButton>
      </form>
      <pre class="mt-3 overflow-auto rounded bg-slate-50 p-3 text-xs text-slate-600">{JSON.stringify(logStatus, null, 2)}</pre>
    </section>

    <section class="rounded-lg border border-slate-200 bg-white p-4">
      <h2 class="mb-3 flex items-center gap-1 text-sm font-semibold text-slate-700"><ClipboardList size={14} /> 最近审计</h2>
      {#if audit.length === 0}<StatePanel state="empty" />{:else}
        <DataTable columns={auditColumns} rows={audit} rowKey={(r: any) => r.id ?? r.event ?? String(r.created_at ?? '')}>
          {#snippet cell(row: any, key: string)}
            {#if key === 'created_at'}{row.created_at ? new Date(row.created_at * 1000).toLocaleString() : '-'}{:else}{row[key] ?? '-'}{/if}
          {/snippet}
        </DataTable>
      {/if}
    </section>

    <section class="rounded-lg border border-slate-200 bg-white p-4">
      <h2 class="mb-3 flex items-center gap-1 text-sm font-semibold text-slate-700"><KeyRound size={14} /> 管理员 Token</h2>
      <AsyncButton variant="danger" busy={rotating} loadingText="轮换中…" onclick={() => { rotateConfirm = true }}>轮换 Token</AsyncButton>
      {#if newToken}
        <div class="mt-3 rounded border border-amber-300 bg-amber-50 p-3">
          <p class="text-xs text-amber-800">新 Token 只显示一次，请立即保存并更新客户端环境变量。</p>
          <div class="mt-2 flex items-center gap-2">
            <code class="min-w-0 flex-1 overflow-auto rounded bg-white p-2 text-xs">{newToken}</code>
            <CopyButton text={newToken} />
          </div>
        </div>
      {/if}
    </section>
  </div>
{/if}


<ConfirmDialog open={rotateConfirm} title="轮换 Token" message="轮换后旧 Token 立即失效。此操作不可撤销，是否继续？" confirmText="轮换" danger onConfirm={() => void rotate()} onClose={() => { rotateConfirm = false }} />
