<script lang="ts">
  let { status }: { status: string } = $props()

  const map: Record<string, string> = {
    '运行中': 'bg-green-100 text-green-800',
    'running': 'bg-green-100 text-green-800',
    'ok': 'bg-green-100 text-green-800',
    '已暂停': 'bg-amber-100 text-amber-800',
    'paused': 'bg-amber-100 text-amber-800',
    '已停止': 'bg-slate-200 text-slate-700',
    'stopped': 'bg-slate-200 text-slate-700',
    '失败': 'bg-red-100 text-red-800',
    'failed': 'bg-red-100 text-red-800',
    '不健康': 'bg-orange-100 text-orange-800',
    'unhealthy': 'bg-orange-100 text-orange-800',
    '部署中': 'bg-blue-100 text-blue-800',
    'deploying': 'bg-blue-100 text-blue-800'
  }
  const cls = $derived(map[status] ?? 'bg-slate-100 text-slate-600')
</script>

<span class="inline-flex rounded-full px-2 py-0.5 text-xs font-medium {cls}">{status || '未知'}</span>
