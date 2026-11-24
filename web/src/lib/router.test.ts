import { describe, expect, it } from 'vitest'
import { parseHash } from './router'

describe('parseHash', () => {
  it('应用列表与系统设置', () => {
    expect(parseHash('')).toEqual({ name: 'apps' })
    expect(parseHash('#/apps')).toEqual({ name: 'apps' })
    expect(parseHash('#/system')).toEqual({ name: 'system' })
  })

  it('应用默认进入概览', () => {
    expect(parseHash('#/apps/shop')).toEqual({ name: 'app', appId: 'shop', section: 'overview' })
    expect(parseHash('#/apps/shop/overview')).toEqual({ name: 'app', appId: 'shop', section: 'overview' })
  })

  it('识别所有应用二级页', () => {
    for (const section of ['database', 'files', 'env', 'logs', 'versions', 'security'] as const) {
      expect(parseHash(`#/apps/shop/${section}`)).toEqual({ name: 'app', appId: 'shop', section })
    }
  })

  it('支持带编码的应用 id', () => {
    expect(parseHash('#/apps/%E5%95%86%E5%9F%8E/database')).toEqual({ name: 'app', appId: '商城', section: 'database' })
  })

  it('未知路由归一到 unknown', () => {
    expect(parseHash('#/unknown')).toEqual({ name: 'unknown' })
    expect(parseHash('#/apps/shop/nope')).toEqual({ name: 'unknown' })
  })
})
