<script lang="ts">
  let { columns, rows, rowKey, cell, rowClick }: {
    columns: { key: string; label: string }[]
    rows: any[]
    rowKey?: (row: any) => string
    cell?: (row: any, key: string) => any
    rowClick?: (row: any) => void
  } = $props()
</script>

<div class="overflow-x-auto rounded-lg border border-slate-200 bg-white">
  <table class="w-full text-left text-sm">
    <thead class="bg-slate-50 text-slate-600">
      <tr>
        {#each columns as col (col.key)}
          <th class="whitespace-nowrap px-4 py-3 font-medium">{col.label}</th>
        {/each}
      </tr>
    </thead>
    <tbody class="divide-y divide-slate-100">
      {#each rows as row, i (rowKey ? rowKey(row) : i)}
        <tr class="hover:bg-slate-50" class:cursor-pointer={!!rowClick} onclick={() => rowClick?.(row)}>
          {#each columns as col (col.key)}
            <td class="px-4 py-3 text-slate-700">
              {#if cell}
                {@render cell(row, col.key)}
              {:else}
                {row[col.key] ?? '-'}
              {/if}
            </td>
          {/each}
        </tr>
      {/each}
    </tbody>
  </table>
</div>
