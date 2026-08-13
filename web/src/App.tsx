import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { Alert, Box, CircularProgress, Snackbar } from '@mui/material'
import { api } from './api'
import type { Principal, VersionInfo } from './types'
import { LoginPage } from './pages/LoginPage'
import { MarketplacePage } from './pages/MarketplacePage'
import { OrdersPage } from './pages/OrdersPage'
import { ProfilePage } from './pages/ProfilePage'
import { AdminPage } from './pages/AdminPage'
import { TalentPage } from './pages/TalentPage'
import { OrderWorkspacePage } from './pages/OrderWorkspacePage'
import { AppShell } from './components/AppShell'

type AppContextValue = {
  me: Principal | null
  version: VersionInfo | null
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
  const [loading, setLoading] = useState(true)
  const [notice, setNotice] = useState<{ message: string; severity: 'success' | 'error' | 'info' | 'warning' } | null>(null)

  const refreshMe = useCallback(async () => {
    try { setMe(await api<Principal>('/api/v1/me')) } catch { setMe(null) }
  }, [])

  useEffect(() => {
    Promise.all([refreshMe(), api<VersionInfo>('/api/v1/version').then(setVersion).catch(() => undefined)]).finally(() => setLoading(false))
  }, [refreshMe])

  const value = useMemo<AppContextValue>(() => ({
    me, version, refreshMe,
    notify: (message, severity = 'info') => setNotice({ message, severity }),
  }), [me, version, refreshMe])

  if (loading) return <Box sx={{ minHeight: '100vh', display: 'grid', placeItems: 'center' }}><CircularProgress aria-label="서비스 불러오는 중" /></Box>

  return <AppContext.Provider value={value}>
    <Routes>
      <Route path="/login" element={me ? <Navigate to="/" replace /> : <LoginPage />} />
      <Route element={<AppShell />}>
        <Route index element={<MarketplacePage />} />
        <Route path="talents/:id" element={<TalentPage />} />
        <Route path="orders" element={<RequireAuth><OrdersPage /></RequireAuth>} />
        <Route path="orders/:id" element={<RequireAuth><OrderWorkspacePage /></RequireAuth>} />
        <Route path="profile/*" element={<RequireAuth><ProfilePage /></RequireAuth>} />
      </Route>
      <Route path="admin/*" element={<RequirePermission permission="admin.access"><AdminPage /></RequirePermission>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
    <Snackbar open={Boolean(notice)} autoHideDuration={5000} onClose={() => setNotice(null)} anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}>
      {notice ? <Alert severity={notice.severity} variant="filled" onClose={() => setNotice(null)}>{notice.message}</Alert> : undefined}
    </Snackbar>
  </AppContext.Provider>
}

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { me } = useApp()
  const location = useLocation()
  return me ? children : <Navigate to="/login" state={{ from: location.pathname }} replace />
}

function RequirePermission({ permission, children }: { permission: string; children: React.ReactNode }) {
  const { me } = useApp()
  if (!me) return <Navigate to="/login" replace />
  return me.permissions.includes(permission) ? children : <Navigate to="/" replace />
}
