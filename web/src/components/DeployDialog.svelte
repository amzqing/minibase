<script lang="ts">
  import Modal from './Modal.svelte'
  import { deployApp } from '../lib/api'
  import { toast } from '../lib/stores'

  let { open = false, onClose, onDeployed }: {
    open?: boolean
    onClose: () => void
    onDeployed: () => void
  } = $props()

  let file: File | null = $state(null)
  let appName = $state('')
  let mode = $state<'a' | 'b'>('b')
  let busy = $state(false)

  $effect(() => {
    if (open) {
      file = null
      appName = ''
      mode = 'b'
      busy = false
    }
  })

  const valid = $derived(!!file && appName.trim().length > 0 && (mode === 'a' || mode === 'b'))

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    if (!valid || busy || !file) return
    busy = true
    try {
      const form = new FormData()
      form.append('file', file)
      form.append('app', appName.trim())
      form.append('mode', mode)
      const result = await deployApp(form)
      toast.set({ type: 'success', text: `部署成功：${result.app_name ?? appName.trim()}` })
      onDeployed()
      onClose()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '部署失败' })
    } finally {
      busy = false
    }
  }
</script>

<Modal {open} title="部署应用" {onClose}>
  <form class="space-y-4" onsubmit={submit}>
    <div>
      <label class="mb-1 block text-sm text-slate-600" for="deploy-file">项目 zip</label>
      <input id="deploy-file" type="file" accept=".zip,application/zip"
        class="w-full text-sm"
        onchange={(e) => { file = e.currentTarget.files?.[0] ?? null }} />
    </div>
    <div>
      <label class="mb-1 block text-sm text-slate-600" for="deploy-app">应用名</label>
      <input id="deploy-app" bind:value={appName} class="w-full rounded border border-slate-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none" placeholder="shop" />
    </div>
    <div>
      <label class="mb-1 block text-sm text-slate-600" for="deploy-mode">部署模式</label>
      <select id="deploy-mode" bind:value={mode} class="w-full rounded border border-slate-300 bg-white px-3 py-2 text-sm">
        <option value="b">b：平台执行 npm install</option>
        <option value="a">a：包内已带 node_modules</option>
      </select>
    </div>
    <div class="flex justify-end gap-2 pt-2">
      <button type="button" class="rounded border border-slate-300 px-3 py-1.5 text-sm text-slate-600 hover:bg-slate-50" onclick={onClose}>取消</button>
      <button type="submit" disabled={!valid || busy} class="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-40">
        {busy ? '部署中…' : '开始部署'}
      </button>
    </div>
  </form>
</Modal>
