import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, IconButton, MenuItem, Stack, Switch, TextField, Typography } from '@mui/material'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import EditRoundedIcon from '@mui/icons-material/EditRounded'
import BlockRoundedIcon from '@mui/icons-material/BlockRounded'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import type { Coupon } from '../types'

const emptyForm = { code: '', name: '', discount_type: 'percent', discount_value: '10', min_order_amount: '0', max_discount_amount: '', usage_limit: '', per_user_limit: '1', starts_at: '', ends_at: '', active: true }
type Form = typeof emptyForm

const toForm = (item: Coupon): Form => ({
  code: item.code, name: item.name, discount_type: item.discount_type, discount_value: String(item.discount_value),
  min_order_amount: String(item.min_order_amount), max_discount_amount: item.max_discount_amount == null ? '' : String(item.max_discount_amount),
  usage_limit: item.usage_limit == null ? '' : String(item.usage_limit), per_user_limit: String(item.per_user_limit),
  starts_at: item.starts_at ? item.starts_at.slice(0, 16) : '', ends_at: item.ends_at ? item.ends_at.slice(0, 16) : '', active: item.active,
})

// Discounts are funded by the platform, so this page is where an operator sees
// exactly how much promotion spend a code has already committed.
export function CouponsAdmin() {
  const { notify } = useApp()
  const [items, setItems] = useState<Coupon[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Coupon | null>(null)
  const [form, setForm] = useState<Form>(emptyForm)

  const load = useCallback(() => {
    api<{ items: Coupon[] }>('/api/v1/admin/coupons')
      .then((data) => setItems(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '쿠폰을 불러오지 못했습니다.', 'error'))
  }, [notify])
  useEffect(() => { load() }, [load])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const body = JSON.stringify({
      code: form.code, name: form.name, discount_type: form.discount_type, discount_value: Number(form.discount_value),
      min_order_amount: Number(form.min_order_amount || 0),
      max_discount_amount: form.max_discount_amount ? Number(form.max_discount_amount) : undefined,
      usage_limit: form.usage_limit ? Number(form.usage_limit) : undefined,
      per_user_limit: Number(form.per_user_limit || 1),
      starts_at: form.starts_at ? new Date(form.starts_at).toISOString() : undefined,
      ends_at: form.ends_at ? new Date(form.ends_at).toISOString() : undefined,
      active: form.active,
    })
    try {
      if (editing) await api(`/api/v1/admin/coupons/${editing.id}`, { method: 'PUT', body })
      else await api('/api/v1/admin/coupons', { method: 'POST', body })
      setOpen(false); load(); notify(editing ? '쿠폰을 수정했습니다.' : '쿠폰을 발행했습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') }
  }

  const stop = async (item: Coupon) => {
    if (!window.confirm(`${item.code} 쿠폰 사용을 중단할까요? 이미 사용된 기록은 남습니다.`)) return
    try { await api(`/api/v1/admin/coupons/${item.id}`, { method: 'DELETE' }); load(); notify('쿠폰 사용을 중단했습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '중단하지 못했습니다.', 'error') }
  }

  const discountLabel = (item: Coupon) => item.discount_type === 'percent'
    ? `${item.discount_value}%${item.max_discount_amount ? ` (최대 ${money(item.max_discount_amount)})` : ''}`
    : money(item.discount_value)

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 3 }}>
      <Box><Typography variant="h2">할인 쿠폰</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>할인은 플랫폼이 부담하며 판매자 정산 금액은 그대로입니다.</Typography></Box>
      <Button variant="contained" startIcon={<AddRoundedIcon />} onClick={() => { setEditing(null); setForm(emptyForm); setOpen(true) }}>쿠폰 발행</Button>
    </Stack>
    <Stack spacing={1.5}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5, opacity: item.active ? 1 : .6 }}>
        <Stack direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Typography variant="h4" sx={{ fontFamily: 'monospace' }}>{item.code}</Typography>
              <Chip size="small" label={discountLabel(item)} color="primary" variant="outlined" />
              {!item.active && <Chip size="small" label="중단됨" />}
            </Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>{item.name}</Typography>
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: .5 }}>
              최소 주문 {money(item.min_order_amount)} · 1인 {item.per_user_limit}회 · 전체 {item.usage_limit ?? '무제한'}
              {item.starts_at || item.ends_at ? ` · ${dateTime(item.starts_at ?? undefined)} ~ ${dateTime(item.ends_at ?? undefined)}` : ''}
            </Typography>
          </Box>
          <Stack alignItems={{ md: 'end' }} gap={.5} flexShrink={0}>
            <Typography variant="body2" color="text.secondary">사용 {item.redemption_count}건</Typography>
            <Typography variant="h4">{money(item.redeemed_amount)}</Typography>
            <Stack direction="row">
              <IconButton aria-label={`${item.code} 수정`} onClick={() => { setEditing(item); setForm(toForm(item)); setOpen(true) }}><EditRoundedIcon /></IconButton>
              {item.active && <IconButton aria-label={`${item.code} 중단`} color="error" onClick={() => void stop(item)}><BlockRoundedIcon /></IconButton>}
            </Stack>
          </Stack>
        </Stack>
      </Card>)}
    </Stack>
    {items.length === 0 && <Card sx={{ p: 5, textAlign: 'center' }}><Typography color="text.secondary">발행한 쿠폰이 없습니다.</Typography></Card>}

    <Dialog open={open} onClose={() => setOpen(false)} fullWidth maxWidth="sm"><Box component="form" onSubmit={submit}>
      <DialogTitle>{editing ? '쿠폰 수정' : '쿠폰 발행'}</DialogTitle>
      <DialogContent><Stack spacing={2} sx={{ mt: 1 }}>
        <Alert severity="info">할인액은 플랫폼 프로모션 비용으로 기록되며 판매자 정산에는 영향을 주지 않습니다.</Alert>
        <TextField label="쿠폰 코드" value={form.code} onChange={(event) => setForm({ ...form, code: event.target.value.toUpperCase() })} required helperText="공백 없이 3자 이상" />
        <TextField label="쿠폰 이름" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} required />
        <Stack direction="row" gap={1.5}>
          <TextField select fullWidth label="할인 유형" value={form.discount_type} onChange={(event) => setForm({ ...form, discount_type: event.target.value })}>
            <MenuItem value="percent">비율(%)</MenuItem><MenuItem value="fixed">정액(원)</MenuItem>
          </TextField>
          <TextField fullWidth type="number" label={form.discount_type === 'percent' ? '할인율' : '할인 금액'} value={form.discount_value} onChange={(event) => setForm({ ...form, discount_value: event.target.value })} required />
        </Stack>
        <Stack direction="row" gap={1.5}>
          <TextField fullWidth type="number" label="최소 주문 금액" value={form.min_order_amount} onChange={(event) => setForm({ ...form, min_order_amount: event.target.value })} />
          <TextField fullWidth type="number" label="최대 할인 금액" value={form.max_discount_amount} onChange={(event) => setForm({ ...form, max_discount_amount: event.target.value })} helperText="비우면 제한 없음" />
        </Stack>
        <Stack direction="row" gap={1.5}>
          <TextField fullWidth type="number" label="전체 사용 한도" value={form.usage_limit} onChange={(event) => setForm({ ...form, usage_limit: event.target.value })} helperText="비우면 무제한" />
          <TextField fullWidth type="number" label="1인 사용 한도" value={form.per_user_limit} onChange={(event) => setForm({ ...form, per_user_limit: event.target.value })} />
        </Stack>
        <Stack direction={{ xs: 'column', sm: 'row' }} gap={1.5}>
          <TextField fullWidth type="datetime-local" label="시작" value={form.starts_at} onChange={(event) => setForm({ ...form, starts_at: event.target.value })} slotProps={{ inputLabel: { shrink: true } }} />
          <TextField fullWidth type="datetime-local" label="종료" value={form.ends_at} onChange={(event) => setForm({ ...form, ends_at: event.target.value })} slotProps={{ inputLabel: { shrink: true } }} />
        </Stack>
        <FormControlLabel control={<Switch checked={form.active} onChange={(event) => setForm({ ...form, active: event.target.checked })} />} label="즉시 사용 가능" />
      </Stack></DialogContent>
      <DialogActions><Button onClick={() => setOpen(false)}>취소</Button><Button type="submit" variant="contained">저장</Button></DialogActions>
    </Box></Dialog>
  </>
}
