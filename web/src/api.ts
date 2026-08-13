export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, message: string, code = '') { super(message); this.status = status; this.code = code }
}

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
    throw new ApiError(response.status, message, code)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const money = (amount: number, currency = 'KRW') => new Intl.NumberFormat('ko-KR', { style: 'currency', currency, maximumFractionDigits: 0 }).format(amount)
export const dateTime = (value?: string) => value ? new Intl.DateTimeFormat('ko-KR', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '—'
