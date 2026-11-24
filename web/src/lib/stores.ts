import { writable } from 'svelte/store'
import type { App, ToastMessage } from './types'

/** 管理员 Token，只存内存，页面刷新靠 sessionStorage 恢复。 */
export const token = writable<string>('')

/** 摊平后的应用列表，每个应用自带 project_id。 */
export const apps = writable<App[]>([])

/** 当前进入的应用。 */
export const currentApp = writable<App | null>(null)

/** 全局一次性提示。 */
export const toast = writable<ToastMessage | null>(null)

const TOKEN_KEY = 'minibase_token'

/** 从 sessionStorage 恢复 Token。 */
export function loadToken(): string {
  const t = sessionStorage.getItem(TOKEN_KEY) ?? ''
  token.set(t)
  return t
}

/** 写入 Token 并同步 sessionStorage。 */
export function saveToken(t: string): void {
  if (t) sessionStorage.setItem(TOKEN_KEY, t)
  else sessionStorage.removeItem(TOKEN_KEY)
  token.set(t)
}

/** 清除 Token。 */
export function clearToken(): void {
  saveToken('')
}
