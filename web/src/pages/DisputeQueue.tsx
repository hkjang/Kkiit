import { useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Dialog, DialogActions, DialogContent, DialogTitle, Divider, MenuItem, Stack, TextField, Typography } from '@mui/material'
import GavelOutlinedIcon from '@mui/icons-material/GavelOutlined'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import type { AdminDispute, DisputeCase } from '../types'

const outcomeLabels: Record<string, string> = { refund_full: '전액 환불', refund_partial: '부분 환불', release_to_seller: '판매자 지급' }

// Resolving a dispute is the only operator action that moves escrow, so the
// dialog states the exact split before it is committed.
export function DisputeQueue() {
  const { notify } = useApp()
  const [items, setItems] = useState<AdminDispute[]>([])
  const [state, setState] = useState('open')
  const [target, setTarget] = useState<AdminDispute | null>(null)
  const [outcome, setOutcome] = useState('refund_full')
  const [refund, setRefund] = useState('0')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [caseFile, setCaseFile] = useState<DisputeCase | null>(null)

  const [slaHours, setSlaHours] = useState(72)
  const load = useCallback(() => {
    api<{ items: AdminDispute[]; sla_hours?: number }>(`/api/v1/admin/disputes${state ? `?state=${state}` : ''}`)
      .then((data) => { setItems(data.items); if (data.sla_hours) setSlaHours(data.sla_hours) })
      .catch((cause) => notify(cause instanceof Error ? cause.message : '분쟁을 불러오지 못했습니다.', 'error'))
  }, [state, notify])
  useEffect(() => { load() }, [load])

  // The evidence belongs in the moment of decision, not on a screen the
  // operator has to remember to open first.
  const open = (item: AdminDispute) => {
    setTarget(item); setOutcome('refund_full'); setRefund(String(Math.floor(item.amount / 2))); setNote(''); setCaseFile(null)
    api<DisputeCase>(`/api/v1/admin/disputes/${item.id}`).then(setCaseFile).catch(() => undefined)
  }
  const resolve = async () => {
    if (!target) return
    setBusy(true)
    try {
      await api(`/api/v1/admin/disputes/${target.id}/resolve`, { method: 'POST', body: JSON.stringify({ outcome, refund_amount: outcome === 'refund_partial' ? Number(refund) : 0, note }) })
      setTarget(null); load(); notify('분쟁을 처리했습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '처리하지 못했습니다.', 'error') }
    finally { setBusy(false) }
  }

  const refundAmount = target ? (outcome === 'refund_full' ? target.amount : outcome === 'refund_partial' ? Number(refund) || 0 : 0) : 0
  const sellerAmount = target ? target.amount - refundAmount : 0

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 2 }}>
      <Typography variant="h3">분쟁 대기열</Typography>
      <TextField select size="small" label="상태" value={state} onChange={(event) => setState(event.target.value)} sx={{ minWidth: 160 }}>
        <MenuItem value="open">접수</MenuItem><MenuItem value="under_review">검토 중</MenuItem><MenuItem value="resolved">처리 완료</MenuItem><MenuItem value="">전체</MenuItem>
      </TextField>
    </Stack>
    <Stack spacing={1.5}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Typography variant="h4">{item.talent_title}</Typography>
              <Chip size="small" color={item.state === 'resolved' ? 'default' : 'warning'} label={item.state} />
              <Chip size="small" variant="outlined" label={item.order_number} />
              {item.waiting_hours != null && (item.state === 'open' || item.state === 'under_review') && <Chip size="small" color={item.waiting_hours >= slaHours ? 'error' : 'default'} label={item.waiting_hours >= 24 ? `대기 ${Math.floor(item.waiting_hours / 24)}일` : `대기 ${item.waiting_hours}시간`} />}
              {(item.note_count ?? 0) > 0 && <Chip size="small" variant="outlined" label={`메모 ${item.note_count}`} />}
            </Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>구매자 {item.buyer_name} · 판매자 {item.seller_name} · 접수 {item.opened_by_name} {dateTime(item.created_at)}</Typography>
            <Typography sx={{ mt: 1 }}>{item.reason}</Typography>
            {item.resolution && <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>{item.resolution.label} · 환불 {money(item.resolution.refund_amount ?? 0, item.currency)} · 판매자 {money(item.resolution.seller_amount ?? 0, item.currency)}{item.resolution.note ? ` · ${item.resolution.note}` : ''}</Typography>}
          </Box>
          <Stack alignItems={{ md: 'end' }} gap={1} flexShrink={0}>
            <Typography variant="h3">{money(item.amount, item.currency)}</Typography>
            {item.state !== 'resolved' && <Button variant="contained" startIcon={<GavelOutlinedIcon />} onClick={() => open(item)}>분쟁 처리</Button>}
          </Stack>
        </Stack>
      </Card>)}
    </Stack>
    {items.length === 0 && <Card sx={{ p: 5, textAlign: 'center' }}><Typography color="text.secondary">해당 상태의 분쟁이 없습니다.</Typography></Card>}

    <Dialog open={Boolean(target)} onClose={() => setTarget(null)} fullWidth maxWidth="lg">
      <DialogTitle>분쟁 처리 · {target?.order_number}</DialogTitle>
      <DialogContent><Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1.1fr .9fr' }, gap: 3, mt: 1 }}>
      <Box sx={{ minWidth: 0 }}>
        {!caseFile ? <Typography color="text.secondary">사건 자료를 불러오는 중…</Typography> : <Stack spacing={2}>
          <Box>
            <Typography variant="caption" color="text.secondary">신청자</Typography>
            <Typography>{caseFile.opened_by.display_name} ({caseFile.opened_by.side === 'buyer' ? '구매자' : '판매자'}) · {dateTime(caseFile.created_at)}</Typography>
            <Typography sx={{ mt: .5, whiteSpace: 'pre-wrap' }}>{caseFile.reason}</Typography>
          </Box>
          <Divider />
          <Box>
            <Typography variant="caption" color="text.secondary">주문 요구사항</Typography>
            {Object.keys(caseFile.order.requirements ?? {}).length === 0 ? <Typography variant="body2">받은 요구사항이 없습니다.</Typography>
              : <Stack spacing={.5} sx={{ mt: .5 }}>{Object.entries(caseFile.order.requirements).map(([label, value]) => <Typography key={label} variant="body2"><b>{label}</b> · {String(value)}</Typography>)}</Stack>}
            {caseFile.order.overdue_days > 0 && <Chip size="small" color="warning" sx={{ mt: 1 }} label={`납기 ${caseFile.order.overdue_days}일 초과`} />}
          </Box>
          <Box>
            <Typography variant="caption" color="text.secondary">납품물 {caseFile.deliveries.length}건</Typography>
            {caseFile.deliveries.length === 0 ? <Alert severity="warning" sx={{ mt: .5 }}>납품된 결과물이 없습니다.</Alert>
              : <Stack spacing={.5} sx={{ mt: .5 }}>{caseFile.deliveries.map((item, index) => <Typography key={index} variant="body2">{dateTime(item.at)} · {item.type} · {item.description || '설명 없음'}</Typography>)}</Stack>}
          </Box>
          <Box>
            <Typography variant="caption" color="text.secondary">두 사람의 대화 {caseFile.messages.length}건</Typography>
            {caseFile.messages.length === 0 ? <Typography variant="body2" sx={{ mt: .5 }}>주고받은 메시지가 없습니다.</Typography>
              : <Stack spacing={1} sx={{ mt: .5, maxHeight: 260, overflowY: 'auto' }}>{caseFile.messages.map((item, index) => <Box key={index} sx={{ borderLeft: '3px solid', borderColor: item.side === 'buyer' ? 'primary.main' : 'secondary.main', pl: 1.5 }}>
                  <Typography variant="caption" color="text.secondary">{item.sender} ({item.side === 'buyer' ? '구매자' : '판매자'}) · {dateTime(item.at)}</Typography>
                  <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>{item.body}</Typography>
                </Box>)}</Stack>}
          </Box>
          <Box>
            <Typography variant="caption" color="text.secondary">주문 이력</Typography>
            <Stack spacing={.3} sx={{ mt: .5, maxHeight: 160, overflowY: 'auto' }}>{caseFile.timeline.map((item, index) => <Typography key={index} variant="caption" color="text.secondary">{dateTime(item.at)} · {item.event}{item.to ? ` → ${item.to}` : ''}{item.note ? ` · ${item.note}` : ''}</Typography>)}</Stack>
          </Box>
        </Stack>}
      </Box>
      <Stack spacing={2} sx={{ minWidth: 0 }}>
        <Alert severity="warning">처리하면 에스크로가 즉시 정산됩니다. 되돌리려면 별도 회수 절차가 필요합니다.</Alert>
        {caseFile?.order.settlement_state === 'completed' && <Alert severity="error">이 주문의 정산은 이미 지급 완료되었습니다. 분쟁으로 되돌릴 수 없으며 별도 회수 절차가 필요합니다.</Alert>}
        {caseFile && <Card variant="outlined" sx={{ p: 2, boxShadow: 'none' }}>
          {/* An accepted-then-disputed order holds its money in a settlement,
              not in escrow, so the escrow line alone would read as "nothing to
              refund" at the moment of deciding a refund. */}
          <Typography variant="body2">되돌릴 수 있는 금액 <strong>{money(caseFile.order.reclaimable, caseFile.order.currency)}</strong></Typography>
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>
            에스크로 {money(caseFile.order.escrow, caseFile.order.currency)}{caseFile.order.settlement_state ? ` · 정산 ${caseFile.order.settlement_state}` : ' · 정산 없음'}
          </Typography>
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: .5 }}>
            구매자 {caseFile.buyer.display_name} · 주문 {caseFile.buyer.orders}건 · 분쟁 제기 {caseFile.buyer.disputes_opened}회
          </Typography>
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>
            판매자 {caseFile.seller.display_name} · {caseFile.seller.level} · 평점 {Number(caseFile.seller.rating).toFixed(1)} · 주문 {caseFile.seller.orders}건 · 분쟁 대상 {caseFile.seller.disputes_against}회
          </Typography>
        </Card>}
        <TextField select label="처리 결과" value={outcome} onChange={(event) => setOutcome(event.target.value)}>
          {Object.entries(outcomeLabels).map(([value, label]) => <MenuItem key={value} value={value}>{label}</MenuItem>)}
        </TextField>
        {outcome === 'refund_partial' && <TextField type="number" label="환불 금액" value={refund} onChange={(event) => setRefund(event.target.value)} helperText={`0보다 크고 ${target ? money(target.amount, target.currency) : ''}보다 작아야 합니다.`} />}
        <TextField multiline minRows={2} label="처리 메모" value={note} onChange={(event) => setNote(event.target.value)} />
        <Card variant="outlined" sx={{ p: 2, boxShadow: 'none' }}>
          <Typography variant="body2">구매자 환불 <strong>{money(refundAmount, target?.currency)}</strong></Typography>
          <Typography variant="body2">판매자 정산 대상 <strong>{money(sellerAmount, target?.currency)}</strong> (수수료 차감 전)</Typography>
        </Card>
      </Stack>
      </Box></DialogContent>
      <DialogActions><Button onClick={() => setTarget(null)}>취소</Button><Button variant="contained" onClick={resolve} disabled={busy || (outcome === 'refund_partial' && (refundAmount <= 0 || refundAmount >= (target?.amount ?? 0)))}>처리 확정</Button></DialogActions>
    </Dialog>
  </>
}
