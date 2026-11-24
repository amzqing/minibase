<script lang="ts">
  import { onMount } from 'svelte'
  import { ExternalLink, Pause, Play, RotateCcw, RotateCw, Trash2 } from 'lucide-svelte'
  import PageHeader from '../components/PageHeader.svelte'
  import StatePanel from '../components/StatePanel.svelte'
  import StatusBadge from '../components/StatusBadge.svelte'
  import ConfirmDialog from '../components/ConfirmDialog.svelte'
  import AsyncButton from '../components/AsyncButton.svelte'
  import Modal from '../components/Modal.svelte'
  import { deleteApp, getApp, getAppUsage, pauseApp, restartApp, resumeApp, rollbackApp, updateApp } from '../lib/api'
  import { navigate } from '../lib/router'
  import { currentApp, toast } from '../lib/stores'
  import type { AppDetail } from '../lib/types'

  let { appId }: { appId: string } = $props()

  let detail = $state<AppDetail | null>(null)
  let usage = $state<Record<string, unknown> | null>(null)
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')
  let confirm = $state<{ title: string; message: string; text?: string; requireText?: string; action: () => Promise<unknown> } | null>(null)
  let busy = $state('')
  let editOpen = $state(false)
  let memory = $state('')
  let cpu = $state('')
  let startCmd = $state('')
  let healthPath = $state('')
  let healthTimeout = $state('')

  async function load() {
    phase = 'loading'
    error = ''
    try {
      detail = await getApp(appId)
      usage = await getAppUsage(appId)
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载应用详情失败'
      phase = 'error'
    }
  }
  onMount(load)

  function ask(title: string, message: string, action: () => Promise<unknown>, text = '确认', requireText = '') {
    confirm = { title, message, action, text, requireText }
  }

  async function runConfirm() {
    const current = confirm
    if (!current || busy) return
    confirm = null
    busy = '操作处理中…'
    try {
      await current.action()
      toast.set({ type: 'success', text: '操作成功' })
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '操作失败' })
    } finally {
      busy = ''
    }
  }

  function openEdit() {
    memory = ''
    cpu = ''
    startCmd = ''
    healthPath = ''
    healthTimeout = ''
    editOpen = true
  }

  async function submitEdit(e: SubmitEvent) {
    e.preventDefault()
    if (busy) return
    const body: Record<string, unknown> = {}
    if (memory) body.memory_limit_mb = Number(memory)
    if (cpu) body.cpu_quota_percent = Number(cpu)
    if (startCmd) body.start_cmd = startCmd
    if (healthPath) body.health_path = healthPath
    if (healthTimeout) body.health_timeout_ms = Number(healthTimeout)
    busy = '保存中…'
    try {
      await updateApp(appId, body)
      toast.set({ type: 'success', text: '配置已保存，下次启动生效' })
      editOpen = false
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '保存失败' })
    } finally {
      busy = ''
    }
  }

  const canEdit = $derived(detail?.status === '已暂停')
</script>

<PageHeader title={appId} subtitle="应用概览、运行状态和生命周期操作" />

