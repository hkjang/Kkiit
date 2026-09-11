import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Dialog, DialogActions, DialogContent, DialogTitle, Divider, FormControlLabel, IconButton, MenuItem, Select, Stack, Switch, TextField, Tooltip, Typography } from '@mui/material'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import DeleteOutlineRoundedIcon from '@mui/icons-material/DeleteOutlineRounded'
import EditRoundedIcon from '@mui/icons-material/EditRounded'
import SendRoundedIcon from '@mui/icons-material/SendRounded'
import ReplayRoundedIcon from '@mui/icons-material/ReplayRounded'
import ContentCopyRoundedIcon from '@mui/icons-material/ContentCopyRounded'
import { api, dateTime } from '../api'
import { useApp } from '../App'
import type { NotificationPreference, Webhook, WebhookDelivery } from '../types'

export const eventLabels: Record<string, string> = {
  OrderCreated: '주문 접수', OrderPAYMENT_PENDING: '결제 대기', OrderPAID: '결제 완료', OrderREQUIREMENT_PENDING: '요구사항 대기',
  OrderREADY: '작업 준비', OrderIN_PROGRESS: '작업 시작', OrderDELIVERED: '납품 도착', OrderREVISION_REQUESTED: '수정 요청',
  OrderACCEPTED: '구매확정', OrderCOMPLETED: '거래 완료', OrderCANCEL_REQUESTED: '취소 요청', OrderCANCELLED: '주문 취소',
  OrderDISPUTED: '분쟁 접수', OrderREFUNDED: '환불 처리', MessageCreated: '새 메시지', ReviewCreated: '리뷰 등록',
  TalentPublished: '상품 공개', TalentRejected: '상품 반려', SettlementCreated: '정산 예정', RFQCreated: '견적 요청 등록', QuoteCreated: '새 견적 도착',
}
export const eventLabel = (key: string) => eventLabels[key] ?? key

export function NotificationSettings() {
  const { me } = useApp()
  return <>
    <Typography variant="h2">알림 설정</Typography>
    <Typography color="text.secondary" sx={{ mt: .5, mb: 3 }}>거래 이벤트를 어떤 알림으로 받을지 정하고, 외부 시스템으로 보낼 웹훅을 관리합니다.</Typography>
    <NotificationPreferences />
    {me?.permissions.includes('webhooks.manage.self') && <Box sx={{ mt: 4 }}><WebhookManager /></Box>}
  </>
}

function NotificationPreferences() {
  const { notify } = useApp()
  const [items, setItems] = useState<NotificationPreference[]>([])
  const [saving, setSaving] = useState(false)
  const [channels, setChannels] = useState<Record<string, boolean>>({})
  useEffect(() => {
    api<{ items: NotificationPreference[]; enabled_channels: Record<string, boolean> }>('/api/v1/me/notification-preferences')
      .then((data) => { setItems(data.items); setChannels(data.enabled_channels ?? {}) })
      .catch((cause) => notify(cause instanceof Error ? cause.message : '알림 설정을 불러오지 못했습니다.', 'error'))
  }, [notify])

  const toggle = (eventType: string, enabled: boolean) => setItems((previous) => previous.map((item) => item.event_type === eventType ? { ...item, enabled } : item))
  const save = async () => {
    setSaving(true)
    try { await api('/api/v1/me/notification-preferences', { method: 'PUT', body: JSON.stringify({ items }) }); notify('알림 설정을 저장했습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') }
    finally { setSaving(false) }
  }

  const disabledChannels = Object.entries(channels).filter(([name, value]) => name !== 'web' && !value).map(([name]) => name)
  return <Card sx={{ p: 3 }}>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'center' }} gap={1.5}>
      <Box><Typography variant="h3">받을 알림</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>끈 이벤트는 알림함에 쌓이지 않습니다. 웹훅 구독에는 영향을 주지 않습니다.</Typography></Box>
      <Button variant="contained" onClick={save} disabled={saving || items.length === 0}>{saving ? '저장 중…' : '설정 저장'}</Button>
    </Stack>
    {disabledChannels.length > 0 && <Alert severity="info" sx={{ mt: 2 }}>현재 관리자가 활성화한 채널은 웹 알림입니다. 비활성 채널: {disabledChannels.join(', ')}</Alert>}
    <Divider sx={{ my: 2 }} />
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(2,1fr)' }, columnGap: 3 }}>
      {items.map((item) => <FormControlLabel key={item.event_type} sx={{ justifyContent: 'space-between', ml: 0, py: .4 }} labelPlacement="start"
        control={<Switch checked={item.enabled} onChange={(event) => toggle(item.event_type, event.target.checked)} />}
        label={<Stack><Typography fontWeight={650}>{eventLabel(item.event_type)}</Typography><Typography variant="caption" color="text.secondary">{item.event_type}</Typography></Stack>} />)}
    </Box>
  </Card>
}

const emptyForm = { name: '', target_url: '', events: [] as string[], enabled: true }

