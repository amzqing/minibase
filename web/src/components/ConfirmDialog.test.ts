import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ConfirmDialog from './ConfirmDialog.svelte'

describe('ConfirmDialog', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('取消不触发确认动作', async () => {
    const onConfirm = vi.fn()
    const onClose = vi.fn()
    render(ConfirmDialog, { props: { open: true, message: '确定吗？', onConfirm, onClose } })

    await fireEvent.click(screen.getByText('取消'))
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('requireText 不匹配时禁止确认', async () => {
    const onConfirm = vi.fn()
    render(ConfirmDialog, {
      props: { open: true, message: '删除应用', requireText: 'shop', onConfirm, onClose: () => {} }
    })

    const button = screen.getByText('确认').closest('button') as HTMLButtonElement
    expect(button.disabled).toBe(true)
    await fireEvent.input(screen.getByPlaceholderText('shop'), { target: { value: 'wrong' } })
    expect(button.disabled).toBe(true)
  })
})
