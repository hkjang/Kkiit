import { useCallback, useEffect, useState } from 'react'
import { Link as RouterLink } from 'react-router-dom'
import { Alert, Box, Button, Card, Chip, MenuItem, Stack, TextField, Tooltip, Typography } from '@mui/material'
import RefreshRoundedIcon from '@mui/icons-material/RefreshRounded'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import type { RiskScore } from '../types'

const levelColor = (level: string) => level === 'CRITICAL' ? 'error' : level === 'HIGH' ? 'warning' : level === 'MEDIUM' ? 'info' : 'default'
const actionLabels: Record<string, string> = { settlement_hold: '정산 보류 대상' }

// Every score carries the named signals that produced it, so the queue explains
// itself instead of asking an operator to trust a number.
export function RiskQueue({ onLoaded }: { onLoaded?: (counts: { open_disputes: number; settlement_holds: number }) => void }) {
  const { notify } = useApp()
  const [items, setItems] = useState<RiskScore[]>([])
  const [level, setLevel] = useState('')
  const [busy, setBusy] = useState(false)
  const load = useCallback(() => {
    api<{ items: RiskScore[]; open_disputes: number; settlement_holds: number }>(`/api/v1/admin/risk${level ? `?level=${level}` : ''}`)
      .then((data) => { setItems(data.items); onLoaded?.({ open_disputes: data.open_disputes, settlement_holds: data.settlement_holds }) })
      .catch((cause) => notify(cause instanceof Error ? cause.message : '위험 신호를 불러오지 못했습니다.', 'error'))
  }, [level, notify, onLoaded])
  useEffect(() => { load() }, [load])

  const rescan = async () => {
    setBusy(true)
    try {
      const result = await api<{ changed_scores: number; settlements_processed: number }>('/api/v1/admin/risk/rescan', { method: 'POST', body: '{}' })
      load()
      notify(`재평가 완료 · 변경된 점수 ${result.changed_scores}건 · 처리된 정산 ${result.settlements_processed}건`, 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '재평가하지 못했습니다.', 'error') }
    finally { setBusy(false) }
  }

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 2 }}>
      <Box><Typography variant="h3">위험 신호</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>진행 중인 주문을 규칙 기반으로 주기 재평가한 결과입니다.</Typography></Box>
      <Stack direction="row" gap={1} alignItems="center">
        <TextField select size="small" label="등급" value={level} onChange={(event) => setLevel(event.target.value)} sx={{ minWidth: 160 }}>
          <MenuItem value="">전체</MenuItem><MenuItem value="CRITICAL">CRITICAL</MenuItem><MenuItem value="HIGH">HIGH</MenuItem><MenuItem value="MEDIUM">MEDIUM</MenuItem><MenuItem value="LOW">LOW</MenuItem>
        </TextField>
        <Button startIcon={<RefreshRoundedIcon />} onClick={rescan} disabled={busy}>{busy ? '재평가 중…' : '지금 재평가'}</Button>
      </Stack>
    </Stack>
    <Stack spacing={1.5}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Chip size="small" color={levelColor(item.level)} label={item.level} />
              <Typography variant="h4">{item.talent_title ?? item.resource_type}</Typography>
              {item.order_number && <Chip size="small" variant="outlined" label={item.order_number} />}
              {item.order_state && <Chip size="small" variant="outlined" label={item.order_state} />}
            </Stack>
            {item.buyer_name && <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>구매자 {item.buyer_name} · 판매자 {item.seller_name}</Typography>}
            <Stack direction="row" gap={.6} flexWrap="wrap" sx={{ mt: 1 }}>
              {(item.signals ?? []).map((signal) => <Tooltip key={signal.code} title={signal.detail}><Chip size="small" label={`${signal.label} +${signal.weight}`} /></Tooltip>)}
              {(item.signals ?? []).length === 0 && <Typography variant="body2" color="text.secondary">탐지된 신호가 없습니다.</Typography>}
            </Stack>
            {(item.actions ?? []).length > 0 && <Typography variant="body2" color="warning.main" sx={{ mt: 1 }}>{(item.actions ?? []).map((action) => actionLabels[action] ?? action).join(' · ')}</Typography>}
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1 }}>{item.model_version} · {dateTime(item.calculated_at)}</Typography>
          </Box>
          <Stack alignItems={{ md: 'end' }} gap={1} flexShrink={0}>
            <Typography variant="h3">{Number(item.score).toFixed(0)}점</Typography>
            {item.amount != null && <Typography variant="body2" color="text.secondary">{money(item.amount, item.currency ?? 'KRW')}</Typography>}
            {item.resource_type === 'order' && <Button size="small" component={RouterLink} to={`/orders/${item.resource_id}`}>주문 열기</Button>}
          </Stack>
        </Stack>
      </Card>)}
    </Stack>
    {items.length === 0 && <Alert severity="success">해당 등급의 위험 신호가 없습니다.</Alert>}
  </>
}
