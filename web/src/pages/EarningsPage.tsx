import { useCallback, useEffect, useState } from 'react'
import { Link as RouterLink } from 'react-router-dom'
import { Alert, Box, Button, Card, Chip, MenuItem, Stack, TextField, Typography } from '@mui/material'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import type { SellerSettlement, SellerEarnings } from '../types'

const stateColor = (state: string) => state === 'completed' ? 'success' : state === 'hold' ? 'error' : state === 'cancelled' ? 'default' : 'primary'

// Sellers previously had no financial view at all. This page answers the three
// questions they actually have: what is coming, what is stuck, what arrived.
export function EarningsPage() {
  const { notify } = useApp()
  const [items, setItems] = useState<SellerSettlement[]>([])
  const [summary, setSummary] = useState<SellerEarnings>({})
  const [state, setState] = useState('')
  const [cursor, setCursor] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const fetchPage = useCallback((next?: string) => {
    setBusy(true)
    api<{ items: SellerSettlement[]; next_cursor: string | null; summary: SellerEarnings }>(`/api/v1/me/settlements?limit=25${state ? `&state=${state}` : ''}${next ? `&cursor=${encodeURIComponent(next)}` : ''}`)
      .then((data) => { setItems((current) => next ? [...current, ...data.items] : data.items); setCursor(data.next_cursor); setSummary(data.summary ?? {}) })
      .catch((cause) => notify(cause instanceof Error ? cause.message : '정산 내역을 불러오지 못했습니다.', 'error'))
      .finally(() => setBusy(false))
  }, [state, notify])
  useEffect(() => { fetchPage() }, [fetchPage])

  const cards: Array<[string, number, string]> = [
    ['지급 예정', summary.upcoming_amount ?? 0, summary.next_payout_at ? `가장 이른 지급 ${dateTime(summary.next_payout_at)}` : '예정된 정산이 없습니다'],
    ['보류 중', summary.held_amount ?? 0, '운영 검토가 끝나면 지급됩니다'],
    ['누적 지급', summary.paid_amount ?? 0, `누적 거래 ${money(summary.lifetime_gross ?? 0)}`],
  ]

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 3 }}>
      <Box><Typography variant="h2">수익·정산</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>구매확정된 주문의 정산 상태와 지급 예정일을 확인합니다.</Typography></Box>
      <TextField select size="small" label="상태" value={state} onChange={(event) => setState(event.target.value)} sx={{ minWidth: 160 }}>
        <MenuItem value="">전체</MenuItem><MenuItem value="scheduled">지급 예정</MenuItem><MenuItem value="confirmed">지급 대기</MenuItem>
        <MenuItem value="hold">보류</MenuItem><MenuItem value="completed">지급 완료</MenuItem><MenuItem value="cancelled">취소</MenuItem>
      </TextField>
    </Stack>

    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(3,1fr)' }, gap: 2, mb: 3 }}>
      {cards.map(([label, value, caption]) => <Card key={label} sx={{ p: 3 }}>
        <Typography color="text.secondary" fontWeight={650}>{label}</Typography>
        <Typography sx={{ fontSize: '1.9rem', fontWeight: 800, mt: .5, color: label === '보류 중' && value > 0 ? 'error.main' : 'inherit' }}>{money(value)}</Typography>
        <Typography variant="body2" color="text.secondary">{caption}</Typography>
      </Card>)}
    </Box>
    {(summary.lifetime_fee ?? 0) > 0 && <Alert severity="info" sx={{ mb: 3 }}>누적 거래 {money(summary.lifetime_gross ?? 0)} 중 플랫폼 수수료 {money(summary.lifetime_fee ?? 0)}가 차감되었습니다.</Alert>}

    <Stack spacing={1.5}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Typography variant="h4" component={RouterLink} to={`/orders/${item.order_id}`} sx={{ textDecoration: 'none', color: 'inherit' }}>{item.talent_title}</Typography>
              <Chip size="small" color={stateColor(item.state)} label={item.state_label ?? item.state} />
              <Chip size="small" variant="outlined" label={item.order_number} />
            </Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>
              구매자 {item.buyer_name} · 확정 {dateTime(item.created_at)}
              {item.state === 'completed' ? ` · 지급 ${dateTime(item.settled_at ?? undefined)}` : item.scheduled_at ? ` · 지급 예정 ${dateTime(item.scheduled_at)}` : ''}
            </Typography>
            {item.hold_reason && <Alert severity="warning" sx={{ mt: 1 }}>보류 사유: {item.hold_reason}</Alert>}
          </Box>
          <Box sx={{ textAlign: { md: 'right' }, flexShrink: 0 }}>
            <Typography variant="h3">{money(item.net_amount, item.currency)}</Typography>
            <Typography variant="body2" color="text.secondary">거래액 {money(item.gross_amount, item.currency)} · 수수료 −{money(item.platform_fee, item.currency)}</Typography>
          </Box>
        </Stack>
      </Card>)}
    </Stack>
    {cursor && <Box sx={{ textAlign: 'center', mt: 2 }}><Button onClick={() => fetchPage(cursor)} disabled={busy}>{busy ? '불러오는 중…' : '더 보기'}</Button></Box>}
    {!busy && items.length === 0 && <Card sx={{ p: 5, textAlign: 'center' }}><Typography color="text.secondary">아직 정산 내역이 없습니다. 구매확정된 주문부터 여기에 나타납니다.</Typography></Card>}
  </>
}
