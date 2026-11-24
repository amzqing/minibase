<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte'

  type Variant = 'primary' | 'secondary' | 'danger' | 'ghost'

  let {
    busy = false,
    disabled = false,
    variant = 'secondary',
    icon: Icon,
    loadingText = '处理中…',
    onclick,
    type = 'button',
    class: className = '',
    children
  }: {
    busy?: boolean
    disabled?: boolean
    variant?: Variant
    icon?: any
    loadingText?: string
    onclick?: (event: MouseEvent) => void
    type?: 'button' | 'submit'
    class?: string
    children?: any
  } = $props()

  const variants: Record<Variant, string> = {
    primary: 'bg-blue-600 text-white hover:bg-blue-700 disabled:bg-blue-300',
    secondary: 'border border-slate-300 bg-white text-slate-700 hover:bg-slate-50 disabled:text-slate-400',
    danger: 'border border-red-200 bg-white text-red-600 hover:bg-red-50 disabled:text-red-300',
    ghost: 'text-blue-600 hover:underline disabled:text-slate-400'
  }
</script>

<button
  {type}
  disabled={busy || disabled}
  {onclick}
  class={`inline-flex items-center justify-center gap-1 rounded px-3 py-1.5 text-sm transition-colors disabled:cursor-not-allowed ${variants[variant]} ${className}`}
>
  {#if busy}
    <LoaderCircle size={14} class="animate-spin" />
    {loadingText}
  {:else}
    {#if Icon}<Icon size={14} />{/if}
    {@render children?.()}
  {/if}
</button>
