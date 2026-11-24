<script lang="ts">
  import { onMount } from 'svelte'
  import { get } from 'svelte/store'
  import { listApps } from './lib/api'
  import { apps, currentApp, loadToken, token } from './lib/stores'
  import { route } from './lib/router'
  import AppShell from './components/AppShell.svelte'
  import Login from './views/Login.svelte'
  import AppList from './views/AppList.svelte'
  import AppOverview from './views/AppOverview.svelte'
  import AppDatabase from './views/AppDatabase.svelte'
  import AppFiles from './views/AppFiles.svelte'
  import AppEnv from './views/AppEnv.svelte'
  import AppLogs from './views/AppLogs.svelte'
  import AppVersions from './views/AppVersions.svelte'
  import AppSecurity from './views/AppSecurity.svelte'
  import SystemSettings from './views/SystemSettings.svelte'

  let ready = $state(false)

  async function ensureCurrentApp(appId: string) {
    if (get(currentApp)?.id === appId) return
    let found = get(apps).find((a) => a.id === appId)
    if (!found) {
      try {
        const list = await listApps()
        apps.set(list)
        found = list.find((a) => a.id === appId)
      } catch {
        found = undefined
      }
    }
    currentApp.set(found ?? { id: appId, app_name: appId, status: '', current_version: '', port: 0 })
  }

  $effect(() => {
    if ($route.name === 'app') void ensureCurrentApp($route.appId)
  })

  onMount(() => {
    loadToken()
    ready = true
  })
</script>

{#if !ready}
  <div class="flex h-screen items-center justify-center text-slate-500">加载中…</div>
{:else if !$token}
  <Login />
{:else}
  <AppShell>
    {#if $route.name === 'apps'}
      <AppList />
    {:else if $route.name === 'app'}
      {#if $route.section === 'database'}
        <AppDatabase appId={$route.appId} />
      {:else if $route.section === 'files'}
        <AppFiles appId={$route.appId} />
      {:else if $route.section === 'env'}
        <AppEnv appId={$route.appId} />
      {:else if $route.section === 'logs'}
        <AppLogs appId={$route.appId} />
      {:else if $route.section === 'versions'}
        <AppVersions appId={$route.appId} />
      {:else if $route.section === 'security'}
        <AppSecurity appId={$route.appId} />
      {:else}
        <AppOverview appId={$route.appId} />
      {/if}
    {:else if $route.name === 'system'}
      <SystemSettings />
    {:else}
      <AppList />
    {/if}
  </AppShell>
{/if}
