import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Divider, FormControlLabel, MenuItem, Stack, Switch, TextField, Typography } from '@mui/material'
import SaveRoundedIcon from '@mui/icons-material/SaveRounded'
import RefreshRoundedIcon from '@mui/icons-material/RefreshRounded'
import SendRoundedIcon from '@mui/icons-material/SendRounded'
import { api, dateTime } from '../api'
import { useApp } from '../App'

const settingKey = 'mail'

// The event list is the whole contract: a mail is worth sending only when not
// getting it costs somebody money or keeps them refreshing a page.
const events = [
  ['notify_order_paid', '결제 완료', '판매자에게 — 돈이 에스크로에 들어왔고 작업 시작 차례입니다.'],
  ['notify_order_delivered', '납품 도착', '구매자에게 — 검수 기한이 흐르기 시작합니다. 놓치면 자동 구매확정됩니다.'],
  ['notify_revision_requested', '수정 요청', '판매자에게 — 주문이 다시 판매자 차례가 되었습니다.'],
  ['notify_quote', '견적 도착·선택', '구매자에게 견적이 도착했을 때, 판매자에게 견적이 선택됐을 때.'],
  ['notify_dispute', '분쟁 접수·처리', '접수는 상대방에게, 처리 결과는 양쪽에게. 분쟁 중에는 돈이 멈춥니다.'],
  ['notify_settlement_held', '정산 보류', '판매자에게 — 지급이 멈춘 것은 주문 화면에서 보이지 않습니다.'],
] as const

type Mail = {
  enabled: boolean; smtp_host: string; smtp_port: number; security: string; skip_tls_verify: boolean; username: string
  from_address: string; from_name: string; base_url: string; timeout_seconds: number
  notify_order_paid: boolean; notify_order_delivered: boolean; notify_revision_requested: boolean; notify_quote: boolean; notify_dispute: boolean; notify_settlement_held: boolean
}
type Setting = { key: string; value: Partial<Mail>; version: number; updated_at: string; secret_configured: boolean }
type Delivery = { id: string; event_type: string; recipient: string; subject: string; status: string; attempts: number; last_error: string; sent_at?: string; created_at: string }
type Summary = { total: number; status: Record<string, number> }

const defaults: Mail = {
  enabled: false, smtp_host: '', smtp_port: 25, security: 'auto', skip_tls_verify: false, username: '', from_address: '', from_name: 'Kkiit', base_url: '', timeout_seconds: 10,
  notify_order_paid: true, notify_order_delivered: true, notify_revision_requested: true, notify_quote: true, notify_dispute: true, notify_settlement_held: true,
}

const statusLabel: Record<string, [string, 'default' | 'success' | 'error' | 'warning' | 'info']> = {
  queued: ['대기', 'default'], sending: ['보내는 중', 'info'], retry: ['재시도 예정', 'warning'], sent: ['보냄', 'success'], failed: ['실패', 'error'],
}

