import { describe, expect, it } from 'vitest'
import { get } from 'svelte/store'
import { apps, clearToken, currentApp, loadToken, saveToken, toast, token } from './stores'

describe('stores', () => {
  it('saveToken / clearToken 同步 store 与 sessionStorage', () => {
    saveToken('tk')
    expect(get(token)).toBe('tk')
    expect(sessionStorage.getItem('minibase_token')).toBe('tk')

    clearToken()
    expect(get(token)).toBe('')
    expect(sessionStorage.getItem('minibase_token')).toBeNull()
  })

  it('loadToken 从 sessionStorage 恢复', () => {
    sessionStorage.setItem('minibase_token', 'restored')
    expect(loadToken()).toBe('restored')
    expect(get(token)).toBe('restored')
  })

  it('应用与应用详情 store 可写', () => {
    const app = { id: 'app_1', app_name: 'a', status: 'running', current_version: 'v1', port: 1 }
    apps.set([app])
    currentApp.set(app)
    toast.set({ type: 'success', text: 'ok' })

    expect(get(apps)).toEqual([app])
    expect(get(currentApp)).toEqual(app)
    expect(get(toast)).toEqual({ type: 'success', text: 'ok' })
  })
})
