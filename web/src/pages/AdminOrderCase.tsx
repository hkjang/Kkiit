import { useCallback, useEffect, useState } from 'react'
import { Link as RouterLink } from 'react-router-dom'
import { Alert, Box, Button, Card, Checkbox, Chip, CircularProgress, Divider, FormControlLabel, Stack, TextField, Typography } from '@mui/material'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import type { AdminOrderCase as OrderCase, OperatorNote } from '../types'

// Where the searchable order queue leads. Finding the order used to end at a
// card with a state written on it and nowhere to record what was found.
export function AdminOrderCase({ orderId, onClose }: { orderId: string; onClose: () => void }) {
  const { me, notify } = useApp()
  const [detail, setDetail] = useState<OrderCase | null>(null)
  const [error, setError] = useState('')
  const [notes, setNotes] = useState<OperatorNote[]>([])
  const [draft, setDraft] = useState('')
  const [pinned, setPinned] = useState(false)

  const loadNotes = useCallback(() => {
    api<{ items: OperatorNote[] }>(`/api/v1/admin/notes?subject_type=order&subject_id=${orderId}`)
      .then((data) => setNotes(data.items)).catch(() => undefined)
  }, [orderId])
  useEffect(() => {
    api<OrderCase>(`/api/v1/admin/orders/${orderId}`).then(setDetail).catch((cause) => setError(cause instanceof Error ? cause.message : '주문을 불러오지 못했습니다.'))
    loadNotes()
  }, [orderId, loadNotes])

  const addNote = async () => {
    const body = draft.trim()
    if (body.length < 2) return
    try {
      await api('/api/v1/admin/notes', { method: 'POST', body: JSON.stringify({ subject_type: 'order', subject_id: orderId, body, pinned }) })
      setDraft(''); setPinned(false); loadNotes(); notify('메모를 남겼습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '메모를 남기지 못했습니다.', 'error') }
  }

  if (error) return <Alert severity="error">{error}</Alert>
  if (!detail) return <Box sx={{ py: 6, display: 'grid', placeItems: 'center' }}><CircularProgress /></Box>

  const figures: [string, string][] = [
    ['계약 금액', money(detail.amount, detail.currency)],
    ['결제됨', money(detail.money.paid, detail.currency)],
    ['에스크로', money(detail.money.escrow, detail.currency)],
    ['환불됨', money(detail.money.refunded, detail.currency)],
  ]

  return <Box>
    <Stack direction="row" justifyContent="space-between" alignItems="center" gap={2} flexWrap="wrap" sx={{ mb: 2 }}>
      <Box>
        <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
          <Typography variant="h3">{detail.order_number}</Typography>
          <Chip size="small" label={detail.state} />
          {detail.overdue && <Chip size="small" color="warning" label={`납기 ${detail.overdue_days}일 초과`} />}
          {detail.disputes.length > 0 && <Chip size="small" color="error" label={`분쟁 ${detail.disputes.length}건`} />}
        </Stack>
        <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>
          {detail.talent.title} · {detail.buyer.display_name} → {detail.seller.display_name}
          {detail.organization ? ` · ${detail.organization.name} 명의` : ''}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {dateTime(detail.created_at)} 주문 · 납기 {dateTime(detail.due_at ?? undefined)}
          {detail.money.settlement_state ? ` · 정산 ${detail.money.settlement_state}` : ''}
        </Typography>
      </Box>
      <Stack direction="row" gap={1}>
        <Button component={RouterLink} to={`/orders/${detail.id}`}>주문 화면 열기</Button>
        <Button onClick={onClose}>닫기</Button>
      </Stack>
    </Stack>

    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr 1fr', md: 'repeat(4,1fr)' }, gap: 2 }}>
      {figures.map(([label, value]) => <Card key={label} sx={{ p: 2 }}>
        <Typography variant="caption" color="text.secondary">{label}</Typography>
        <Typography variant="h4" sx={{ mt: .3 }}>{value}</Typography>
      </Card>)}
    </Box>

    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' }, gap: 2, mt: 2 }}>
      <Card sx={{ p: 2.5 }}>
        <Typography variant="h4">요구사항</Typography>
        {Object.keys(detail.requirements ?? {}).length === 0 ? <Typography color="text.secondary" sx={{ mt: 1 }}>받은 요구사항이 없습니다.</Typography>
          : <Stack spacing={1} sx={{ mt: 1 }}>{Object.entries(detail.requirements).map(([label, value]) => <Box key={label}>
              <Typography variant="caption" fontWeight={750} color="text.secondary">{label}</Typography>
              <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>{String(value)}</Typography>
            </Box>)}</Stack>}
        <Typography variant="h4" sx={{ mt: 2.5 }}>납품물 {detail.deliveries.length}건</Typography>
        {detail.deliveries.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1 }}>납품된 결과물이 없습니다.</Typography>
          : <Stack spacing={.5} sx={{ mt: 1 }}>{detail.deliveries.map((item, index) => <Typography key={index} variant="body2">{dateTime(item.at)} · {item.type} · {item.description || '설명 없음'}</Typography>)}</Stack>}
      </Card>

      <Card sx={{ p: 2.5 }}>
        <Typography variant="h4">대화 {detail.messages.length}건</Typography>
        {detail.messages.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1 }}>주고받은 메시지가 없습니다.</Typography>
          : <Stack spacing={1} sx={{ mt: 1, maxHeight: 300, overflowY: 'auto' }}>{detail.messages.map((item, index) => <Box key={index} sx={{ borderLeft: '3px solid', borderColor: item.side === 'buyer' ? 'primary.main' : 'secondary.main', pl: 1.5 }}>
              <Typography variant="caption" color="text.secondary">{item.sender} ({item.side === 'buyer' ? '구매자' : '판매자'}) · {dateTime(item.at)}</Typography>
              <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>{item.body}</Typography>
            </Box>)}</Stack>}
      </Card>
    </Box>

    <Card sx={{ p: 2.5, mt: 2 }}>
      <Typography variant="h4">주문 이력</Typography>
      <Stack spacing={.3} sx={{ mt: 1, maxHeight: 220, overflowY: 'auto' }}>
        {detail.timeline.map((item, index) => <Typography key={index} variant="caption" color="text.secondary">
          {dateTime(item.at)} · {item.event}{item.to ? ` → ${item.to}` : ''}{item.note ? ` · ${item.note}` : ''}
        </Typography>)}
      </Stack>
    </Card>

    <Card sx={{ p: 2.5, mt: 2 }}>
      <Typography variant="h4">운영 메모</Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mt: .3 }}>이 주문을 살펴본 결과를 남겨 두면 다음 사람이 같은 조사를 반복하지 않습니다.</Typography>
      <Stack direction={{ xs: 'column', sm: 'row' }} gap={1} alignItems="flex-start" sx={{ mt: 1.5 }}>
        <TextField fullWidth size="small" multiline minRows={2} value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="예: 납품물은 요구사항대로. 구매자에게 확인 요청 안내함." />
        <Stack gap={.5}>
          <Button variant="contained" onClick={addNote} disabled={draft.trim().length < 2}>남기기</Button>
          <FormControlLabel control={<Checkbox size="small" checked={pinned} onChange={(event) => setPinned(event.target.checked)} />} label={<Typography variant="caption">상단 고정</Typography>} />
        </Stack>
      </Stack>
      {notes.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1.5 }}>남겨진 메모가 없습니다.</Typography> : <Stack divider={<Divider />} sx={{ mt: 1.5 }}>
        {notes.map((note) => <Box key={note.id} sx={{ py: 1.2 }}>
          <Stack direction="row" gap={1} alignItems="center">
            {note.pinned && <Chip size="small" color="primary" label="고정" />}
            <Typography variant="caption" fontWeight={750}>{note.author_name}</Typography>
            <Typography variant="caption" color="text.secondary">{dateTime(note.created_at)}</Typography>
            {note.author_id === me?.id && <Typography variant="caption" color="text.secondary">· 내 메모</Typography>}
          </Stack>
          <Typography sx={{ mt: .5, whiteSpace: 'pre-wrap' }}>{note.body}</Typography>
        </Box>)}
      </Stack>}
    </Card>
  </Box>
}
