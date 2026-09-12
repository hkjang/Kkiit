// Runs under `node --test` with Node's built-in type stripping (npm test), so
// it needs no test framework. A fake window stands in for the browser.
import { beforeEach, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { autoLoginProvider, beginSilentSso, clearSilentSsoState, markSignedOut, safeReturnTo, shouldAttemptSilentSso } from './silentSso.ts'

type FakeWindow = { sessionStorage: Storage; location: { pathname: string; search: string; assign: (url: string) => void } }

function memoryStorage(): Storage {
  const items = new Map<string, string>()
  return {
    get length() { return items.size },
    key: (index: number) => [...items.keys()][index] ?? null,
    getItem: (key: string) => items.get(key) ?? null,
    setItem: (key: string, value: string) => { items.set(key, value) },
    removeItem: (key: string) => { items.delete(key) },
    clear: () => { items.clear() },
  }
}

// A storage that throws on every access, the way a private mode or blocked
// site data does.
function blockedStorage(): Storage {
  const refuse = () => { throw new DOMException('The operation is insecure.', 'SecurityError') }
  return { length: 0, key: refuse, getItem: refuse, setItem: refuse, removeItem: refuse, clear: refuse }
}

let assigned: string[]
function install(storage: Storage, pathname = '/', search = '') {
  assigned = []
  const fake: FakeWindow = { sessionStorage: storage, location: { pathname, search, assign: (url) => { assigned.push(url) } } }
  ;(globalThis as any).window = fake
  return fake
}

const at = (pathname: string, search = '') => ({ pathname, search })
const keycloak = { slug: 'keycloak', name: 'Keycloak', preset: 'keycloak', login_url: '/api/v1/auth/oauth/keycloak/start', auto_login: true }

describe('shouldAttemptSilentSso', () => {
  beforeEach(() => { install(memoryStorage()) })

  it('tries once on an ordinary page', () => {
    assert.equal(shouldAttemptSilentSso(at('/')), true)
    assert.equal(shouldAttemptSilentSso(at('/orders/42', '?tab=deliveries')), true)
  })

  it('never starts from the login screen, the callback, or a non-page path', () => {
    for (const pathname of ['/login', '/api/v1/auth/oauth/keycloak/callback', '/api', '/mcp', '/mcp/sse', '/health']) {
      assert.equal(shouldAttemptSilentSso(at(pathname)), false, pathname)
    }
  })

  it('does not retry when the address carries a refusal marker', () => {
    assert.equal(shouldAttemptSilentSso(at('/', '?sso=none')), false)
    assert.equal(shouldAttemptSilentSso(at('/orders', '?sso=error')), false)
  })

  it('does not retry within the same tab session', () => {
    beginSilentSso(keycloak, '/orders')
    assert.equal(shouldAttemptSilentSso(at('/orders')), false)
    // A reload after a refusal must not try again; a fresh tab (new storage) may.
    install(memoryStorage())
    assert.equal(shouldAttemptSilentSso(at('/orders')), true)
  })

  it('does not sign a person back in after they signed out, until a session exists again', () => {
    markSignedOut()
    assert.equal(shouldAttemptSilentSso(at('/')), false)
    clearSilentSsoState()
    assert.equal(shouldAttemptSilentSso(at('/')), true)
  })

  it('fails closed when storage cannot be read', () => {
    install(blockedStorage())
    assert.equal(shouldAttemptSilentSso(at('/')), false)
    // Writing the flags must not throw either.
    assert.doesNotThrow(() => { markSignedOut(); clearSilentSsoState() })
  })
})

describe('beginSilentSso', () => {
  beforeEach(() => { install(memoryStorage(), '/orders/42', '?tab=deliveries') })

  it('navigates at the top level with prompt=none and the deep link', () => {
    beginSilentSso(keycloak, '/orders/42?tab=deliveries')
    assert.deepEqual(assigned, ['/api/v1/auth/oauth/keycloak/start?prompt=none&return_to=%2Forders%2F42%3Ftab%3Ddeliveries'])
  })

  it('refuses to carry an off-site return_to', () => {
    beginSilentSso(keycloak, '//evil.example.test/phish')
    assert.deepEqual(assigned, ['/api/v1/auth/oauth/keycloak/start?prompt=none&return_to=%2F'])
  })
})

describe('safeReturnTo', () => {
  it('accepts only same-origin paths', () => {
    assert.equal(safeReturnTo('/orders/42?tab=x'), '/orders/42?tab=x')
    for (const value of ['', 'orders', '//evil.example.test', '/\\evil.example.test', 'https://evil.example.test/']) {
      assert.equal(safeReturnTo(value), '/', value)
    }
  })
})

describe('autoLoginProvider', () => {
  it('picks only a provider the administrator turned auto_login on for', () => {
    const google = { slug: 'google', name: 'Google', preset: 'google', auto_login: false }
    assert.equal(autoLoginProvider([google]), undefined)
    assert.equal(autoLoginProvider([google, keycloak]), keycloak)
  })
})
