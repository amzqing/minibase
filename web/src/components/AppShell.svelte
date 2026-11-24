<script lang="ts">
  import Sidebar from './Sidebar.svelte'
  import Toast from './Toast.svelte'
  let { children } = $props()
</script>

<div class="flex h-screen bg-slate-50">
  <Sidebar />
  <main class="flex-1 overflow-auto p-8">
    {@render children()}
  </main>
  <Toast />
</div>
