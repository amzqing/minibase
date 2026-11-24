<script lang="ts">
  import PageHeader from '../components/PageHeader.svelte'
  import DatabaseExplorer from '../components/DatabaseExplorer.svelte'
  let { appId }: { appId: string } = $props()
</script>

<PageHeader title="数据库" subtitle={`${appId} 的库、表与索引`} />
<DatabaseExplorer {appId} />
