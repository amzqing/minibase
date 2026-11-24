import { get } from 'svelte/store'
import { clearToken, saveToken, token } from './stores'
import type {
  ApiError, App, AppDetail, AuditLog, CorsDomain, DatabaseDetail, DatabaseInfo, DeployHistoryItem,
  DeployResult, EnvItem, LogControlStatus, StorageObject, SystemUsage, TableDetail, TableItem,
  TokenRotateResult, VersionItem
} from './types'

const BASE = '/api'

function authHeaders(init: RequestInit): Headers {
  const headers = new Headers(init.headers)
  const current = get(token)
  if (current) headers.set('Authorization', `Bearer ${current}`)
  return headers
}

function handleUnauthorized(): never {
  clearToken()
  if (typeof window !== 'undefined') window.location.hash = '#/'
  throw new Error('Token 已失效，请重新登录')
}

async function readError(res: Response): Promise<string> {
  try {
    const body = await res.json() as ApiError
    return body.error ?? `请求失败（${res.status}）`
  } catch {
    return `请求失败（${res.status}）`
  }
}

/** 统一的 JSON 请求封装：Bearer、JSON 解析、401 跳登录。 */
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = authHeaders(init)
  let res: Response
  try {
    res = await fetch(BASE + path, { ...init, headers })
  } catch (e) {
    throw new Error(`网络请求失败：${e instanceof Error ? e.message : String(e)}`)
  }
  if (res.status === 401) handleUnauthorized()
  if (!res.ok) throw new Error(await readError(res))
  try {
    return await res.json() as T
  } catch {
    return undefined as T
  }
}

/** JSON 请求快捷方法。FormData 不要走这里。 */
function jsonRequest<T>(path: string, method: string, body?: unknown): Promise<T> {
  return request<T>(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body)
  })
}

/** 二进制下载，保留 Content-Type。 */
async function requestBlob(path: string): Promise<{ blob: Blob; contentType: string }> {
  const res = await fetch(BASE + path, { headers: authHeaders({}) })
  if (res.status === 401) handleUnauthorized()
  if (!res.ok) throw new Error(await readError(res))
  return { blob: await res.blob(), contentType: res.headers.get('Content-Type') ?? 'application/octet-stream' }
}

function enc(value: string): string {
  return encodeURIComponent(value)
}

/** 用一次真实请求验证 Token；成功即写入 store。 */
export async function verifyToken(value: string): Promise<void> {
  saveToken(value)
  try {
    await listApps()
  } catch (e) {
    clearToken()
    throw e
  }
}

// ── 系统 ──────────────────────────────────────────────────────────

export function systemHealth(): Promise<{ status: string }> {
  return request('/system/health')
}

export function getSystemUsage(): Promise<SystemUsage> {
  return request('/system/usage')
}

export function listAuditLogs(limit = 100): Promise<AuditLog[]> {
  return request(`/system/audit?limit=${limit}`)
}

export function getLogControl(): Promise<LogControlStatus> {
  return request('/system/log-control')
}

export function setLogControl(body: {
  scope: 'control' | 'app'
  app_name?: string
  enabled: boolean
  duration_sec?: number
  level?: 'all' | 'error'
}): Promise<{ status: string }> {
  return jsonRequest('/system/log-control', 'POST', body)
}

export function rotateToken(): Promise<TokenRotateResult> {
  return jsonRequest('/system/token/rotate', 'POST')
}

// ── 应用 ──────────────────────────────────────────────────────────

export function listApps(): Promise<App[]> {
  return request('/apps')
}

/** 部署应用；FormData 由调用方组装，Content-Type 由浏览器设置。 */
export function deployApp(form: FormData): Promise<DeployResult> {
  return request('/apps', { method: 'POST', body: form })
}

export function getApp(appId: string): Promise<AppDetail> {
  return request(`/apps/${enc(appId)}`)
}

export function updateApp(appId: string, body: Record<string, unknown>): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}`, 'PUT', body)
}

export function deleteApp(appId: string): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}`, 'DELETE')
}

export function getEnv(appId: string): Promise<EnvItem[]> {
  return request(`/apps/${enc(appId)}/env`)
}

export function setEnv(appId: string, body: Record<string, unknown>): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/env`, 'POST', body)
}

export function listSecrets(appId: string): Promise<EnvItem[]> {
  return request(`/apps/${enc(appId)}/secrets`)
}

export function setSecrets(appId: string, body: Record<string, string>): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/secrets`, 'POST', body)
}

