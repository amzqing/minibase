export type AppSection = 'overview' | 'database' | 'files' | 'env' | 'logs' | 'versions' | 'security'

export type Route =
  | { name: 'apps' }
  | { name: 'app'; appId: string; section: AppSection }
  | { name: 'system' }
  | { name: 'unknown' }

const SECTIONS: AppSection[] = ['overview', 'database', 'files', 'env', 'logs', 'versions', 'security']

function decode(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

/** 解析 hash，识别应用列表、应用二级页和系统设置。 */
export function parseHash(hash: string): Route {
  const clean = hash.replace(/^#\/?/, '').replace(/\/+$/, '')
  if (clean === '' || clean === 'apps') return { name: 'apps' }
  if (clean === 'system') return { name: 'system' }

  const parts = clean.split('/')
  if (parts[0] === 'apps' && parts[1]) {
    const appId = decode(parts[1])
    const section = parts[2]
    if (!section || section === 'overview') return { name: 'app', appId, section: 'overview' }
    if (SECTIONS.includes(section as AppSection)) {
      return { name: 'app', appId, section: section as AppSection }
    }
  }
  return { name: 'unknown' }
}

export const route = {
  subscribe(run: (r: Route) => void) {
    const update = () => run(parseHash(typeof window !== 'undefined' ? window.location.hash : ''))
    update()
    if (typeof window !== 'undefined') {
      window.addEventListener('hashchange', update)
      return () => window.removeEventListener('hashchange', update)
    }
    return () => {}
  }
}

export function navigate(path: string): void {
  if (typeof window === 'undefined') return
  window.location.hash = path.startsWith('#') ? path : `#${path}`
}

export function appPath(appId: string, section: AppSection = 'overview'): string {
  return `/apps/${encodeURIComponent(appId)}/${section}`
}
