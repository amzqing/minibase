<script lang="ts">
  import { Database, Plus, RefreshCw, Trash2 } from 'lucide-svelte'
  import Modal from './Modal.svelte'
  import ConfirmDialog from './ConfirmDialog.svelte'
  import AsyncButton from './AsyncButton.svelte'
  import StatePanel from './StatePanel.svelte'
  import {
    createDatabase, createIndex, createTable, deleteDatabase, dropIndex, dropTable,
    getTable, listDatabases, listTables, renameTable
  } from '../lib/api'
  import { toast } from '../lib/stores'
  import type { DatabaseInfo, TableDetail, TableIndex, TableItem } from '../lib/types'

  let { appId }: { appId: string } = $props()

  let databases = $state<DatabaseInfo[]>([])
  let selectedDb = $state('')
  let tables = $state<TableItem[]>([])
  let selectedTable = $state('')
  let detail = $state<TableDetail | null>(null)
  let phase = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state('')

  let dbModal = $state(false)
  let dbName = $state('')
  let dbDefault = $state(false)

  let tableModal = $state(false)
  let tableName = $state('')
  let fields = $state([{ name: '', type: 'TEXT', not_null: false }])

  let indexModal = $state(false)
  let indexName = $state('')
  let indexFields = $state('')
  let indexUnique = $state(false)

  let renameModal = $state(false)
  let renameValue = $state('')

  let confirm = $state<{ title: string; message: string; requireText?: string; action: () => Promise<void> } | null>(null)
  let acting = $state('')

  function fail(e: unknown, fallback: string) {
    toast.set({ type: 'error', text: e instanceof Error ? e.message : fallback })
  }

  async function loadDatabases(keepSelection = true) {
    phase = 'loading'
    error = ''
    try {
      databases = await listDatabases(appId)
      const keep = keepSelection && databases.some((db) => db.name === selectedDb)
      selectedDb = keep ? selectedDb : (databases[0]?.name ?? '')
      if (!selectedDb) {
        tables = []
        selectedTable = ''
        detail = null
        phase = 'ready'
        return
      }
      await loadTables(keepSelection)
      phase = 'ready'
    } catch (e) {
      error = e instanceof Error ? e.message : '加载数据库失败'
      phase = 'error'
    }
  }

  async function loadTables(keepSelection = true) {
    if (!selectedDb) return
    try {
      tables = await listTables(appId, selectedDb)
      const keep = keepSelection && tables.some((t) => t.name === selectedTable)
      selectedTable = keep ? selectedTable : (tables[0]?.name ?? '')
      if (!selectedTable) {
        detail = null
        return
      }
      await loadDetail()
    } catch (e) {
      fail(e, '加载表失败')
    }
  }

  async function loadDetail() {
    if (!selectedDb || !selectedTable) return
    try {
      detail = await getTable(appId, selectedDb, selectedTable)
    } catch (e) {
      fail(e, '加载表结构失败')
    }
  }

  async function selectDb(name: string) {
    selectedDb = name
    selectedTable = ''
    detail = null
    await loadTables(false)
  }

  async function selectTable(name: string) {
    selectedTable = name
    await loadDetail()
  }

  async function submitDb(e: SubmitEvent) {
    e.preventDefault()
    if (acting) return
    acting = '创建中…'
    if (!dbName.trim()) return
    try {
      await createDatabase(appId, dbName.trim(), dbDefault)
      toast.set({ type: 'success', text: '数据库已创建' })
      dbModal = false
      dbName = ''
      dbDefault = false
      selectedDb = dbName.trim()
      await loadDatabases(false)
    } catch (e) {
      fail(e, '创建数据库失败')
    } finally {
      acting = ''
    }
  }

  async function submitTable(e: SubmitEvent) {
    e.preventDefault()
    if (acting) return
    acting = '建表中…'
    const cleaned = fields
      .map((f) => ({ ...f, name: f.name.trim(), type: f.type as 'TEXT' | 'INTEGER' | 'REAL' | 'BLOB' }))
      .filter((f) => f.name)
    if (!tableName.trim() || cleaned.length === 0) return
    try {
      await createTable(appId, selectedDb, { name: tableName.trim(), fields: cleaned })
      toast.set({ type: 'success', text: '表已创建' })
      tableModal = false
      tableName = ''
      fields = [{ name: '', type: 'TEXT', not_null: false }]
      selectedTable = cleaned[0].name
      await loadTables(false)
    } catch (e) {
      fail(e, '建表失败')
    } finally {
      acting = ''
    }
  }

  async function submitIndex(e: SubmitEvent) {
    e.preventDefault()
    if (acting) return
    acting = '创建中…'
    const list = indexFields.split(',').map((s) => s.trim()).filter(Boolean)
    if (!indexName.trim() || list.length === 0) return
    try {
      await createIndex(appId, selectedDb, selectedTable, { name: indexName.trim(), fields: list, unique: indexUnique })
      toast.set({ type: 'success', text: '索引已创建' })
      indexModal = false
      indexName = ''
      indexFields = ''
      indexUnique = false
      await loadDetail()
    } catch (e) {
      fail(e, '建索引失败')
    } finally {
      acting = ''
    }
  }

  async function submitRename(e: SubmitEvent) {
    e.preventDefault()
    if (acting) return
    acting = '重命名中…'
    if (!renameValue.trim()) return
    try {
      await renameTable(appId, selectedDb, selectedTable, renameValue.trim())
      toast.set({ type: 'success', text: '表已重命名' })
      renameModal = false
      selectedTable = renameValue.trim()
      renameValue = ''
      await loadTables(false)
    } catch (e) {
      fail(e, '重命名失败')
    } finally {
      acting = ''
    }
  }

  function askDelete(title: string, message: string, action: () => Promise<void>, requireText = '') {
    confirm = { title, message, action, requireText }
  }

  async function runConfirm() {
    const current = confirm
    if (!current || acting) return
    confirm = null
    acting = '删除中…'
    try {
      await current.action()
      toast.set({ type: 'success', text: '已删除' })
      await loadDatabases(false)
    } catch (e) {
      fail(e, '删除失败')
    } finally {
      acting = ''
    }
  }

  $effect(() => { void loadDatabases() })