function WebhookManager() {
  const { notify } = useApp()
  const [items, setItems] = useState<Webhook[]>([])
  const [catalog, setCatalog] = useState<string[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Webhook | null>(null)
  const [form, setForm] = useState(emptyForm)
  const [secret, setSecret] = useState('')
  const [rotate, setRotate] = useState(false)
  const [deliveries, setDeliveries] = useState<{ webhook: Webhook; items: WebhookDelivery[] } | null>(null)

  const load = useCallback(() => {
    api<{ items: Webhook[]; available_events: string[] }>('/api/v1/me/webhooks')
      .then((data) => { setItems(data.items); setCatalog(data.available_events) })
      .catch((cause) => notify(cause instanceof Error ? cause.message : '웹훅을 불러오지 못했습니다.', 'error'))
  }, [notify])
  useEffect(() => { load() }, [load])

  const startCreate = () => { setEditing(null); setForm(emptyForm); setRotate(false); setOpen(true) }
  const startEdit = (item: Webhook) => { setEditing(item); setForm({ name: item.name, target_url: item.target_url, events: item.events, enabled: item.enabled }); setRotate(false); setOpen(true) }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    try {
      const body = JSON.stringify({ ...form, rotate_secret: rotate })
      const result = editing
        ? await api<{ secret?: string }>(`/api/v1/me/webhooks/${editing.id}`, { method: 'PUT', body })
        : await api<{ secret?: string }>('/api/v1/me/webhooks', { method: 'POST', body })
      if (result.secret) setSecret(result.secret)
      setOpen(false); load()
      notify(editing ? '웹훅을 수정했습니다.' : '웹훅을 등록했습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') }
  }

  const remove = async (item: Webhook) => {
    if (!window.confirm(`${item.name} 웹훅을 삭제할까요? 전달 이력도 함께 사라집니다.`)) return
    try { await api(`/api/v1/me/webhooks/${item.id}`, { method: 'DELETE' }); load(); notify('웹훅을 삭제했습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '삭제하지 못했습니다.', 'error') }
  }

  const test = async (item: Webhook) => {
    try { await api(`/api/v1/me/webhooks/${item.id}/test`, { method: 'POST' }); notify('테스트 이벤트를 큐에 넣었습니다. 전달 이력에서 결과를 확인하세요.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '테스트에 실패했습니다.', 'error') }
  }

  const showDeliveries = async (item: Webhook) => {
    try { setDeliveries({ webhook: item, items: (await api<{ items: WebhookDelivery[] }>(`/api/v1/me/webhooks/${item.id}/deliveries`)).items }) }
    catch (cause) { notify(cause instanceof Error ? cause.message : '전달 이력을 불러오지 못했습니다.', 'error') }
  }

  const retry = async (delivery: WebhookDelivery) => {
    if (!deliveries) return
    try { await api(`/api/v1/me/webhooks/${deliveries.webhook.id}/deliveries/${delivery.id}/retry`, { method: 'POST' }); await showDeliveries(deliveries.webhook); load(); notify('재전송을 예약했습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '재전송하지 못했습니다.', 'error') }
  }

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 2 }}>
      <Box><Typography variant="h3">이벤트 웹훅</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>거래 이벤트를 내 시스템이나 AI Agent로 전달합니다. 모든 요청에 HMAC-SHA256 서명이 붙습니다.</Typography></Box>
      <Button variant="contained" startIcon={<AddRoundedIcon />} onClick={startCreate}>웹훅 추가</Button>
    </Stack>
    {secret && <Alert severity="warning" sx={{ mb: 2 }} onClose={() => setSecret('')} action={<IconButton aria-label="서명 키 복사" onClick={() => { void navigator.clipboard.writeText(secret) }}><ContentCopyRoundedIcon /></IconButton>}>
      <Typography fontWeight={750}>서명 키를 지금 보관하세요. 다시 표시되지 않습니다.</Typography>
      <Box component="code" sx={{ display: 'block', mt: 1, wordBreak: 'break-all' }}>{secret}</Box>
    </Alert>}
    <Stack spacing={2}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5, opacity: item.enabled ? 1 : .6 }}>
        <Stack direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center"><Typography variant="h4">{item.name}</Typography>{!item.enabled && <Chip size="small" label="비활성" />}{item.failed_deliveries > 0 && <Chip size="small" color="error" label={`실패 ${item.failed_deliveries}`} />}{item.pending_deliveries > 0 && <Chip size="small" color="warning" label={`대기 ${item.pending_deliveries}`} />}</Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5, wordBreak: 'break-all' }}>{item.target_url}</Typography>
            <Typography variant="caption" color="text.secondary">최근 성공 {dateTime(item.last_delivered_at)}</Typography>
            <Stack direction="row" gap={.6} flexWrap="wrap" sx={{ mt: 1 }}>{item.events.map((event) => <Chip key={event} size="small" variant="outlined" label={event === '*' ? '전체 이벤트' : eventLabel(event)} />)}</Stack>
          </Box>
          <Stack direction="row" alignItems="start" flexShrink={0}>
            <Button size="small" onClick={() => void showDeliveries(item)}>전달 이력</Button>
            <Tooltip title="테스트 전송"><span><IconButton aria-label={`${item.name} 테스트 전송`} onClick={() => void test(item)} disabled={!item.enabled}><SendRoundedIcon /></IconButton></span></Tooltip>
            <IconButton aria-label={`${item.name} 수정`} onClick={() => startEdit(item)}><EditRoundedIcon /></IconButton>
            <IconButton aria-label={`${item.name} 삭제`} color="error" onClick={() => void remove(item)}><DeleteOutlineRoundedIcon /></IconButton>
          </Stack>
        </Stack>
      </Card>)}
    </Stack>
    {items.length === 0 && <Card sx={{ p: 5, textAlign: 'center' }}><Typography color="text.secondary">등록한 웹훅이 없습니다.</Typography></Card>}

    <Dialog open={open} onClose={() => setOpen(false)} fullWidth maxWidth="sm"><Box component="form" onSubmit={submit}>
      <DialogTitle>{editing ? '웹훅 수정' : '웹훅 추가'}</DialogTitle>
      <DialogContent><Stack spacing={2} sx={{ mt: 1 }}>
        <TextField label="이름" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} required />
        <TextField label="전달 주소" value={form.target_url} onChange={(event) => setForm({ ...form, target_url: event.target.value })} required placeholder="https://internal.example.com/hooks/kkiit" helperText="POST 요청을 받아 2xx로 응답해야 합니다." />
        <Box>
          <Typography variant="body2" fontWeight={700} sx={{ mb: .5 }}>구독 이벤트</Typography>
          <Select multiple fullWidth value={form.events} onChange={(event) => setForm({ ...form, events: typeof event.target.value === 'string' ? event.target.value.split(',') : event.target.value })}
            renderValue={(selected) => (selected as string[]).map((item) => item === '*' ? '전체 이벤트' : eventLabel(item)).join(', ')}>
            <MenuItem value="*">전체 이벤트</MenuItem>
            {catalog.map((event) => <MenuItem key={event} value={event}>{eventLabel(event)} · {event}</MenuItem>)}
          </Select>
        </Box>
        <FormControlLabel control={<Switch checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />} label="활성화" />
        {editing && <FormControlLabel control={<Switch checked={rotate} onChange={(event) => setRotate(event.target.checked)} />} label="서명 키 재발급" />}
      </Stack></DialogContent>
      <DialogActions><Button onClick={() => setOpen(false)}>취소</Button><Button type="submit" variant="contained">저장</Button></DialogActions>
    </Box></Dialog>

    <Dialog open={Boolean(deliveries)} onClose={() => setDeliveries(null)} fullWidth maxWidth="md">
      <DialogTitle>전달 이력 · {deliveries?.webhook.name}</DialogTitle>
      <DialogContent><DeliveryList items={deliveries?.items ?? []} onRetry={retry} /></DialogContent>
      <DialogActions><Button onClick={() => setDeliveries(null)}>닫기</Button></DialogActions>
    </Dialog>
  </>
}

