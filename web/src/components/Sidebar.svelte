<script lang="ts">
  import {
    Activity, Database, FileText, KeyRound, LayoutGrid, ListOrdered, ScrollText,
    Settings, Shield, Telescope
  } from 'lucide-svelte'
  import { appPath, navigate, route, type AppSection } from '../lib/router'
  import { currentApp } from '../lib/stores'

  const sections: { key: AppSection; label: string; icon: any }[] = [
    { key: 'overview', label: '概览', icon: Telescope },
    { key: 'database', label: '数据库', icon: Database },
    { key: 'files', label: '文件', icon: FileText },
    { key: 'env', label: '环境变量', icon: KeyRound },
    { key: 'logs', label: '日志', icon: ScrollText },
    { key: 'versions', label: '版本', icon: ListOrdered },
    { key: 'security', label: '安全', icon: Shield }
  ]

  const inApp = $derived($route.name === 'app')
  const appId = $derived(inApp && $route.name === 'app' ? $route.appId : '')
</script>

<aside class="flex w-60 shrink-0 flex-col border-r border-slate-800 bg-slate-900 text-slate-200">
  <div class="border-b border-slate-800 px-5 py-4 text-lg font-semibold tracking-wide">MiniBase</div>

  <nav class="flex-1 overflow-y-auto px-3 py-4">
    <button
      class="flex w-full items-center gap-2 rounded px-3 py-2 text-left text-sm hover:bg-slate-800"
      class:bg-slate-800={$route.name === 'apps'}
      onclick={() => navigate('/apps')}>
      <LayoutGrid size={16} /> 应用管理
    </button>

    {#if inApp && appId}
      <div class="mt-2 rounded bg-slate-950/40 py-2">
        <p class="px-3 pb-1 text-xs text-slate-500">{$currentApp?.app_name ?? appId}</p>
        {#each sections as section (section.key)}
          {@const Icon = section.icon}
          <button
            class="flex w-full items-center gap-2 rounded px-3 py-1.5 text-left text-sm text-slate-300 hover:bg-slate-800"
            class:bg-slate-700={$route.name === 'app' && $route.section === section.key}
            onclick={() => navigate(appPath(appId, section.key))}>
            <Icon size={14} /> {section.label}
          </button>
        {/each}
      </div>
    {/if}
  </nav>

  <div class="border-t border-slate-800 px-3 py-4">
    <button
      class="flex w-full items-center gap-2 rounded px-3 py-2 text-sm text-slate-400 hover:bg-slate-800"
      class:bg-slate-800={$route.name === 'system'}
      onclick={() => navigate('/system')}>
      <Settings size={16} /> 系统设置
    </button>
    <p class="mt-3 px-3 text-xs text-slate-600">管控面板</p>
  </div>
</aside>
