export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, message: string, code = '') { super(message); this.status = status; this.code = code }
}

// A session that quietly expires is worse than one that ends: the header still
// shows the account, every menu is still there, and each action fails with its
// own red toast that says to sign in without offering any way to. One place
// notices the 401 and the application changes state once.
type UnauthorizedHandler = () => void
let unauthorizedHandler: UnauthorizedHandler | null = null
export function onUnauthorized(handler: UnauthorizedHandler | null) { unauthorizedHandler = handler }

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const isFormData = typeof FormData !== 'undefined' && options.body instanceof FormData
  const response = await fetch(path, {
    credentials: 'same-origin',
    ...options,
    headers: { ...(options.body && !isFormData ? { 'Content-Type': 'application/json' } : {}), ...options.headers },
  })
  if (!response.ok) {
    let message = `요청 처리에 실패했습니다. (${response.status})`
    let code = ''
    try { const payload = await response.json(); message = payload.error?.message ?? message; code = payload.error?.code ?? '' } catch { /* empty */ }
    // The handler is only registered while someone is signed in, so the 401 an
    // anonymous visitor gets from the session probe does not reach it.
    if (response.status === 401 && unauthorizedHandler) unauthorizedHandler()
    throw new ApiError(response.status, message, code)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const money = (amount: number, currency = 'KRW') => new Intl.NumberFormat('ko-KR', { style: 'currency', currency, maximumFractionDigits: 0 }).format(amount)
export const dateTime = (value?: string) => value ? new Intl.DateTimeFormat('ko-KR', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '—'
