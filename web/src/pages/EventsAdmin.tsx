import { useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, MenuItem, Stack, Switch, Tab, Tabs, TextField, Typography } from '@mui/material'
import ReplayRoundedIcon from '@mui/icons-material/ReplayRounded'
import RefreshRoundedIcon from '@mui/icons-material/RefreshRounded'
import { api, dateTime } from '../api'
import { useApp } from '../App'
import { DeliveryList, eventLabel } from './NotificationSettings'
import type { DomainEvent, EventSummary, NotificationTemplate, WebhookDelivery } from '../types'

const statusColor = (status: string) => status === 'done' ? 'success' : status === 'failed' ? 'error' : status === 'retry' ? 'warning' : 'default'

// The dispatcher runs out of the request path, so this page is how an operator
// sees whether business events actually turned into notifications and calls.
export function EventsAdmin() {
  const { notify } = useApp()
  const [tab, setTab] = useState(0)
  const [summary, setSummary] = useState<EventSummary>({})
  const [events, setEvents] = useState<DomainEvent[]>([])
  const [status, setStatus] = useState('')
  const [deliveries, setDeliveries] = useState<WebhookDelivery[]>([])
  const [deliveryState, setDeliveryState] = useState('')
  const [templates, setTemplates] = useState<NotificationTemplate[]>([])
  const [editing, setEditing] = useState<NotificationTemplate | null>(null)

  const loadEvents = useCallback(() => {
    api<{ items: DomainEvent[]; summary: EventSummary }>(`/api/v1/admin/events${status ? `?status=${status}` : ''}`)
      .then((data) => { setEvents(data.items); setSummary(data.summary ?? {}) })
      .catch((cause) => notify(cause instanceof Error ? cause.message : '이벤트를 불러오지 못했습니다.', 'error'))
  }, [status, notify])
  const loadDeliveries = useCallback(() => {
    api<{ items: WebhookDelivery[] }>(`/api/v1/admin/events/deliveries${deliveryState ? `?state=${deliveryState}` : ''}`)
      .then((data) => setDeliveries(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '전달 이력을 불러오지 못했습니다.', 'error'))
  }, [deliveryState, notify])
  const loadTemplates = useCallback(() => {
    api<{ items: NotificationTemplate[] }>('/api/v1/admin/notifications/templates')
      .then((data) => setTemplates(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '템플릿을 불러오지 못했습니다.', 'error'))
  }, [notify])

  useEffect(() => { loadEvents() }, [loadEvents])
  useEffect(() => { if (tab === 1) loadDeliveries() }, [tab, loadDeliveries])
  useEffect(() => { if (tab === 2) loadTemplates() }, [tab, loadTemplates])

  const retryEvent = async (item: DomainEvent) => {
    try { await api(`/api/v1/admin/events/${item.id}/retry`, { method: 'POST' }); loadEvents(); notify('이벤트를 재처리 대기열에 넣었습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '재처리하지 못했습니다.', 'error') }
  }
  const retryDelivery = async (item: WebhookDelivery) => {
    try { await api(`/api/v1/admin/events/deliveries/${item.id}/retry`, { method: 'POST' }); loadDeliveries(); notify('재전송을 예약했습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '재전송하지 못했습니다.', 'error') }
  }
  const saveTemplate = async () => {
    if (!editing) return
    try {
      await api(`/api/v1/admin/notifications/templates/${editing.key}`, { method: 'PUT', body: JSON.stringify({ channel: editing.channel, locale: editing.locale, subject_template: editing.subject_template, body_template: editing.body_template, enabled: editing.enabled }) })
      setEditing(null); loadTemplates(); notify('알림 템플릿을 저장했습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') }
  }

  const cards: Array<[string, number, string]> = [
    ['대기 이벤트', summary.events_pending ?? 0, '아직 처리되지 않은 아웃박스'],
    ['실패 이벤트', summary.events_failed ?? 0, '재시도 한도를 넘긴 이벤트'],
    ['대기 전달', summary.deliveries_pending ?? 0, '재시도 예정 웹훅'],
    ['실패 전달', summary.deliveries_failed ?? 0, '수동 재전송 필요'],
  ]

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 3 }}>
      <Box><Typography variant="h2">이벤트·알림</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>거래 이벤트가 알림과 웹훅으로 전달된 결과를 확인하고 실패분을 재처리합니다.</Typography></Box>
      <Button startIcon={<RefreshRoundedIcon />} onClick={() => { loadEvents(); if (tab === 1) loadDeliveries(); if (tab === 2) loadTemplates() }}>새로고침</Button>
    </Stack>
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2,1fr)', xl: 'repeat(4,1fr)' }, gap: 2 }}>
      {cards.map(([label, value, caption]) => <Card key={label} sx={{ p: 3 }}>
        <Typography color="text.secondary" fontWeight={650}>{label}</Typography>
        <Typography sx={{ fontSize: '2.3rem', fontWeight: 800, mt: 1, color: value > 0 && label.startsWith('실패') ? 'error.main' : 'inherit' }}>{value}</Typography>
        <Typography variant="body2" color="text.secondary">{caption}</Typography>
      </Card>)}
    </Box>
    {(summary.events_failed ?? 0) === 0 && (summary.deliveries_failed ?? 0) === 0 && <Alert severity="success" sx={{ mt: 3 }}>모든 이벤트가 정상적으로 전달되었습니다.</Alert>}

    <Card sx={{ mt: 3 }}>
      <Tabs value={tab} onChange={(_, value) => setTab(value)} sx={{ px: 2, borderBottom: '1px solid', borderColor: 'divider' }}>
        <Tab label="이벤트 아웃박스" /><Tab label="웹훅 전달" /><Tab label="알림 템플릿" />
      </Tabs>
      <Box sx={{ p: { xs: 2, md: 3 } }}>
        {tab === 0 && <>
          <TextField select size="small" label="상태" value={status} onChange={(event) => setStatus(event.target.value)} sx={{ minWidth: 180, mb: 2 }}>
            <MenuItem value="">전체</MenuItem><MenuItem value="pending">대기</MenuItem><MenuItem value="retry">재시도</MenuItem><MenuItem value="processing">처리 중</MenuItem><MenuItem value="failed">실패</MenuItem><MenuItem value="done">완료</MenuItem>
          </TextField>
          <Stack spacing={1.2}>
            {events.map((item) => <Card key={item.id} variant="outlined" sx={{ p: 2, boxShadow: 'none' }}>
              <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={1.5}>
                <Box sx={{ minWidth: 0 }}>
                  <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
                    <Typography fontWeight={750}>{eventLabel(item.event_type)}</Typography>
                    <Chip size="small" color={statusColor(item.status)} label={item.status} />
                    <Chip size="small" variant="outlined" label={`${item.aggregate_type} · 시도 ${item.attempts}`} />
                  </Stack>
                  <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: .5 }}>{item.event_type} · 생성 {dateTime(item.created_at)}{item.processed_at ? ` · 처리 ${dateTime(item.processed_at)}` : ''}</Typography>
                  {item.last_error && <Typography variant="body2" color="error" sx={{ mt: .5, wordBreak: 'break-all' }}>{item.last_error}</Typography>}
                </Box>
                {item.status !== 'done' && <Button size="small" startIcon={<ReplayRoundedIcon />} onClick={() => void retryEvent(item)}>재처리</Button>}
              </Stack>
            </Card>)}
          </Stack>
          {events.length === 0 && <Alert severity="info">조건에 맞는 이벤트가 없습니다.</Alert>}
        </>}
        {tab === 1 && <>
          <TextField select size="small" label="상태" value={deliveryState} onChange={(event) => setDeliveryState(event.target.value)} sx={{ minWidth: 180, mb: 2 }}>
            <MenuItem value="">전체</MenuItem><MenuItem value="pending">대기</MenuItem><MenuItem value="retry">재시도</MenuItem><MenuItem value="sending">전송 중</MenuItem><MenuItem value="failed">실패</MenuItem><MenuItem value="delivered">성공</MenuItem>
          </TextField>
          <DeliveryList items={deliveries} onRetry={retryDelivery} />
        </>}
        {tab === 2 && <>
          <Typography color="text.secondary" sx={{ mb: 2 }}>본문에는 <code>{'{{order_number}}'}</code>, <code>{'{{talent_title}}'}</code>, <code>{'{{actor_name}}'}</code>처럼 이벤트 값을 넣을 수 있습니다. 비활성 템플릿은 알림을 만들지 않습니다.</Typography>
          <Stack spacing={1.2}>
            {templates.map((item) => <Card key={item.key} variant="outlined" sx={{ p: 2, boxShadow: 'none', opacity: item.enabled ? 1 : .6 }}>
              <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={1.5}>
                <Box sx={{ minWidth: 0 }}>
                  <Stack direction="row" gap={1} alignItems="center"><Typography fontWeight={750}>{eventLabel(item.key)}</Typography><Chip size="small" variant="outlined" label={item.key} />{!item.enabled && <Chip size="small" label="비활성" />}</Stack>
                  <Typography variant="body2" sx={{ mt: .5 }}>{item.subject_template}</Typography>
                  <Typography variant="body2" color="text.secondary">{item.body_template}</Typography>
                </Box>
                <Button size="small" onClick={() => setEditing({ ...item })}>수정</Button>
              </Stack>
            </Card>)}
          </Stack>
          {templates.length === 0 && <Alert severity="info">등록된 템플릿이 없습니다.</Alert>}
        </>}
      </Box>
    </Card>

    <Dialog open={Boolean(editing)} onClose={() => setEditing(null)} fullWidth maxWidth="sm">
      <DialogTitle>알림 템플릿 · {editing?.key}</DialogTitle>
      <DialogContent><Stack spacing={2} sx={{ mt: 1 }}>
        <TextField label="제목" value={editing?.subject_template ?? ''} onChange={(event) => setEditing(editing && { ...editing, subject_template: event.target.value })} />
        <TextField label="본문" multiline minRows={3} value={editing?.body_template ?? ''} onChange={(event) => setEditing(editing && { ...editing, body_template: event.target.value })} required />
        <FormControlLabel control={<Switch checked={editing?.enabled ?? false} onChange={(event) => setEditing(editing && { ...editing, enabled: event.target.checked })} />} label="이 이벤트로 알림 만들기" />
      </Stack></DialogContent>
      <DialogActions><Button onClick={() => setEditing(null)}>취소</Button><Button variant="contained" onClick={saveTemplate}>저장</Button></DialogActions>
    </Dialog>
  </>
}