export const deliveryColor = (state: string) => state === 'delivered' ? 'success' : state === 'failed' ? 'error' : state === 'retry' ? 'warning' : 'default'

export function DeliveryList({ items, onRetry }: { items: WebhookDelivery[]; onRetry?: (item: WebhookDelivery) => void | Promise<void> }) {
  if (items.length === 0) return <Alert severity="info" sx={{ mt: 1 }}>아직 전달 기록이 없습니다.</Alert>
  return <Stack spacing={1.2} sx={{ mt: 1 }}>
    {items.map((item) => <Card key={item.id} variant="outlined" sx={{ p: 2, boxShadow: 'none' }}>
      <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={1.5}>
        <Box sx={{ minWidth: 0 }}>
          <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
            <Typography fontWeight={750}>{eventLabel(item.event_type)}</Typography>
            <Chip size="small" color={deliveryColor(item.state)} label={item.state} />
            {item.response_status ? <Chip size="small" variant="outlined" label={`HTTP ${item.response_status}`} /> : null}
            <Chip size="small" variant="outlined" label={`시도 ${item.attempts}`} />
          </Stack>
          {item.webhook_name && <Typography variant="body2" color="text.secondary">{item.owner_name} · {item.webhook_name} · {item.target_url}</Typography>}
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: .5 }}>생성 {dateTime(item.created_at)} · {item.state === 'delivered' ? `전달 ${dateTime(item.delivered_at)}` : `다음 시도 ${dateTime(item.next_attempt_at)}`}</Typography>
          {item.last_error && <Typography variant="body2" color="error" sx={{ mt: .5, wordBreak: 'break-all' }}>{item.last_error}</Typography>}
        </Box>
        {onRetry && item.state !== 'delivered' && <Button size="small" startIcon={<ReplayRoundedIcon />} onClick={() => void onRetry(item)}>재전송</Button>}
      </Stack>
    </Card>)}
  </Stack>
}