</script>

<div class="grid gap-5 lg:grid-cols-[260px_1fr]">
  <section class="rounded-lg border border-slate-200 bg-white p-4">
    <div class="mb-3 flex items-center justify-between">
      <h2 class="flex items-center gap-1 text-sm font-semibold text-slate-700"><Database size={14} /> 数据库</h2>
      <AsyncButton variant="ghost" icon={RefreshCw} busy={phase === 'loading' && !!databases.length} loadingText="" class="!px-1 !py-1" onclick={() => void loadDatabases()}>刷新</AsyncButton>
    </div>
    <div class="space-y-1">
      {#each databases as db (db.name)}
        <button
          class="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-sm hover:bg-slate-50"
          class:bg-slate-100={selectedDb === db.name}
          onclick={() => selectDb(db.name)}>
          <span class="truncate">{db.name}{db.is_default ? '（默认）' : ''}</span>
          {#if !db.is_default}
            <span role="button" tabindex="0" class="text-slate-400 hover:text-red-600"
              onclick={(e) => { e.stopPropagation(); askDelete('删除数据库', `确定删除数据库 ${db.name}？此操作不可恢复。`, async () => { await deleteDatabase(appId, db.name) }, db.name) }}
              onkeydown={(e) => { if (e.key === 'Enter') e.currentTarget.click() }}>
              <Trash2 size={13} />
            </span>
          {/if}
        </button>
      {/each}
    </div>
    <button class="mt-3 flex w-full items-center justify-center gap-1 rounded border border-dashed border-slate-300 py-1.5 text-xs text-slate-600 hover:bg-slate-50" onclick={() => { dbName = ''; dbDefault = false; dbModal = true }}>
      <Plus size={13} /> 新建库
    </button>
  </section>

  <section class="space-y-4">
    {#if phase === 'loading'}
      <StatePanel state="loading" />
    {:else if phase === 'error'}
      <StatePanel state="error" {error} retry={() => loadDatabases(false)} />
    {:else if !selectedDb}
      <StatePanel state="empty" />
    {:else}
      <div class="rounded-lg border border-slate-200 bg-white p-4">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-sm font-semibold text-slate-700">表 · {selectedDb}</h2>
          <button class="flex items-center gap-1 rounded bg-blue-600 px-2.5 py-1 text-xs text-white hover:bg-blue-700" onclick={() => { tableName = ''; fields = [{ name: '', type: 'TEXT', not_null: false }]; tableModal = true }}>
            <Plus size={13} /> 建表
          </button>
        </div>
        <div class="flex flex-wrap gap-2">
          {#each tables as table (table.name)}
            <button class="rounded border px-2.5 py-1 text-xs hover:bg-slate-50" class:border-blue-500={selectedTable === table.name} class:text-blue-700={selectedTable === table.name} onclick={() => selectTable(table.name)}>
              {table.name} <span class="text-slate-400">({table.row_count ?? 0})</span>
            </button>
          {/each}
        </div>
      </div>

      {#if selectedTable}
        <div class="rounded-lg border border-slate-200 bg-white p-4">
          <div class="mb-3 flex items-center justify-between">
            <h2 class="text-sm font-semibold text-slate-700">表结构 · {selectedTable}</h2>
            <div class="flex gap-2">
              <button class="rounded border border-slate-300 px-2 py-1 text-xs hover:bg-slate-50" onclick={() => { renameValue = selectedTable; renameModal = true }}>重命名</button>
              <button class="rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50" onclick={() => askDelete('删除表', `确定删除表 ${selectedTable}？`, async () => { await dropTable(appId, selectedDb, selectedTable) })}>删表</button>
            </div>
          </div>
          {#if detail}
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <h3 class="mb-2 text-xs font-semibold text-slate-500">字段</h3>
                <div class="space-y-1">
                  {#each detail.columns as col (col.name)}
                    <div class="flex items-center justify-between rounded bg-slate-50 px-2 py-1 text-xs">
                      <span class="font-mono">{col.name}</span>
                      <span class="text-slate-500">{col.type}{col.primary_key ? ' · PK' : ''}{col.not_null ? ' · NOT NULL' : ''}</span>
                    </div>
                  {/each}
                </div>
              </div>
              <div>
                <div class="mb-2 flex items-center justify-between">
                  <h3 class="text-xs font-semibold text-slate-500">索引</h3>
                  <button class="rounded border border-slate-300 px-2 py-0.5 text-xs hover:bg-slate-50" onclick={() => { indexName = ''; indexFields = ''; indexUnique = false; indexModal = true }}>建索引</button>
                </div>
                <div class="space-y-1">
                  {#each detail.indexes as index (index.name)}
                    <div class="flex items-center justify-between rounded bg-slate-50 px-2 py-1 text-xs">
                      <span class="font-mono">{index.name}{index.unique ? ' · UNIQUE' : ''}</span>
                      <button class="text-slate-400 hover:text-red-600" onclick={() => askDelete('删除索引', `确定删除索引 ${index.name}？`, async () => { await dropIndex(appId, selectedDb, selectedTable, index.name) })}>删除</button>
                    </div>
                  {:else}
                    <p class="text-xs text-slate-400">暂无索引</p>
                  {/each}
                </div>
              </div>
            </div>
          {/if}
        </div>
      {/if}
    {/if}
  </section>
</div>

<Modal open={dbModal} title="新建数据库" onClose={() => { dbModal = false }}>
  <form class="space-y-3" onsubmit={submitDb}>
    <input bind:value={dbName} class="w-full rounded border border-slate-300 px-3 py-2 text-sm" placeholder="库名，例如 analytics" />
    <label class="flex items-center gap-2 text-sm text-slate-600"><input type="checkbox" bind:checked={dbDefault} /> 设为默认库</label>
    <div class="flex justify-end gap-2"><button type="button" class="rounded border border-slate-300 px-3 py-1.5 text-sm" onclick={() => { dbModal = false }}>取消</button><AsyncButton type="submit" variant="primary" busy={!!acting} loadingText={acting || '创建中…'}>创建</AsyncButton></div>
  </form>
</Modal>

<Modal open={tableModal} title="新建表" onClose={() => { tableModal = false }}>
  <form class="space-y-3" onsubmit={submitTable}>
    <input bind:value={tableName} class="w-full rounded border border-slate-300 px-3 py-2 text-sm" placeholder="表名，例如 users" />
    {#each fields as field, i (i)}
      <div class="flex gap-2">
        <input aria-label={`字段名-${i}`} bind:value={field.name} class="w-32 rounded border border-slate-300 px-2 py-1.5 text-sm" placeholder="字段名" />
        <select aria-label={`字段类型-${i}`} bind:value={field.type} class="rounded border border-slate-300 px-2 py-1.5 text-sm">
          <option>TEXT</option><option>INTEGER</option><option>REAL</option><option>BLOB</option>
        </select>
        <label class="flex items-center gap-1 text-xs text-slate-600"><input type="checkbox" bind:checked={field.not_null} /> 非空</label>
        <button type="button" class="text-slate-400 hover:text-red-600" onclick={() => { fields = fields.filter((_, idx) => idx !== i) }}><Trash2 size={14} /></button>
      </div>
    {/each}
    <button type="button" class="rounded border border-dashed border-slate-300 px-2 py-1 text-xs" onclick={() => { fields = [...fields, { name: '', type: 'TEXT', not_null: false }] }}>加字段</button>
    <div class="flex justify-end gap-2"><button type="button" class="rounded border border-slate-300 px-3 py-1.5 text-sm" onclick={() => { tableModal = false }}>取消</button><AsyncButton type="submit" variant="primary" busy={!!acting} loadingText={acting || '建表中…'}>建表</AsyncButton></div>
  </form>
</Modal>

<Modal open={indexModal} title="新建索引" onClose={() => { indexModal = false }}>
  <form class="space-y-3" onsubmit={submitIndex}>
    <input bind:value={indexName} class="w-full rounded border border-slate-300 px-3 py-2 text-sm" placeholder="索引名" />
    <input bind:value={indexFields} class="w-full rounded border border-slate-300 px-3 py-2 text-sm" placeholder="字段，多个用逗号分隔" />
    <label class="flex items-center gap-2 text-sm text-slate-600"><input type="checkbox" bind:checked={indexUnique} /> 唯一索引</label>
    <div class="flex justify-end gap-2"><button type="button" class="rounded border border-slate-300 px-3 py-1.5 text-sm" onclick={() => { indexModal = false }}>取消</button><AsyncButton type="submit" variant="primary" busy={!!acting} loadingText={acting || '创建中…'}>创建</AsyncButton></div>
  </form>
</Modal>

<Modal open={renameModal} title="重命名表" onClose={() => { renameModal = false }}>
  <form class="space-y-3" onsubmit={submitRename}>
    <input bind:value={renameValue} class="w-full rounded border border-slate-300 px-3 py-2 text-sm" />
    <div class="flex justify-end gap-2"><button type="button" class="rounded border border-slate-300 px-3 py-1.5 text-sm" onclick={() => { renameModal = false }}>取消</button><AsyncButton type="submit" variant="primary" busy={!!acting} loadingText={acting || '保存中…'}>保存</AsyncButton></div>
  </form>
</Modal>

<ConfirmDialog
  open={!!confirm}
  title={confirm?.title ?? '确认删除'}
  message={confirm?.message ?? ''}
  confirmText="删除"
  danger
  requireText={confirm?.requireText ?? ''}
  onConfirm={() => void runConfirm()}
  onClose={() => { confirm = null }}
/>
