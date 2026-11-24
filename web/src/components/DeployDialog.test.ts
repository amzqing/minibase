import { cleanup, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import DeployDialog from './DeployDialog.svelte'
import * as api from '../lib/api'

describe('DeployDialog', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('没有 zip 和应用名时禁止部署，并限制 mode', async () => {
    render(DeployDialog, { props: { open: true, onClose: () => {}, onDeployed: () => {} } })

    const submit = screen.getByText('开始部署').closest('button') as HTMLButtonElement
    expect(submit.disabled).toBe(true)

    const mode = screen.getByLabelText('部署模式') as HTMLSelectElement
    expect(Array.from(mode.options).map((o) => o.value)).toEqual(['b', 'a'])

    const deploy = vi.spyOn(api, 'deployApp').mockResolvedValue({ app_name: 'shop' })
    expect(deploy).not.toHaveBeenCalled()
  })
})
