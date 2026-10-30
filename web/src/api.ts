import type { ApiErrorBody } from './types'

export class ApiError extends Error {
  constructor(public status: number, public body: ApiErrorBody) { super(body.message) }
}

let csrfToken = ''
let csrfRequest: Promise<string> | undefined

async function getCSRFToken(rejectedToken?: string): Promise<string> {
  if (rejectedToken !== undefined && csrfToken === rejectedToken) {
    csrfToken = ''
  }
  if (csrfToken) return csrfToken
  if (!csrfRequest) {
    csrfRequest = fetch('/api/csrf', { credentials: 'same-origin', cache: 'no-store' })
      .then(async response => {
        if (!response.ok) throw new Error('无法建立安全会话')
        const body = await response.json() as { token: string }
        if (typeof body.token !== 'string' || !body.token.trim()) throw new Error('无法建立安全会话')
        csrfToken = body.token
        return csrfToken
      })
      .finally(() => { csrfRequest = undefined })
  }
  return csrfRequest
}

export async function csrfHeaders() {
  return { 'X-CSRF-Token': await getCSRFToken() }
}

function requiresCSRF(method = 'GET') {
  return !['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase())
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (typeof init.body === 'string') headers.set('Content-Type', 'application/json')
  if (requiresCSRF(init.method)) headers.set('X-CSRF-Token', await getCSRFToken())
  let response = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (response.status === 403 && requiresCSRF(init.method)) {
    const rejected = await response.clone().json().catch(() => ({})) as ApiErrorBody
    if (rejected.code === 'csrf_rejected') {
      headers.set('X-CSRF-Token', await getCSRFToken(headers.get('X-CSRF-Token') ?? ''))
      response = await fetch(path, { ...init, headers, credentials: 'same-origin' })
    }
  }
  const body = await response.json().catch(() => ({}))
  if (!response.ok) {
    const errorBody = body as ApiErrorBody
    if (errorBody.code === 'session_locked' && typeof window !== 'undefined') {
      window.dispatchEvent(new CustomEvent('velin:session-locked'))
    }
    throw new ApiError(response.status, errorBody)
  }
  return body as T
}

export const json = (value: unknown) => JSON.stringify(value)