export function listVersions(appId: string): Promise<VersionItem[]> {
  return request(`/apps/${enc(appId)}/versions`)
}

export function listDeployments(appId: string): Promise<DeployHistoryItem[]> {
  return request(`/apps/${enc(appId)}/deployments`)
}

export function getAppLog(appId: string): Promise<{ logs: string; lines: import('./types').AppLogLine[] }> {
  return request(`/apps/${enc(appId)}/logs`)
}

export function getAppUsage(appId: string): Promise<Record<string, unknown>> {
  return request(`/apps/${enc(appId)}/usage`)
}

export function pauseApp(appId: string): Promise<unknown> {
  return jsonRequest(`/apps/${enc(appId)}/actions/pause`, 'POST')
}

export function resumeApp(appId: string): Promise<unknown> {
  return jsonRequest(`/apps/${enc(appId)}/actions/resume`, 'POST')
}

export function restartApp(appId: string): Promise<unknown> {
  return jsonRequest(`/apps/${enc(appId)}/actions/restart`, 'POST')
}

export function rollbackApp(appId: string): Promise<DeployResult> {
  return jsonRequest(`/apps/${enc(appId)}/actions/rollback`, 'POST')
}

// ── 文件 ──────────────────────────────────────────────────────────

export function listFiles(appId: string): Promise<StorageObject[]> {
  return request(`/apps/${enc(appId)}/files`)
}

export function uploadFile(appId: string, file: File): Promise<{ id: string; url: string }> {
  const form = new FormData()
  form.append('file', file)
  return request(`/apps/${enc(appId)}/files`, { method: 'POST', body: form })
}

export function downloadFile(appId: string, fileId: string): Promise<{ blob: Blob; contentType: string }> {
  return requestBlob(`/apps/${enc(appId)}/files/${enc(fileId)}`)
}

export function deleteFile(appId: string, fileId: string): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/files/${enc(fileId)}`, 'DELETE')
}

// ── 数据库 ────────────────────────────────────────────────────────

export function listDatabases(appId: string): Promise<DatabaseInfo[]> {
  return request(`/apps/${enc(appId)}/databases`)
}

export function createDatabase(appId: string, name: string, isDefault = false): Promise<DatabaseInfo> {
  return jsonRequest(`/apps/${enc(appId)}/databases`, 'POST', { name, is_default: isDefault })
}

export function deleteDatabase(appId: string, db: string): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/databases/${enc(db)}`, 'DELETE')
}

export function getDatabase(appId: string, db: string): Promise<DatabaseDetail> {
  return request(`/apps/${enc(appId)}/databases/${enc(db)}`)
}

export function listTables(appId: string, db: string): Promise<TableItem[]> {
  return request(`/apps/${enc(appId)}/databases/${enc(db)}/tables`)
}

export function createTable(appId: string, db: string, body: {
  name: string
  fields: { name: string; type: 'TEXT' | 'INTEGER' | 'REAL' | 'BLOB'; not_null?: boolean }[]
}): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/databases/${enc(db)}/tables`, 'POST', body)
}

export function getTable(appId: string, db: string, table: string): Promise<TableDetail> {
  return request(`/apps/${enc(appId)}/databases/${enc(db)}/tables/${enc(table)}`)
}

export function renameTable(appId: string, db: string, table: string, name: string): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/databases/${enc(db)}/tables/${enc(table)}`, 'PUT', { name })
}

export function dropTable(appId: string, db: string, table: string): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/databases/${enc(db)}/tables/${enc(table)}`, 'DELETE')
}

export function createIndex(appId: string, db: string, table: string, body: {
  name: string
  fields: string[]
  unique?: boolean
}): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/databases/${enc(db)}/tables/${enc(table)}/indexes`, 'POST', body)
}

export function dropIndex(appId: string, db: string, table: string, index: string): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/databases/${enc(db)}/tables/${enc(table)}/indexes/${enc(index)}`, 'DELETE')
}

// ── CORS ──────────────────────────────────────────────────────────

export function listCors(appId: string): Promise<CorsDomain[]> {
  return request(`/apps/${enc(appId)}/cors`)
}

export function addCors(appId: string, domain: string): Promise<CorsDomain> {
  return jsonRequest(`/apps/${enc(appId)}/cors`, 'POST', { domain })
}

export function deleteCors(appId: string, id: string): Promise<{ status: string }> {
  return jsonRequest(`/apps/${enc(appId)}/cors/${enc(id)}`, 'DELETE')
}