{#if phase === 'loading'}
  <StatePanel state="loading" />
{:else if phase === 'error'}
  <StatePanel state="error" {error} retry={load} />
{:else if detail}
  <div class="grid gap-4 md:grid-cols-3">
    <div class="rounded-lg border border-slate-200 bg-white p-4">
      <p class="text-xs text-slate-500">状态</p>
      <div class="mt-2"><StatusBadge status={detail.status} /></div>
    </div>
    <div class="rounded-lg border border-slate-200 bg-white p-4">
      <p class="text-xs text-slate-500">当前版本</p>
      <p class="mt-2 text-lg font-semibold text-slate-800">{detail.current_version || '-'}</p>
    </div>
    <div class="rounded-lg border border-slate-200 bg-white p-4">
      <p class="text-xs text-slate-500">端口</p>
      <p class="mt-2 text-lg font-semibold text-slate-800">{detail.port || $currentApp?.port || '-'}</p>
    </div>
  </div>

  <div class="mt-4 rounded-lg border border-slate-200 bg-white p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-700">操作</h2>
    <div class="flex flex-wrap gap-2">
      {#if detail.status === '已暂停'}
        <button class="flex items-center gap-1 rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50" onclick={() => ask('恢复应用', `恢复 ${appId}？`, () => resumeApp(appId))}><Play size={14} /> 恢复</button>
      {:else}
        <button class="flex items-center gap-1 rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50" onclick={() => ask('暂停应用', `暂停 ${appId}？数据会保留。`, () => pauseApp(appId))}><Pause size={14} /> 暂停</button>
      {/if}
      <button class="flex items-center gap-1 rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50" onclick={() => ask('重启应用', `重启 ${appId}？`, () => restartApp(appId))}><RotateCw size={14} /> 重启</button>
      <button class="flex items-center gap-1 rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50" onclick={() => ask('回滚应用', `回滚到上一个版本？`, () => rollbackApp(appId))}><RotateCcw size={14} /> 回滚</button>
      <button class="rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50 disabled:opacity-40" disabled={!canEdit} title={canEdit ? '' : '请先暂停应用再编辑'} onclick={openEdit}>编辑配置</button>
      {#if detail.access_url}
        <a class="flex items-center gap-1 rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50" href={detail.access_url} target="_blank" rel="noreferrer"><ExternalLink size={14} /> 打开应用</a>
      {/if}
      <button class="ml-auto flex items-center gap-1 rounded border border-red-200 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50" onclick={() => ask('删除应用', `彻底删除 ${appId}？数据无法恢复。`, async () => { await deleteApp(appId); navigate('/apps') }, '删除', appId)}><Trash2 size={14} /> 删除应用</button>
    </div>
  </div>

  {#if usage}
    <div class="mt-4 rounded-lg border border-slate-200 bg-white p-4">
      <h2 class="mb-3 text-sm font-semibold text-slate-700">资源用量</h2>
      <pre class="overflow-auto rounded bg-slate-50 p-3 text-xs text-slate-600">{JSON.stringify(usage, null, 2)}</pre>
    </div>
  {/if}
{/if}

<ConfirmDialog
  open={!!confirm}
  title={confirm?.title ?? ''}
  message={confirm?.message ?? ''}
  confirmText={confirm?.text ?? '确认'}
  danger
  requireText={confirm?.requireText ?? ''}
  busy={!!busy}
  loadingText={busy}
  onConfirm={() => void runConfirm()}
  onClose={() => { confirm = null }}
/>

<Modal open={editOpen} title="编辑应用配置" onClose={() => { editOpen = false }}>
  <form class="space-y-3" onsubmit={submitEdit}>
    <input aria-label="内存限制" bind:value={memory} type="number" placeholder="内存限制 MB（32-4096）" class="w-full rounded border border-slate-300 px-3 py-2 text-sm" />
    <input aria-label="CPU 配额" bind:value={cpu} type="number" placeholder="CPU 配额百分比（1-100）" class="w-full rounded border border-slate-300 px-3 py-2 text-sm" />
    <input aria-label="启动命令" bind:value={startCmd} placeholder="启动命令" class="w-full rounded border border-slate-300 px-3 py-2 text-sm" />
    <input aria-label="健康检查路径" bind:value={healthPath} placeholder="健康检查路径" class="w-full rounded border border-slate-300 px-3 py-2 text-sm" />
    <input aria-label="健康检查超时" bind:value={healthTimeout} type="number" placeholder="健康检查超时 ms（100-60000）" class="w-full rounded border border-slate-300 px-3 py-2 text-sm" />
    <p class="text-xs text-slate-500">留空的字段保持继承全局配置。配置下次启动生效。</p>
    <div class="flex justify-end gap-2"><button type="button" class="rounded border border-slate-300 px-3 py-1.5 text-sm" onclick={() => { editOpen = false }}>取消</button><AsyncButton type="submit" variant="primary" busy={!!busy} loadingText={busy || '保存中…'}>保存</AsyncButton></div>
  </form>
</Modal>
