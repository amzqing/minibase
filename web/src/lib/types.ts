/** 应用摘要，对应 GET /api/apps 的返回项。 */
export interface App {
  id: string
  app_name: string
  status: string
  current_version: string
  port: number
  access_url?: string
  root_path?: string
}

/** 应用详情，对应 GET /api/apps/{app}。 */
export interface AppDetail extends App {
  id: string
  previous_version?: string
  restart_count?: number
  history?: AuditLog[]
}

/** 后端统一错误格式。 */
export interface ApiError {
  error: string
}

/** 全局提示消息。 */
export interface ToastMessage {
  type: 'success' | 'error'
  text: string
}

export interface DeployResult {
  app_name: string
  root_path?: string
  version?: string
  port?: number
  status?: string
  access_url?: string
  notice?: string
}

export interface AuditLog {
  id?: string
  app_id?: string
  event: string
  from_status?: string
  to_status?: string
  reason?: string
  created_at?: number
}

export interface SystemUsage {
  disk?: Record<string, unknown>
  memory?: Record<string, unknown>
  apps?: Record<string, unknown>
  ports?: Record<string, unknown>
  storage?: Record<string, unknown>
  [key: string]: unknown
}

export interface LogControlItem {
  scope: string
  app_name?: string
  enabled: boolean
  level?: string
  expires_at?: number
}

export type LogControlStatus = Record<string, LogControlItem>

export interface EnvItem {
  key: string
  value: string
  is_secret: boolean
}

export interface VersionItem {
  version: string
  created_at?: number
  mode?: string
  port?: number
  [key: string]: unknown
}

export interface DeployHistoryItem extends AuditLog {}

export interface StorageObject {
  id: string
  app_id?: string
  app_name: string
  file_name: string
  size: number
  content_type?: string
  created_at?: number
}

export interface DatabaseInfo {
  id?: string
  app_name?: string
  name: string
  path?: string
  is_default?: boolean
  created_at?: number
}

export interface DatabaseDetail {
  app_name?: string
  path?: string
  size_bytes?: number
  table_count?: number
}

export interface TableItem {
  name: string
  row_count?: number
}

export interface TableColumn {
  name: string
  type: string
  not_null?: boolean
  primary_key?: boolean
}

export interface TableIndex {
  name: string
  unique?: boolean
}

export interface TableDetail {
  name: string
  columns: TableColumn[]
  indexes: TableIndex[]
  row_count?: number
}

export interface CorsDomain {
  id: string
  domain: string
  created_at?: number
  app_name?: string
}

export interface TokenRotateResult {
  token: string
  notice?: string
}

/** 结构化日志行，对应 GET /api/apps/{id}/logs 的 lines 项。 */
export interface AppLogLine {
  time: string
  level: 'TRACE' | 'DEBUG' | 'INFO' | 'WARN' | 'ERROR' | 'FATAL' | 'PANIC' | string
  message: string
  raw: string
}
