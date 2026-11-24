<script lang="ts">
  import { verifyToken } from '../lib/api'
  import { toast } from '../lib/stores'

  let value = $state('')
  let busy = $state(false)
  let error = $state('')

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    if (!value.trim() || busy) return
    busy = true
    error = ''
    try {
      await verifyToken(value.trim())
      toast.set({ type: 'success', text: '登录成功' })
    } catch (e) {
      error = e instanceof Error ? e.message : '登录失败'
    } finally {
      busy = false
    }
  }
</script>

<div class="flex min-h-screen items-center justify-center bg-slate-100">
  <form class="w-full max-w-sm rounded-lg border border-slate-200 bg-white p-8 shadow-sm" onsubmit={submit}>
    <h1 class="mb-6 text-center text-xl font-semibold text-slate-800">MiniBase 管控面板</h1>

    <label class="mb-2 block text-sm text-slate-600" for="token">管理员 Token</label>
    <input id="token" type="password" bind:value placeholder="请输入 Token"
      class="w-full rounded border border-slate-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none" />

    {#if error}
      <p class="mt-2 text-sm text-red-600">{error}</p>
    {/if}

    <button type="submit" disabled={busy || !value.trim()}
      class="mt-5 w-full rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
      {busy ? '验证中…' : '进入面板'}
    </button>

    <p class="mt-4 text-center text-xs text-slate-400">Token 只保存在当前浏览器会话</p>
  </form>
</div>
