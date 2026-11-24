import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import EnvEditor from './EnvEditor.svelte'

describe('EnvEditor', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('可以把变量标记为 secret', async () => {
    const onSave = vi.fn()
    render(EnvEditor, {
      props: {
        items: [{ key: 'NODE_ENV', value: 'production', is_secret: false }],
        mode: 'env',
        onSave
      }
    })

    const checkbox = screen.getByLabelText('密钥-0') as HTMLInputElement
    await fireEvent.click(checkbox)
    expect(checkbox.checked).toBe(true)

    await fireEvent.click(screen.getByText('保存'))
    expect(onSave).toHaveBeenCalledWith([
      { key: 'NODE_ENV', value: 'production', is_secret: true }
    ])
  })
})
