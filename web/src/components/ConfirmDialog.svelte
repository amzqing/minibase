<script lang="ts">
  import Modal from './Modal.svelte'
  import { LoaderCircle } from 'lucide-svelte'

  let { open = false, title = '请确认', message = '', confirmText = '确认', danger = false,
    requireText = '', busy = false, loadingText = '处理中…', onConfirm, onClose }: {
    open?: boolean
    title?: string
    message?: string
    confirmText?: string
    danger?: boolean
    requireText?: string
    busy?: boolean
    loadingText?: string
    onConfirm: () => void
    onClose: () => void
  } = $props()

  let input = $state('')
  $effect(() => {
    if (open) input = ''
  })

  const ready = $derived((!requireText || input === requireText) && !busy)
</script>

<Modal {open} {title} {onClose}>
  <p class="text-sm leading-6 text-slate-600">{message}</p>
  {#if requireText}
    <p class="mt-3 text-xs text-slate-500">请输入 <span class="font-mono font-semibold">{requireText}</span> 以确认。</p>
    <input bind:value={input} class="mt-2 w-full rounded border border-slate-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none" placeholder={requireText} />
  {/if}
  <div class="mt-5 flex justify-end gap-2">
    <button class="rounded border border-slate-300 px-3 py-1.5 text-sm text-slate-600 hover:bg-slate-50" onclick={onClose}>取消</button>
    <button
      class="rounded px-3 py-1.5 text-sm text-white disabled:opacity-40"
      class:bg-red-600={danger}
      class:bg-blue-600={!danger}
      disabled={!ready}
      onclick={onConfirm}>{#if busy}<LoaderCircle size={14} class="mr-1 inline animate-spin" />{loadingText}{:else}{confirmText}{/if}</button>
  </div>
</Modal>
