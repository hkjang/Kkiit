import { FormEvent, useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Alert, Box, Button, Card, Chip, Container, Divider, Stack, TextField, Typography } from '@mui/material'
import ArrowForwardRoundedIcon from '@mui/icons-material/ArrowForwardRounded'
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined'
import HubOutlinedIcon from '@mui/icons-material/HubOutlined'
import OfflineBoltOutlinedIcon from '@mui/icons-material/OfflineBoltOutlined'
import { Brand } from '../components/Brand'
import { api, ApiError } from '../api'
import { useApp } from '../App'
import type { AuthProvider } from '../types'

export function LoginPage() {
  const { version, refreshMe } = useApp()
  const [providers, setProviders] = useState<AuthProvider[]>([])
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [registering, setRegistering] = useState(false)
  const [email, setEmail] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [mfaCode, setMfaCode] = useState('')
  const [mfaRequired, setMfaRequired] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()
  useEffect(() => { api<{ items: AuthProvider[] }>('/api/v1/auth/providers').then((data) => setProviders(data.items)).catch(() => undefined) }, [])
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setSubmitting(true); setError('')
    try { await api(registering ? '/api/v1/auth/register' : '/api/v1/auth/login', { method: 'POST', body: JSON.stringify(registering ? { username, email, display_name: displayName, password } : { username, password, ...(mfaCode ? { mfa_code: mfaCode } : {}) }) }); await refreshMe(); navigate((location.state as { from?: string } | null)?.from ?? '/') }
    catch (cause) { if(cause instanceof ApiError&&cause.code==='mfa_required'){setMfaRequired(true);setError('')}else setError(cause instanceof Error ? cause.message : '로그인하지 못했습니다.') }
    finally { setSubmitting(false) }
  }
  return <Box sx={{ minHeight: '100vh', display: 'grid', gridTemplateColumns: { xs: '1fr', lg: 'minmax(420px, .9fr) minmax(600px, 1.1fr)' }, bgcolor: '#081b31' }}>
    <Box sx={{ display: { xs: 'none', lg: 'flex' }, flexDirection: 'column', p: { lg: 7, xl: 10 }, color: 'white', position: 'relative', overflow: 'hidden' }}>
      <Box sx={{ position: 'absolute', width: 620, height: 620, borderRadius: '50%', background: 'radial-gradient(circle, rgba(54,111,255,.38), transparent 66%)', right: -250, top: -250 }} />
      <Brand inverse />
      <Box sx={{ my: 'auto', maxWidth: 620, position: 'relative' }}>
        <Chip label="Autonomous Service Marketplace" sx={{ mb: 3, bgcolor: 'rgba(169,243,223,.13)', color: '#b9f7e6', border: '1px solid rgba(169,243,223,.25)' }} />
        <Typography variant="h1" sx={{ color: 'white' }}>필요한 일을,<br /><Box component="span" sx={{ color: '#a9f3df' }}>더 적은 협의로.</Box></Typography>
        <Typography sx={{ mt: 3, color: '#b8c8dc', fontSize: '1.14rem', maxWidth: 540 }}>사람과 AI 전문가를 찾고, 요구사항부터 납품·정산까지 하나의 흐름으로 연결합니다.</Typography>
        <Stack direction="row" spacing={4} sx={{ mt: 6 }}>
          {[['안전한 거래', <ShieldOutlinedIcon />], ['AI + MCP', <HubOutlinedIcon />], ['오프라인 운영', <OfflineBoltOutlinedIcon />]].map(([label, icon]) => <Stack key={String(label)} direction="row" spacing={1} alignItems="center" sx={{ color: '#dce8f6' }}>{icon}<Typography fontWeight={650}>{label}</Typography></Stack>)}
        </Stack>
      </Box>
      <Typography variant="body2" sx={{ color: '#8398b2' }}>Kkiit v{version?.version ?? '—'} · {version?.commit && version.commit !== 'unknown' ? version.commit.slice(0, 8) : 'offline ready'}</Typography>
    </Box>
    <Box sx={{ bgcolor: 'background.default', display: 'grid', placeItems: 'center', p: { xs: 2.5, sm: 5 } }}>
      <Container maxWidth="sm">
        <Box sx={{ display: { lg: 'none' }, mb: 4 }}><Brand /></Box>
        <Card sx={{ p: { xs: 3, sm: 5 }, borderRadius: 4 }}>
          <Typography variant="h2">{registering ? 'Kkiit 시작하기' : '다시 만나서 반가워요'}</Typography>
          <Typography color="text.secondary" sx={{ mt: 1, mb: 3.5 }}>{registering ? '구매자 계정을 만들고 필요한 서비스를 찾아보세요.' : '계속하려면 Kkiit 계정으로 로그인하세요.'}</Typography>
          {error && <Alert severity="error" sx={{ mb: 2.5 }}>{error}</Alert>}
          <Box component="form" onSubmit={submit}>
            <Stack spacing={2.2}>
              <TextField label={registering ? '아이디' : '아이디 또는 이메일'} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" required fullWidth />
              {registering && <><TextField label="이메일" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required fullWidth /><TextField label="표시 이름" value={displayName} onChange={(e) => setDisplayName(e.target.value)} autoComplete="name" required fullWidth /></>}
              <TextField label="비밀번호" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={registering ? 'new-password' : 'current-password'} required fullWidth helperText={registering ? '12자 이상 입력하세요.' : undefined} />
              {!registering&&mfaRequired&&<TextField label="인증 앱 코드" value={mfaCode} onChange={(e)=>setMfaCode(e.target.value.replace(/\D/g,'').slice(0,6))} inputProps={{inputMode:'numeric',maxLength:6}} autoComplete="one-time-code" required autoFocus helperText="Authenticator 앱의 6자리 코드를 입력하세요."/>}
              <Button type="submit" variant="contained" size="large" disabled={submitting} endIcon={<ArrowForwardRoundedIcon />}>{submitting ? '확인 중…' : registering ? '계정 만들기' : '로그인'}</Button>
              <Button color="inherit" onClick={() => { setRegistering(!registering); setMfaRequired(false); setMfaCode(''); setError('') }}>{registering ? '이미 계정이 있어요' : '새 계정 만들기'}</Button>
            </Stack>
          </Box>
          {providers.length > 0 && <><Divider sx={{ my: 3 }}><Typography variant="body2" color="text.secondary">또는 간편 로그인</Typography></Divider><Stack spacing={1.2}>{providers.map((provider) => <Button key={provider.slug} component="a" href={provider.login_url} variant="outlined" color="inherit">{provider.name}로 계속하기</Button>)}</Stack></>}
          <Typography variant="body2" color="text.secondary" sx={{ mt: 3, textAlign: 'center' }}>소셜 로그인은 관리자가 활성화한 제공자만 표시됩니다.</Typography>
        </Card>
        <Typography variant="caption" color="text.secondary" sx={{ display: { lg: 'none' }, mt: 3, textAlign: 'center' }}>Kkiit v{version?.version ?? '—'} · API {version?.api_version ?? 'v1'}</Typography>
      </Container>
    </Box>
  </Box>
}
