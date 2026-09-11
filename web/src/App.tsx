import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { Alert, Box, CircularProgress, Snackbar } from '@mui/material'
import { api, onUnauthorized } from './api'
import type { Principal, VersionInfo } from './types'
import { LoginPage } from './pages/LoginPage'
import { MarketplacePage } from './pages/MarketplacePage'
import { OrdersPage } from './pages/OrdersPage'
import { TalentPage } from './pages/TalentPage'
import { SellerPage } from './pages/SellerPage'

// The marketplace is what an anonymous visitor lands on, and until now they
// downloaded the operator console with it. These three are behind a sign in or
// a permission, so they load when someone actually goes there.
const ProfilePage = lazy(() => import('./pages/ProfilePage').then((module) => ({ default: module.ProfilePage })))
const AdminPage = lazy(() => import('./pages/AdminPage').then((module) => ({ default: module.AdminPage })))
const OrderWorkspacePage = lazy(() => import('./pages/OrderWorkspacePage').then((module) => ({ default: module.OrderWorkspacePage })))
import { AppShell } from './components/AppShell'

type AppContextValue = {
  me: Principal | null
  version: VersionInfo | null
  features: Record<string, boolean>
  /** false until the session lookup has answered; who is signed in is unknown before that. */
  ready: boolean
  refreshMe: () => Promise<void>
  notify: (message: string, severity?: 'success' | 'error' | 'info' | 'warning') => void
}

const AppContext = createContext<AppContextValue | null>(null)
export const useApp = () => {
  const value = useContext(AppContext)
  if (!value) throw new Error('AppContext missing')
  return value
}

export function App() {
  const [me, setMe] = useState<Principal | null>(null)
  const [version, setVersion] = useState<VersionInfo | null>(null)
  const [features, setFeatures] = useState<Record<string, boolean>>({})
  const [ready, setReady] = useState(false)
  const [notice, setNotice] = useState<{ message: string; severity: 'success' | 'error' | 'info' | 'warning' } | null>(null)

  const refreshMe = useCallback(async () => {
    try { setMe(await api<Principal>('/api/v1/me')) } catch { setMe(null) }
  }, [])

  useEffect(() => {
    Promise.all([
      refreshMe(),
      api<VersionInfo>('/api/v1/version').then(setVersion).catch(() => undefined),
      // A deployment can switch features off, so the app asks what is available
      // instead of assuming every section exists.
      api<{ features: Record<string, boolean> }>('/api/v1/features').then((data) => setFeatures(data.features ?? {})).catch(() => undefined),
    ]).finally(() => setReady(true))
  }, [refreshMe])

  // Registered only while signed in: an anonymous visitor's session probe also
  // answers 401, and treating that as an expiry would bounce them off the
  // marketplace they came to browse.
  useEffect(() => {
    if (!me) { onUnauthorized(null); return }
    onUnauthorized(() => {
      setMe(null)
      setNotice({ message: '로그인 세션이 만료되었습니다. 다시 로그인해 주세요.', severity: 'warning' })
    })
    return () => onUnauthorized(null)
  }, [me])

  const value = useMemo<AppContextValue>(() => ({
    me, version, features, ready, refreshMe,
    notify: (message, severity = 'info') => setNotice({ message, severity }),
  }), [me, version, features, ready, refreshMe])

  // The marketplace used to wait behind the session lookup before it began
  // fetching anything, so an anonymous visitor spent two sequential round trips
  // looking at a spinner: one to be told they are not signed in, and only then
  // one for the listings they came for. Public pages render straight away and
  // fetch in parallel with the bootstrap; only the pages that genuinely depend
  // on who you are wait.

  const pending = <Box sx={{ minHeight: 420, display: 'grid', placeItems: 'center' }}><CircularProgress aria-label="화면 불러오는 중" /></Box>

  return <AppContext.Provider value={value}>
    <Suspense fallback={pending}>
    <Routes>
      <Route path="/login" element={!ready ? <Bootstrapping /> : me ? <Navigate to="/" replace /> : <LoginPage />} />
      <Route element={<AppShell />}>
        <Route index element={<MarketplacePage />} />
        <Route path="talents/:id" element={<TalentPage />} />
        <Route path="sellers/:id" element={<SellerPage />} />
        <Route path="orders" element={<RequireAuth><OrdersPage /></RequireAuth>} />
        <Route path="orders/:id" element={<RequireAuth><OrderWorkspacePage /></RequireAuth>} />
        <Route path="profile/*" element={<RequireAuth><ProfilePage /></RequireAuth>} />
      </Route>
      <Route path="admin/*" element={<RequirePermission permission="admin.access"><AdminPage /></RequirePermission>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
    </Suspense>
    <Snackbar open={Boolean(notice)} autoHideDuration={5000} onClose={() => setNotice(null)} anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}>
      {notice ? <Alert severity={notice.severity} variant="filled" onClose={() => setNotice(null)}>{notice.message}</Alert> : undefined}
    </Snackbar>
  </AppContext.Provider>
}

function Bootstrapping() {
  return <Box sx={{ minHeight: 420, display: 'grid', placeItems: 'center' }}><CircularProgress aria-label="서비스 불러오는 중" /></Box>
}

// Waiting here rather than redirecting matters: before the session lookup
// answers, "not signed in" and "not known yet" look the same, and sending a
// signed in user to the login page because their session had not loaded yet is
// the worse mistake.
function RequireAuth({ children }: { children: React.ReactNode }) {
  const { me, ready } = useApp()
  const location = useLocation()
  if (!ready) return <Bootstrapping />
  return me ? children : <Navigate to="/login" state={{ from: location.pathname }} replace />
}

function RequirePermission({ permission, children }: { permission: string; children: React.ReactNode }) {
  const { me, ready } = useApp()
  if (!ready) return <Bootstrapping />
  if (!me) return <Navigate to="/login" replace />
  return me.permissions.includes(permission) ? children : <Navigate to="/" replace />
}
