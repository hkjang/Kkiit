import type { AuthProvider } from './types'

// Silent sign in: when the person already has a session at the identity
// provider, the app sends the browser there with prompt=none and comes back
// signed in without ever showing the login screen. prompt=none never renders
// anything — the provider either answers with a code or with login_required.
//
// The whole difficulty is not looping. A refusal that is retried on the next
// page load bounces the browser between the provider and the app for ever,
// and the person only sees the screen flicker. Three guards stop that:
//   1. one attempt per tab session, remembered in sessionStorage;
//   2. no attempt after the person signed out on purpose;
//   3. the callback lands a refusal on /login?sso=none, so the address itself
//      carries the marker even when browser storage was wiped in between.
//
// sessionStorage rather than localStorage: a new tab should try again, while
// a reload after a refusal should not.
const ATTEMPTED_KEY = 'kkiit.sso.silentAttempted'
const SIGNED_OUT_KEY = 'kkiit.sso.signedOut'

// Paths where a silent attempt must never start: the callback and login
// screen are where the loop would begin, and the rest are not pages.
const EXCLUDED_PATHS = ['/login', '/api', '/mcp', '/health']

function readFlag(key: string): boolean {
  try {
    return window.sessionStorage.getItem(key) === 'true'
  } catch {
    // Private modes and blocked site data throw. Reading that as "not yet
    // attempted" would loop, so it counts as attempted: fail closed.
    return true
  }
}

function writeFlag(key: string, value: boolean) {
  try {
    if (value) window.sessionStorage.setItem(key, 'true')
    else window.sessionStorage.removeItem(key)
  } catch {
    /* nothing to do; readFlag already fails closed */
  }
}

/** Records that the person signed out on purpose, which suppresses auto-login. */
export function markSignedOut() {
  writeFlag(SIGNED_OUT_KEY, true)
  writeFlag(ATTEMPTED_KEY, true)
}

/** Clears the suppression once a session exists again. */
export function clearSilentSsoState() {
  writeFlag(SIGNED_OUT_KEY, false)
  writeFlag(ATTEMPTED_KEY, false)
}

/** Only a same-origin path may be returned to; "//host" is a scheme-relative URL. */
export function safeReturnTo(value: string): string {
  return value.startsWith('/') && !value.startsWith('//') && !value.startsWith('/\\') ? value : '/'
}

/** Picks the provider the administrator turned silent sign in on for, if any. */
export function autoLoginProvider(providers: AuthProvider[]): AuthProvider | undefined {
  return providers.find((provider) => provider.auto_login === true)
}

/**
 * Decides whether this page load may try signing in without a login screen.
 * The location is the browser's, and the decision is made before anything
 * else is fetched so a deep link is still in the address bar.
 */
export function shouldAttemptSilentSso(location: { pathname: string; search: string } = window.location): boolean {
  if (EXCLUDED_PATHS.some((path) => location.pathname === path || location.pathname.startsWith(path + '/'))) return false
  // The callback appends this marker when the provider had no session, so a
  // refusal is remembered even if sessionStorage was cleared in between.
  const sso = new URLSearchParams(location.search).get('sso')
  if (sso === 'none' || sso === 'error') return false
  if (readFlag(SIGNED_OUT_KEY)) return false
  if (readFlag(ATTEMPTED_KEY)) return false
  return true
}

/** Sends the browser to the provider for a silent attempt, once per tab session. */
export function beginSilentSso(provider: AuthProvider, returnTo: string) {
  writeFlag(ATTEMPTED_KEY, true)
  const base = provider.login_url ?? `/api/v1/auth/oauth/${encodeURIComponent(provider.slug)}/start`
  // A top-level navigation rather than a hidden iframe: it works with
  // third-party cookies blocked and does not depend on the provider allowing
  // itself to be framed.
  window.location.assign(`${base}?prompt=none&return_to=${encodeURIComponent(safeReturnTo(returnTo))}`)
}
