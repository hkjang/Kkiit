import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Stack, TextField, Typography } from '@mui/material'
import LogoutRoundedIcon from '@mui/icons-material/LogoutRounded'
import { api, dateTime } from '../api'
import { useApp } from '../App'
import type { AccountSession, AuthProvider, LinkedIdentity } from '../types'

// Password rotation and session review belong together: both are what someone
// reaches for when they think their account is exposed.
export function AccountSecurity() {
  const { notify } = useApp()
  const [form, setForm] = useState({ current: '', next: '', confirm: '' })
  const [busy, setBusy] = useState(false)
  const [sessions, setSessions] = useState<AccountSession[]>([])
  const [identities, setIdentities] = useState<LinkedIdentity[]>([])
  const [hasPassword, setHasPassword] = useState(true)
  const [providers, setProviders] = useState<AuthProvider[]>([])

  const loadSessions = useCallback(() => {
    api<{ items: AccountSession[] }>('/api/v1/me/sessions')
      .then((data) => setSessions(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '로그인 기록을 불러오지 못했습니다.', 'error'))
  }, [notify])
  const loadIdentities = useCallback(() => {
    api<{ items: LinkedIdentity[]; has_password: boolean }>('/api/v1/me/identities')
      .then((data) => { setIdentities(data.items); setHasPassword(data.has_password) })
      .catch(() => undefined)
    api<{ items: AuthProvider[] }>('/api/v1/auth/providers').then((data) => setProviders(data.items)).catch(() => undefined)
  }, [])
  useEffect(() => { loadSessions(); loadIdentities() }, [loadSessions, loadIdentities])

  const unlink = async (item: LinkedIdentity) => {
    if (!window.confirm(`${item.provider_name} 연결을 해제할까요?`)) return
    try { await api(`/api/v1/me/identities/${item.id}`, { method: 'DELETE' }); loadIdentities(); notify('연결을 해제했습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '연결을 해제하지 못했습니다.', 'error') }
  }

  const change = async (event: FormEvent) => {
    event.preventDefault()
    if (form.next !== form.confirm) { notify('새 비밀번호와 확인이 서로 다릅니다.', 'warning'); return }
    setBusy(true)
    try {
      const result = await api<{ revoked_sessions: number }>('/api/v1/me/password', { method: 'POST', body: JSON.stringify({ current_password: form.current, new_password: form.next }) })
      setForm({ current: '', next: '', confirm: '' })
      loadSessions()
      notify(result.revoked_sessions > 0 ? `비밀번호를 변경하고 다른 기기 ${result.revoked_sessions}곳의 로그인을 종료했습니다.` : '비밀번호를 변경했습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '비밀번호를 변경하지 못했습니다.', 'error') }
    finally { setBusy(false) }
  }

  const revokeOthers = async () => {
    if (!window.confirm('지금 사용 중인 기기를 제외한 모든 로그인을 종료할까요?')) return
    try {
      const result = await api<{ revoked_sessions: number }>('/api/v1/me/sessions', { method: 'DELETE' })
      loadSessions()
      notify(`다른 기기 ${result.revoked_sessions}곳의 로그인을 종료했습니다.`, 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '로그인을 종료하지 못했습니다.', 'error') }
  }

  return <>
    <Card component="form" onSubmit={change} sx={{ p: 3, mt: 3 }}>
      <Typography variant="h3">비밀번호 변경</Typography>
      <Typography color="text.secondary" sx={{ mt: .5 }}>변경하면 지금 사용 중인 기기를 제외한 모든 로그인이 종료됩니다.</Typography>
      <Stack spacing={2} sx={{ mt: 2, maxWidth: 420 }}>
        <TextField type="password" label="현재 비밀번호" value={form.current} onChange={(event) => setForm({ ...form, current: event.target.value })} autoComplete="current-password" helperText="소셜 로그인으로 가입해 비밀번호가 없다면 비워 두세요." />
        <TextField type="password" label="새 비밀번호" value={form.next} onChange={(event) => setForm({ ...form, next: event.target.value })} autoComplete="new-password" required helperText="12자 이상" />
        <TextField type="password" label="새 비밀번호 확인" value={form.confirm} onChange={(event) => setForm({ ...form, confirm: event.target.value })} autoComplete="new-password" required />
        <Button type="submit" variant="contained" disabled={busy || form.next.length < 12}>{busy ? '변경 중…' : '비밀번호 변경'}</Button>
      </Stack>
    </Card>

    <Card sx={{ p: 3, mt: 3 }}>
      <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'center' }} gap={1.5}>
        <Box><Typography variant="h3">로그인된 기기</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>모르는 접속이 있다면 즉시 종료하고 비밀번호를 바꾸세요.</Typography></Box>
        <Button color="error" startIcon={<LogoutRoundedIcon />} onClick={revokeOthers} disabled={sessions.length < 2}>다른 기기 로그아웃</Button>
      </Stack>
      <Stack spacing={1.5} sx={{ mt: 2 }}>
        {sessions.map((item) => <Stack key={item.id} direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={1}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center"><Typography fontWeight={650}>{item.ip ?? '주소 미확인'}</Typography>{item.current && <Chip size="small" color="primary" label="현재 기기" />}</Stack>
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', wordBreak: 'break-all' }}>{item.user_agent || '알 수 없는 클라이언트'}</Typography>
          </Box>
          <Typography variant="caption" color="text.secondary" flexShrink={0}>최근 활동 {dateTime(item.last_seen_at ?? item.created_at)}</Typography>
        </Stack>)}
      </Stack>
      {sessions.length === 0 && <Alert severity="info" sx={{ mt: 2 }}>활성 로그인이 없습니다.</Alert>}
    </Card>

    <Card sx={{ p: 3, mt: 3 }}>
      <Typography variant="h3">연결된 로그인 제공자</Typography>
      <Typography color="text.secondary" sx={{ mt: .5 }}>이미 계정이 있다면 이메일이 같더라도 자동으로 연결되지 않습니다. 여기에서 직접 연결해 주세요.</Typography>
      <Stack spacing={1.5} sx={{ mt: 2 }}>
        {identities.map((item) => <Stack key={item.id} direction="row" justifyContent="space-between" alignItems="center" gap={1}>
          <Box><Typography fontWeight={650}>{item.provider_name}</Typography><Typography variant="caption" color="text.secondary">최근 로그인 {dateTime(item.last_login_at)}</Typography></Box>
          <Button size="small" color="error" onClick={() => unlink(item)} disabled={!hasPassword && identities.length <= 1}>연결 해제</Button>
        </Stack>)}
      </Stack>
      {identities.length === 0 && <Alert severity="info" sx={{ mt: 2 }}>연결된 제공자가 없습니다.</Alert>}
      {!hasPassword && identities.length <= 1 && <Alert severity="warning" sx={{ mt: 2 }}>지금은 이 제공자가 유일한 로그인 수단입니다. 비밀번호를 먼저 설정하면 해제할 수 있습니다.</Alert>}
      {providers.filter((provider) => !identities.some((item) => item.provider_slug === provider.slug)).length > 0 && <Stack direction="row" gap={1} flexWrap="wrap" sx={{ mt: 2 }}>
        {providers.filter((provider) => !identities.some((item) => item.provider_slug === provider.slug)).map((provider) => (
          <Button key={provider.slug} variant="outlined" href={`/api/v1/auth/oauth/${provider.slug}/start?link=1`}>{provider.name} 연결</Button>
        ))}
      </Stack>}
    </Card>
  </>
}