// Relay settings are rarely right the first time, so the test button and the
// delivery log live on the same screen as the form: change, save, send one,
// read the reason.
export function MailAdmin() {
  const { notify, me } = useApp()
  const [setting, setSetting] = useState<Setting | null>(null)
  const [form, setForm] = useState<Mail>(defaults)
  const [password, setPassword] = useState('')
  const [recipient, setRecipient] = useState('')
  const [deliveries, setDeliveries] = useState<Delivery[]>([])
  const [summary, setSummary] = useState<Summary>({ total: 0, status: {} })
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)

  const load = useCallback(() => Promise.all([
    api<{ items: Setting[] }>('/api/v1/admin/settings').then((data) => {
      const item = data.items.find((entry) => entry.key === settingKey) ?? null
      setSetting(item)
      setForm({ ...defaults, ...(item?.value ?? {}) })
    }),
    api<{ items: Delivery[]; summary: Summary }>('/api/v1/admin/mail/deliveries?limit=100').then((data) => { setDeliveries(data.items); setSummary(data.summary) }),
  ]).catch((cause) => notify(cause instanceof Error ? cause.message : '메일 설정을 불러오지 못했습니다.', 'error')), [notify])
  useEffect(() => { load() }, [load])

  const update = <K extends keyof Mail>(key: K, value: Mail[K]) => setForm((current) => ({ ...current, [key]: value }))

  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (!setting) return
    setSaving(true)
    try {
      const body: Record<string, unknown> = { value: form, version: setting.version }
      // The password travels only when it is being changed; the API never
      // returns it, so an untouched field must not overwrite it with nothing.
      if (password) body.secret = password
      await api(`/api/v1/admin/settings/${settingKey}`, { method: 'PUT', body: JSON.stringify(body) })
      setPassword('')
      notify(form.enabled ? '메일 설정을 저장했습니다. 디스패처는 30초 안에 새 설정을 읽습니다.' : '메일 설정을 저장했습니다. 메일은 나가지 않습니다.', 'success')
      await load()
    } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') } finally { setSaving(false) }
  }

  const sendTest = async () => {
    setTesting(true); setTestResult(null)
    try {
      const result = await api<{ recipient: string; elapsed_ms: number }>('/api/v1/admin/mail/test', { method: 'POST', body: JSON.stringify({ recipient }) })
      setTestResult({ ok: true, message: `${result.recipient} 으로 보냈습니다 (${result.elapsed_ms}ms). 받은 편지함을 확인하세요.` })
    } catch (cause) {
      setTestResult({ ok: false, message: cause instanceof Error ? cause.message : '보내지 못했습니다.' })
    } finally { setTesting(false); await load() }
  }

  const fromHint = form.smtp_host && !form.from_address ? `비우면 kkiit@${form.smtp_host} 로 보냅니다.` : '릴레이가 받아 주는 보내는 사람 주소여야 합니다.'

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 3 }}>
      <Box><Typography variant="h2">메일 알림</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>사람이 실제로 기다리는 일만 사내 SMTP 릴레이로 보냅니다. 기본은 꺼짐이며, 메일은 요청을 막지 않고 배경에서 나갑니다.</Typography></Box>
      <Chip label={form.enabled ? '메일 켜짐' : '메일 꺼짐'} color={form.enabled ? 'success' : 'default'} variant="outlined" />
    </Stack>

    <Card component="form" onSubmit={save} sx={{ p: 3, mb: 3 }}>
      <Stack spacing={2.5}>
        <FormControlLabel control={<Switch checked={form.enabled} onChange={(event) => update('enabled', event.target.checked)} />} label="메일 알림 사용" />
        <Stack direction={{ xs: 'column', md: 'row' }} gap={2}>
          <TextField fullWidth label="SMTP 릴레이 주소" placeholder="relay.corp.example" value={form.smtp_host} onChange={(event) => update('smtp_host', event.target.value)} required={form.enabled} helperText="사내 릴레이는 대개 포트 25, 인증 없음, TLS 없음입니다. postra 를 가리키면 알림이 사내를 벗어나지 않습니다." />
          <TextField label="포트" type="number" value={form.smtp_port} onChange={(event) => update('smtp_port', Number(event.target.value))} sx={{ minWidth: 120 }} slotProps={{ htmlInput: { min: 1, max: 65535 } }} />
          <TextField select label="보안" value={form.security} onChange={(event) => update('security', event.target.value)} sx={{ minWidth: 160 }} helperText="auto 는 서버가 알리는 대로 맞춥니다.">
            <MenuItem value="auto">auto</MenuItem><MenuItem value="none">none</MenuItem><MenuItem value="starttls">starttls</MenuItem><MenuItem value="tls">tls (465)</MenuItem>
          </TextField>
        </Stack>
        <Stack direction={{ xs: 'column', md: 'row' }} gap={2}>
          <TextField fullWidth label="사용자 이름 (선택)" value={form.username} onChange={(event) => update('username', event.target.value)} helperText="인증이 없는 릴레이면 비워 둡니다." autoComplete="off" />
          <TextField fullWidth type="password" label={setting?.secret_configured ? '비밀번호 (변경할 때만 입력)' : '비밀번호 (선택)'} value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password"
            helperText={setting?.secret_configured ? '설정됨 · 원문은 다시 표시되지 않습니다.' : '암호화되어 저장되며 설정 API 는 되돌려주지 않습니다.'} />
        </Stack>
        <FormControlLabel control={<Switch checked={form.skip_tls_verify} onChange={(event) => update('skip_tls_verify', event.target.checked)} />} label="TLS 인증서 검증 건너뛰기 (사내 사설 인증서일 때만)" />
        <Divider />
        <Stack direction={{ xs: 'column', md: 'row' }} gap={2}>
          <TextField fullWidth label="보내는 사람 주소" placeholder="kkiit@corp.example" value={form.from_address} onChange={(event) => update('from_address', event.target.value)} helperText={fromHint} />
          <TextField fullWidth label="보내는 사람 이름" value={form.from_name} onChange={(event) => update('from_name', event.target.value)} helperText="제목 앞의 [이름] 과 From 헤더에 씁니다." />
        </Stack>
        <Stack direction={{ xs: 'column', md: 'row' }} gap={2}>
          <TextField fullWidth type="url" label="이 앱의 주소 (메일 속 링크)" placeholder="https://kkiit.corp.example" value={form.base_url} onChange={(event) => update('base_url', event.target.value)} helperText="비우면 인증 연동의 외부 서비스 주소(auth.oauth.callback_base_url)를 씁니다. 둘 다 없으면 링크 없이 보냅니다." />
          <TextField label="제한 시간 (초)" type="number" value={form.timeout_seconds} onChange={(event) => update('timeout_seconds', Number(event.target.value))} sx={{ minWidth: 140 }} slotProps={{ htmlInput: { min: 1, max: 120 } }} />
        </Stack>
        <Divider />
        <Box>
          <Typography variant="h4" sx={{ mb: .5 }}>보낼 이벤트</Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>자기가 한 일은 자기에게 보내지 않고, 한 사람에게 같은 때 생긴 알림은 한 통으로 묶습니다. 받는 사람이 개인화 &gt; 알림 설정에서 끈 이벤트는 메일로도 가지 않습니다.</Typography>
          <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(2,1fr)' }, columnGap: 3 }}>
            {events.map(([key, label, description]) => <FormControlLabel key={key} sx={{ alignItems: 'flex-start', ml: 0, py: .6 }}
              control={<Switch checked={form[key]} onChange={(event) => update(key, event.target.checked)} />}
              label={<Stack><Typography fontWeight={650}>{label}</Typography><Typography variant="caption" color="text.secondary">{description}</Typography></Stack>} />)}
          </Box>
        </Box>
        <Stack direction="row" gap={1} alignItems="center">
          <Button type="submit" variant="contained" startIcon={<SaveRoundedIcon />} disabled={saving || !setting}>저장</Button>
          {setting && <Typography variant="body2" color="text.secondary">v{setting.version} · {dateTime(setting.updated_at)}</Typography>}
        </Stack>
      </Stack>
    </Card>

    <Card sx={{ p: 3, mb: 3 }}>
      <Typography variant="h3">시험 발송</Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mt: .5, mb: 2 }}>저장한 설정으로 실제 한 통을 보내고 결과를 여기에 보여 줍니다. 저장하지 않은 변경은 반영되지 않습니다.</Typography>
      <Stack direction={{ xs: 'column', md: 'row' }} gap={2} alignItems={{ md: 'center' }}>
        <TextField fullWidth type="email" label="받는 사람" placeholder={me?.email || 'someone@corp.example'} value={recipient} onChange={(event) => setRecipient(event.target.value)} helperText="비우면 내 계정 주소로 보냅니다." />
        <Button variant="outlined" startIcon={<SendRoundedIcon />} onClick={sendTest} disabled={testing || !form.enabled} sx={{ whiteSpace: 'nowrap', minHeight: 44 }}>{testing ? '보내는 중…' : '시험 발송'}</Button>
      </Stack>
      {!form.enabled && <Alert severity="info" sx={{ mt: 2 }}>메일 알림을 켜고 저장한 뒤 시험 발송할 수 있습니다.</Alert>}
      {testResult && <Alert severity={testResult.ok ? 'success' : 'error'} sx={{ mt: 2 }}>{testResult.message}</Alert>}
    </Card>

    <Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 1.5 }}>
      <Box><Typography variant="h3">발송 기록</Typography><Typography variant="body2" color="text.secondary">시도마다 남습니다 — 언제, 어떤 이벤트로, 누구에게, 무슨 제목으로, 되었는지. 본문은 기록하지 않습니다.</Typography></Box>
      <Stack direction="row" gap={1} alignItems="center">
        {Object.entries(summary.status).map(([status, count]) => <Chip key={status} size="small" variant="outlined" color={statusLabel[status]?.[1] ?? 'default'} label={`${statusLabel[status]?.[0] ?? status} ${count}`} />)}
        <Button size="small" startIcon={<RefreshRoundedIcon />} onClick={() => load()}>새로고침</Button>
      </Stack>
    </Stack>
    {deliveries.length === 0 && <Card sx={{ p: 4, textAlign: 'center' }}><Typography color="text.secondary">{form.enabled ? '아직 나간 메일이 없습니다.' : '메일 알림을 켜면 나간 메일이 여기에 남습니다.'}</Typography></Card>}
    <Stack spacing={1.5}>
      {deliveries.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'center' }} gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap"><Typography variant="h4">{item.subject}</Typography><Chip size="small" label={item.event_type} variant="outlined" /><Chip size="small" color={statusLabel[item.status]?.[1] ?? 'default'} label={statusLabel[item.status]?.[0] ?? item.status} /></Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>{item.recipient} · {dateTime(item.sent_at ?? item.created_at)} · {item.attempts}회 시도</Typography>
            {item.last_error && <Typography variant="body2" color="error.main" sx={{ mt: .5, wordBreak: 'break-all' }}>{item.last_error}</Typography>}
          </Box>
        </Stack>
      </Card>)}
    </Stack>
  </>
}
