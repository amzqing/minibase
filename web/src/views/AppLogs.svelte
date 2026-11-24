<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { Download, RefreshCw, Search, Trash2 } from 'lucide-svelte'
  import PageHeader from '../components/PageHeader.svelte'
  import StatePanel from '../components/StatePanel.svelte'
  import AsyncButton from '../components/AsyncButton.svelte'
  import { getAppLog } from '../lib/api'
  import { toast } from '../lib/stores'
  import type { AppLogLine } from '../lib/types'

  let { appId }: { appId: string } = $props()
  let lines = $state<AppLogLine[]>([])
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')
  let refreshing = $state(false)
  let polling = $state(false)
  let timer: ReturnType<typeof setInterval> | null = null
  let keyword = $state('')
  let level = $state('ALL')

  const levels = ['ALL', 'TRACE', 'DEBUG', 'INFO', 'WARN', 'ERROR', 'FATAL', 'PANIC']

  async function load(showLoading = true) {
    if (showLoading) phase = 'loading'
    error = ''
    refreshing = true
    try {
      const result = await getAppLog(appId)
      lines = (result.lines ?? []).map((line, i) => ({
        time: line.time ?? '',
        level: line.level ?? 'INFO',
        message: line.message ?? line.raw ?? '',
        raw: line.raw ?? ''
      }))
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载日志失败'
      phase = 'error'
    } finally {
      refreshing = false
    }
  }

  function togglePolling() {
    if (timer) clearInterval(timer)
    timer = polling ? setInterval(() => void load(false), 3000) : null
  }

  function clear() {
    lines = []
    keyword = ''
    level = 'ALL'
  }

  function download() {
    const text = lines.map((l) => `${l.time ? `[${l.time}] ` : ''}${l.level} ${l.message}`).join('\n')
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `logs-${appId}.txt`
    a.click()
    URL.revokeObjectURL(url)
  }

  let filtered = $derived(
    lines.filter((line) => {
      const matchLevel = level === 'ALL' || line.level === level
      const kw = keyword.trim().toLowerCase()
      const matchText = !kw || line.message.toLowerCase().includes(kw) || line.raw.toLowerCase().includes(kw)
      return matchLevel && matchText
    })
  )

  async function copyLine(line: AppLogLine) {
    try {
      await navigator.clipboard.writeText(line.raw || line.message)
      toast.set({ type: 'success', text: '已复制日志' })
    } catch {
      toast.set({ type: 'error', text: '复制失败' })
    }
  }

  function levelClass(value: string): string {
    switch (value) {
      case 'ERROR': case 'FATAL': case 'PANIC': return 'bg-red-50 text-red-700 ring-red-200'
      case 'WARN': return 'bg-amber-50 text-amber-700 ring-amber-200'
      case 'DEBUG': case 'TRACE': return 'bg-slate-100 text-slate-600 ring-slate-200'
      default: return 'bg-emerald-50 text-emerald-700 ring-emerald-200'
    }
  }

  onMount(() => { void load() })
  onDestroy(() => { if (timer) clearInterval(timer) })
</script>

<PageHeader title="日志" subtitle={`应用 ${appId} 的运行日志 · 最近 8 KB`} />

<div class="mb-4 flex flex-wrap items-center gap-3">
  <AsyncButton icon={RefreshCw} busy={refreshing} loadingText="刷新中…" onclick={() => void load()}>刷新</AsyncButton>
  <label class="flex items-center gap-1.5 text-sm text-slate-600">
    <input type="checkbox" bind:checked={polling} onchange={togglePolling} /> 每 3 秒自动刷新
  </label>
  <div class="relative ml-auto">
    <Search size={15} class="absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-400" />
    <input bind:value={keyword} placeholder="搜索日志内容" aria-label="搜索日志内容"
      class="w-56 rounded border border-slate-300 bg-white py-1.5 pl-8 pr-3 text-sm focus:border-blue-500 focus:outline-none" />
  </div>
  <select bind:value={level} aria-label="按级别筛选" class="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
    {#each levels as item}<option value={item}>{item === 'ALL' ? '全部级别' : item}</option>{/each}
  </select>
  <AsyncButton variant="ghost" icon={Download} onclick={download}>下载</AsyncButton>
  <button class="rounded p-1.5 text-slate-400 hover:bg-red-50 hover:text-red-600" aria-label="清空日志" title="清空当前列表" onclick={clear}><Trash2 size={14} /></button>
</div>

{#if phase === 'loading'}
  <StatePanel state="loading" />
{:else if phase === 'error'}
  <StatePanel state="error" {error} retry={() => void load()} />
{:else if lines.length === 0}
  <StatePanel state="empty" />
{:else}
  <div class="overflow-hidden rounded-lg border border-slate-200 bg-white">
    <div class="flex items-center justify-between border-b border-slate-200 bg-slate-50 px-4 py-2 text-xs text-slate-500">
      <span>共 {lines.length} 条，当前显示 {filtered.length} 条</span>
      <span class="font-mono">最新在上方</span>
    </div>
    <div class="max-h-[70vh] overflow-auto">
      <table class="w-full table-fixed border-collapse text-left font-mono text-xs">
        <thead class="sticky top-0 z-10 bg-slate-50 text-slate-600">
          <tr>
            <th class="w-32 border-b border-slate-200 px-4 py-2 font-semibold">时间</th>
            <th class="w-20 border-b border-slate-200 px-4 py-2 font-semibold">级别</th>
            <th class="border-b border-slate-200 px-4 py-2 font-semibold">内容</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100">
          {#each filtered as line, i (i)}
            <tr class="align-top hover:bg-slate-50">
              <td class="whitespace-nowrap px-4 py-2 text-slate-500">{line.time || '-'}</td>
              <td class="px-4 py-2">
                <span class={`inline-flex rounded px-1.5 py-0.5 text-[10px] font-semibold ring-1 ring-inset ${levelClass(line.level)}`}>{line.level}</span>
              </td>
              <td class="break-all px-4 py-2 text-slate-800">
                <button class="w-full text-left hover:text-blue-600" title="点击复制原始日志" onclick={() => void copyLine(line)}>{line.message || line.raw}</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if filtered.length === 0}
        <div class="px-4 py-10 text-center text-sm text-slate-500">没有匹配的日志</div>
      {/if}
    </div>
  </div>
{/if}
