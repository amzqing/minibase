<script lang="ts">
  import { onMount } from 'svelte'
  import PageHeader from '../components/PageHeader.svelte'
  import StatePanel from '../components/StatePanel.svelte'
  import EnvEditor from '../components/EnvEditor.svelte'
  import { getEnv, listSecrets, setEnv, setSecrets } from '../lib/api'
  import { toast } from '../lib/stores'
  import type { EnvItem } from '../lib/types'

  let { appId }: { appId: string } = $props()
  let envItems = $state<EnvItem[]>([])
  let secretItems = $state<EnvItem[]>([])
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')
  let reloading = $state(false)

  async function load() {
    reloading = true
    error = ''
    try {
      const [env, secrets] = await Promise.all([getEnv(appId), listSecrets(appId)])
      envItems = env
      secretItems = secrets
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载环境变量失败'
      phase = 'error'
    } finally {
      reloading = false
    }
  }
  onMount(load)

  async function saveEnv(rows: EnvItem[]) {
    const body: Record<string, unknown> = {}
    const secretKeys: string[] = []
    for (const row of rows) {
      if (row.value === '<encrypted>' && row.is_secret) continue
      body[row.key] = row.value
      if (row.is_secret) secretKeys.push(row.key)
    }
    body._secret = secretKeys
    try {
      await setEnv(appId, body)
      toast.set({ type: 'success', text: '环境变量已保存，下次部署生效' })
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '保存失败' })
    }
  }

  async function saveSecrets(rows: EnvItem[]) {
    const body: Record<string, string> = {}
    for (const row of rows) {
      if (row.value && row.value !== '<encrypted>') body[row.key] = row.value
    }
    if (Object.keys(body).length === 0) return
    try {
      await setSecrets(appId, body)
      toast.set({ type: 'success', text: '密钥已写入' })
      await load()
    } catch (e) {
      toast.set({ type: 'error', text: e instanceof Error ? e.message : '保存失败' })
    }
  }
</script>

<PageHeader title="环境变量" subtitle={`${appId} 的环境变量与密钥，配置下次部署生效`} />

{#if phase === 'loading'}
  <StatePanel state="loading" />
{:else if phase === 'error'}
  <StatePanel state="error" {error} retry={load} />
{:else}
  <div class="space-y-5">
    <section class="rounded-lg border border-slate-200 bg-white p-4">
      <h2 class="mb-3 text-sm font-semibold text-slate-700">普通变量与密钥</h2>
      <EnvEditor items={envItems} mode="env" onSave={saveEnv} />
    </section>
    <section class="rounded-lg border border-slate-200 bg-white p-4">
      <h2 class="mb-1 text-sm font-semibold text-slate-700">独立密钥</h2>
      <p class="mb-3 text-xs text-slate-500">已有密钥显示为密文，留空表示不修改。</p>
      <EnvEditor items={secretItems} mode="secret" onSave={saveSecrets} />
    </section>
  </div>
{/if}
