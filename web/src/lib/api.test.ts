import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { get } from 'svelte/store'
import { token } from './stores'
import * as api from './api'

function ok(body: unknown = []): Response {
  return { status: 200, ok: true, json: async () => body, headers: new Headers() } as Response
}

describe('api', () => {
  beforeEach(() => {
    sessionStorage.clear()
    token.set('test-token')
  })

  afterEach(() => vi.unstubAllGlobals())

  it('注入 Bearer Token、401 清 Token、解析 JSON 错误', async () => {
    const fetchMock = vi.fn().mockResolvedValue(ok([]))
    vi.stubGlobal('fetch', fetchMock)
    await api.listApps()
    const headers = fetchMock.mock.calls[0][1].headers as Headers
    expect(headers.get('Authorization')).toBe('Bearer test-token')

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 401, ok: false, json: async () => ({ error: 'x' }) }))
    await expect(api.listApps()).rejects.toThrow('Token 已失效')
    expect(get(token)).toBe('')

    token.set('test-token')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 500, ok: false, json: async () => ({ error: '炸了' }) }))
    await expect(api.listApps()).rejects.toThrow('炸了')
  })

  it('所有 JSON 接口使用 OpenAPI 路径', async () => {
    const fetchMock = vi.fn().mockResolvedValue(ok([]))
    vi.stubGlobal('fetch', fetchMock)

    const calls: [() => Promise<unknown>, string, string][] = [
      [() => api.systemHealth(), 'GET', '/api/system/health'],
      [() => api.getSystemUsage(), 'GET', '/api/system/usage'],
      [() => api.listAuditLogs(20), 'GET', '/api/system/audit?limit=20'],
      [() => api.getLogControl(), 'GET', '/api/system/log-control'],
      [() => api.setLogControl({ scope: 'control', enabled: true }), 'POST', '/api/system/log-control'],
      [() => api.rotateToken(), 'POST', '/api/system/token/rotate'],
      [() => api.listApps(), 'GET', '/api/apps'],
      [() => api.getApp('shop'), 'GET', '/api/apps/shop'],
      [() => api.updateApp('shop', {}), 'PUT', '/api/apps/shop'],
      [() => api.deleteApp('shop'), 'DELETE', '/api/apps/shop'],
      [() => api.getEnv('shop'), 'GET', '/api/apps/shop/env'],
      [() => api.setEnv('shop', {}), 'POST', '/api/apps/shop/env'],
      [() => api.listSecrets('shop'), 'GET', '/api/apps/shop/secrets'],
      [() => api.setSecrets('shop', {}), 'POST', '/api/apps/shop/secrets'],
      [() => api.listVersions('shop'), 'GET', '/api/apps/shop/versions'],
      [() => api.listDeployments('shop'), 'GET', '/api/apps/shop/deployments'],
      [() => api.getAppLog('shop'), 'GET', '/api/apps/shop/logs'],
      [() => api.getAppUsage('shop'), 'GET', '/api/apps/shop/usage'],
      [() => api.pauseApp('shop'), 'POST', '/api/apps/shop/actions/pause'],
      [() => api.resumeApp('shop'), 'POST', '/api/apps/shop/actions/resume'],
      [() => api.restartApp('shop'), 'POST', '/api/apps/shop/actions/restart'],
      [() => api.rollbackApp('shop'), 'POST', '/api/apps/shop/actions/rollback'],
      [() => api.listFiles('shop'), 'GET', '/api/apps/shop/files'],
      [() => api.deleteFile('shop', 'f1'), 'DELETE', '/api/apps/shop/files/f1'],
      [() => api.listDatabases('shop'), 'GET', '/api/apps/shop/databases'],
      [() => api.createDatabase('shop', 'analytics'), 'POST', '/api/apps/shop/databases'],
      [() => api.deleteDatabase('shop', 'analytics'), 'DELETE', '/api/apps/shop/databases/analytics'],
      [() => api.getDatabase('shop', 'main'), 'GET', '/api/apps/shop/databases/main'],
      [() => api.listTables('shop', 'main'), 'GET', '/api/apps/shop/databases/main/tables'],
      [() => api.createTable('shop', 'main', { name: 'users', fields: [{ name: 'name', type: 'TEXT' }] }), 'POST', '/api/apps/shop/databases/main/tables'],
      [() => api.getTable('shop', 'main', 'users'), 'GET', '/api/apps/shop/databases/main/tables/users'],
      [() => api.renameTable('shop', 'main', 'users', 'people'), 'PUT', '/api/apps/shop/databases/main/tables/users'],
      [() => api.dropTable('shop', 'main', 'users'), 'DELETE', '/api/apps/shop/databases/main/tables/users'],
      [() => api.createIndex('shop', 'main', 'users', { name: 'idx', fields: ['name'] }), 'POST', '/api/apps/shop/databases/main/tables/users/indexes'],
      [() => api.dropIndex('shop', 'main', 'users', 'idx'), 'DELETE', '/api/apps/shop/databases/main/tables/users/indexes/idx'],
      [() => api.listCors('shop'), 'GET', '/api/apps/shop/cors'],
      [() => api.addCors('shop', 'https://a.test'), 'POST', '/api/apps/shop/cors'],
      [() => api.deleteCors('shop', 'c1'), 'DELETE', '/api/apps/shop/cors/c1']
    ]

    for (const [call, method, path] of calls) {
      fetchMock.mockClear()
      await call()
      expect(fetchMock).toHaveBeenCalledTimes(1)
      const [url, init] = fetchMock.mock.calls[0]
      expect(url).toBe(path)
      expect(init.method ?? 'GET').toBe(method)
    }
  })

  it('multipart 部署和上传不手动设置 Content-Type', async () => {
    const fetchMock = vi.fn().mockResolvedValue(ok({ id: 'f1' }))
    vi.stubGlobal('fetch', fetchMock)

    const form = new FormData()
    form.append('app', 'shop')
    await api.deployApp(form)
    let init = fetchMock.mock.calls[0][1]
    expect(init.method).toBe('POST')
    expect(init.body).toBe(form)
    expect((init.headers as Headers).has('Content-Type')).toBe(false)

    fetchMock.mockClear()
    await api.uploadFile('shop', new File(['x'], 'a.txt'))
    init = fetchMock.mock.calls[0][1]
    expect((init.headers as Headers).has('Content-Type')).toBe(false)
  })

  it('文件下载走 blob', async () => {
    const blob = new Blob(['x'])
    const res = { status: 200, ok: true, blob: async () => blob, headers: new Headers({ 'Content-Type': 'text/plain' }) } as Response
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(res))
    const result = await api.downloadFile('shop', 'f1')
    expect(result.blob).toBe(blob)
    expect(result.contentType).toBe('text/plain')
  })

  it('verifyToken 成功写入 Token，失败清除', async () => {
    token.set('')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(ok([])))
    await api.verifyToken('good')
    expect(get(token)).toBe('good')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 401, ok: false, json: async () => ({}) }))
    await expect(api.verifyToken('bad')).rejects.toThrow()
    expect(get(token)).toBe('')
  })
})
