<script lang="ts">
  import { onMount } from 'svelte'
  import { Download, Trash2, Upload } from 'lucide-svelte'
  import PageHeader from '../components/PageHeader.svelte'
  import DataTable from '../components/DataTable.svelte'
  import StatePanel from '../components/StatePanel.svelte'
  import ConfirmDialog from '../components/ConfirmDialog.svelte'
  import AsyncButton from '../components/AsyncButton.svelte'
  import { deleteFile, downloadFile, listFiles, uploadFile } from '../lib/api'
  import { toast } from '../lib/stores'
  import type { StorageObject } from '../lib/types'

  let { appId }: { appId: string } = $props()
  let files = $state<StorageObject[]>([])
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')
  let file: File | null = $state(null)
  let confirm = $state<StorageObject | null>(null)
  let busy = $state(false)
  let downloading = $state('')

  const columns = [
    { key: 'file_name', label: '文件名' },
    { key: 'size', label: '大小' },
    { key: 'content_type', label: '类型' },
    { key: 'created_at', label: '上传时间' },
    { key: 'actions', label: '操作' }
  ]

  async function load() {
    phase = 'loading'
    error = ''
    try {
      // 后端无文件时可能返回 null，统一归一化为空数组，避免 length 报错卡在 loading。
      files = (await listFiles(appId)) ?? []
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载文件失败'
      phase = 'error'
    }
  }
  onMount(load)

  async function upload(e: SubmitEvent) {
    e.preventDefault()
    if (!file || busy) return
    busy = true
    const current = file
    try {
      const result = await uploadFile(appId, current)
      toast.set({ type: 'success', text: `上传成功：${result.id}` })
      file = null
      const input = document.querySelector<HTMLInputElement>('input[type=file]')
      if (input) input.value = ''
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '上传失败' })
    } finally {
      busy = false
    }
  }

  async function download(row: StorageObject) {
    if (downloading) return
    downloading = row.id
    try {
      const { blob } = await downloadFile(appId, row.id)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = row.file_name
      a.click()
      URL.revokeObjectURL(url)
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '下载失败' })
    } finally {
      downloading = ''
    }
  }

  async function remove() {
    if (!confirm || busy) return
    const target = confirm
    confirm = null
    busy = true
    try {
      await deleteFile(appId, target.id)
      toast.set({ type: 'success', text: '文件已删除' })
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '删除失败' })
    } finally {
      busy = false
    }
  }
</script>

<PageHeader title="文件" subtitle={`应用 ${appId} 的对象存储文件`} />

<form class="mb-5 flex flex-wrap items-center gap-3 rounded-lg border border-slate-200 bg-white p-4" onsubmit={upload}>
  <input type="file" aria-label="选择文件" onchange={(e) => { file = e.currentTarget.files?.[0] ?? null }} class="text-sm" />
  <AsyncButton type="submit" variant="primary" icon={Upload} busy={busy} disabled={!file} loadingText="上传中…">上传</AsyncButton>
</form>

{#if phase === 'loading'}
  <StatePanel state="loading" />
{:else if phase === 'error'}
  <StatePanel state="error" {error} retry={load} />
{:else if files.length === 0}
  <StatePanel state="empty" />
{:else}
  <DataTable columns={columns} rows={files} rowKey={(r: any) => r.id}>
    {#snippet cell(row: any, key: string)}
      {#if key === 'size'}
        {row.size >= 1024 * 1024 ? `${(row.size / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round((row.size ?? 0) / 1024))} KB`}
      {:else if key === 'created_at'}
        {row.created_at ? new Date(row.created_at * 1000).toLocaleString() : '-'}
      {:else if key === 'actions'}
        <div class="flex gap-2">
          <AsyncButton variant="ghost" busy={downloading === row.id} loadingText="下载中" onclick={() => void download(row)} icon={Download}>下载</AsyncButton>
          <button class="text-red-600 hover:underline" aria-label="删除文件" onclick={() => { confirm = row }}><Trash2 size={14} /></button>
        </div>
      {:else}
        {row[key] ?? '-'}
      {/if}
    {/snippet}
  </DataTable>
{/if}

<ConfirmDialog open={!!confirm} title="删除文件" message={`确定删除 ${confirm?.file_name ?? ''}？`} confirmText="删除" danger onConfirm={() => void remove()} onClose={() => { confirm = null }} />
