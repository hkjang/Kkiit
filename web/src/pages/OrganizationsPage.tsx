import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, LinearProgress, MenuItem, Stack, TextField, Typography } from '@mui/material'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import PersonAddAltRoundedIcon from '@mui/icons-material/PersonAddAltRounded'
import DeleteOutlineRoundedIcon from '@mui/icons-material/DeleteOutlineRounded'
import ArrowBackRoundedIcon from '@mui/icons-material/ArrowBackRounded'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import type { Organization, OrganizationDetail, OrganizationOrder } from '../types'

const roleOptions: Array<[string, string]> = [['member', '구성원'], ['admin', '관리자'], ['owner', '소유자']]

export function OrganizationsPage() {
  const { notify } = useApp()
  const [items, setItems] = useState<Organization[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')

  const load = useCallback(() => {
    api<{ items: Organization[] }>('/api/v1/me/organizations')
      .then((data) => setItems(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '조직을 불러오지 못했습니다.', 'error'))
  }, [notify])
  useEffect(() => { load() }, [load])

  const create = async (event: FormEvent) => {
    event.preventDefault()
    try {
      const result = await api<{ id: string }>('/api/v1/organizations', { method: 'POST', body: JSON.stringify({ name }) })
      setCreating(false); setName(''); load(); setSelected(result.id)
      notify('조직을 만들었습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '조직을 만들지 못했습니다.', 'error') }
  }

  if (selected) return <OrganizationDetailView id={selected} onBack={() => { setSelected(null); load() }} />

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 3 }}>
      <Box><Typography variant="h2">조직</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>회사 계정으로 주문하면 예산 안에서만 지출되고 관리자가 전체 내역을 봅니다.</Typography></Box>
      <Button variant="contained" startIcon={<AddRoundedIcon />} onClick={() => setCreating(true)}>조직 만들기</Button>
    </Stack>
    <Stack spacing={1.5}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5, cursor: 'pointer' }} onClick={() => setSelected(item.id)}>
        <Stack direction="row" justifyContent="space-between" alignItems="center" gap={2}>
          <Box>
            <Stack direction="row" gap={1} alignItems="center"><Typography variant="h4">{item.name}</Typography><Chip size="small" label={item.role_label} color={item.role === 'member' ? 'default' : 'primary'} /></Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>구성원 {item.member_count}명 · 생성 {dateTime(item.created_at)}</Typography>
          </Box>
          <Button size="small">열기</Button>
        </Stack>
      </Card>)}
    </Stack>
    {items.length === 0 && <Card sx={{ p: 5, textAlign: 'center' }}><Typography color="text.secondary">소속된 조직이 없습니다.</Typography></Card>}

    <Dialog open={creating} onClose={() => setCreating(false)} fullWidth maxWidth="xs"><Box component="form" onSubmit={create}>
      <DialogTitle>조직 만들기</DialogTitle>
      <DialogContent><TextField autoFocus fullWidth label="조직 이름" value={name} onChange={(event) => setName(event.target.value)} required sx={{ mt: 1 }} helperText="만든 사람이 소유자가 됩니다." /></DialogContent>
      <DialogActions><Button onClick={() => setCreating(false)}>취소</Button><Button type="submit" variant="contained">만들기</Button></DialogActions>
    </Box></Dialog>
  </>
}

function OrganizationDetailView({ id, onBack }: { id: string; onBack: () => void }) {
  const { me, notify } = useApp()
  const [detail, setDetail] = useState<OrganizationDetail | null>(null)
  const [orders, setOrders] = useState<OrganizationOrder[]>([])
  const [inviting, setInviting] = useState(false)
  const [account, setAccount] = useState('')
  const [role, setRole] = useState('member')
  const [budgeting, setBudgeting] = useState(false)
  const [budget, setBudget] = useState({ name: '', amount: '1000000' })

  const load = useCallback(() => {
    api<OrganizationDetail>(`/api/v1/organizations/${id}`).then(setDetail).catch((cause) => notify(cause instanceof Error ? cause.message : '조직을 불러오지 못했습니다.', 'error'))
    api<{ items: OrganizationOrder[] }>(`/api/v1/organizations/${id}/orders`).then((data) => setOrders(data.items)).catch(() => undefined)
  }, [id, notify])
  useEffect(() => { load() }, [load])
  if (!detail) return <LinearProgress />

  const manages = detail.role === 'owner' || detail.role === 'admin'
  const act = async (run: () => Promise<unknown>, success: string) => {
    try { await run(); load(); notify(success, 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '처리하지 못했습니다.', 'error') }
  }

  return <>
    <Button startIcon={<ArrowBackRoundedIcon />} onClick={onBack} sx={{ mb: 1 }}>조직 목록</Button>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 3 }}>
      <Box><Typography variant="h2">{detail.name}</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>내 역할 {detail.role_label} · 구성원 {detail.members.length}명</Typography></Box>
      {manages && <Stack direction="row" gap={1}>
        <Button startIcon={<PersonAddAltRoundedIcon />} onClick={() => setInviting(true)}>구성원 추가</Button>
        <Button variant="contained" startIcon={<AddRoundedIcon />} onClick={() => setBudgeting(true)}>예산 추가</Button>
      </Stack>}
    </Stack>

    <Card sx={{ p: 3, mb: 3 }}>
      <Typography variant="h3">예산</Typography>
      {detail.budgets.length === 0 && <Alert severity="info" sx={{ mt: 2 }}>설정한 예산이 없습니다. 예산이 없으면 지출 상한 없이 주문할 수 있습니다.</Alert>}
      <Stack spacing={2} sx={{ mt: 2 }}>
        {detail.budgets.map((item) => {
          const used = item.amount > 0 ? Math.min(100, Math.round((item.consumed_amount / item.amount) * 100)) : 0
          return <Box key={item.id}>
            <Stack direction="row" justifyContent="space-between" alignItems="center" gap={1}>
              <Stack direction="row" gap={1} alignItems="center"><Typography fontWeight={750}>{item.name}</Typography>{!item.active && <Chip size="small" label="기간 아님" />}</Stack>
              <Typography variant="body2" color="text.secondary">{money(item.consumed_amount, item.currency)} / {money(item.amount, item.currency)}</Typography>
            </Stack>
            <LinearProgress variant="determinate" value={used} color={used >= 90 ? 'error' : used >= 70 ? 'warning' : 'primary'} sx={{ mt: .8, height: 8, borderRadius: 4 }} />
            <Typography variant="caption" color="text.secondary">잔액 {money(item.remaining_amount, item.currency)} · {dateTime(item.starts_at)} ~ {dateTime(item.ends_at)}</Typography>
          </Box>
        })}
      </Stack>
      {detail.spend && <Typography variant="body2" color="text.secondary" sx={{ mt: 2 }}>누적 주문 {detail.spend.order_count}건 · 진행 중 {detail.spend.active_orders}건 · 결제 합계 {money(detail.spend.paid_amount ?? 0)}</Typography>}
    </Card>

    <Card sx={{ p: 3, mb: 3 }}>
      <Typography variant="h3">구성원</Typography>
      <Stack spacing={1} sx={{ mt: 2 }}>
        {detail.members.map((item) => <Stack key={item.user_id} direction="row" justifyContent="space-between" alignItems="center" gap={2}>
          <Box><Typography fontWeight={650}>{item.display_name}</Typography><Typography variant="caption" color="text.secondary">@{item.username} · 합류 {dateTime(item.joined_at)}</Typography></Box>
          <Stack direction="row" gap={1} alignItems="center">
            {manages && item.user_id !== me?.id ? <TextField select size="small" value={item.role} onChange={(event) => act(() => api(`/api/v1/organizations/${id}/members/${item.user_id}`, { method: 'PATCH', body: JSON.stringify({ role: event.target.value }) }), '역할을 변경했습니다.')} sx={{ minWidth: 120 }}>
              {roleOptions.map(([value, label]) => <MenuItem key={value} value={value}>{label}</MenuItem>)}
            </TextField> : <Chip size="small" label={item.role_label} />}
            {manages && item.user_id !== me?.id && <IconButton aria-label={`${item.display_name} 제거`} color="error" onClick={() => act(() => api(`/api/v1/organizations/${id}/members/${item.user_id}`, { method: 'DELETE' }), '구성원을 제거했습니다.')}><DeleteOutlineRoundedIcon /></IconButton>}
          </Stack>
        </Stack>)}
      </Stack>
    </Card>

    <Card sx={{ p: 3 }}>
      <Typography variant="h3">{manages ? '조직 주문' : '내 조직 주문'}</Typography>
      <Stack spacing={1.2} sx={{ mt: 2 }}>
        {orders.map((item) => <Stack key={item.id} direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={1}>
          <Box><Typography fontWeight={650}>{item.talent_title}</Typography><Typography variant="caption" color="text.secondary">{item.order_number} · {item.buyer_name} → {item.seller_name} · {dateTime(item.created_at)}</Typography></Box>
          <Stack direction="row" gap={1} alignItems="center"><Chip size="small" label={item.state} /><Typography fontWeight={700}>{money(item.payable_amount, item.currency)}</Typography></Stack>
        </Stack>)}
      </Stack>
      {orders.length === 0 && <Typography color="text.secondary" sx={{ mt: 2 }}>조직 명의 주문이 아직 없습니다.</Typography>}
    </Card>

    <Dialog open={inviting} onClose={() => setInviting(false)} fullWidth maxWidth="xs">
      <DialogTitle>구성원 추가</DialogTitle>
      <DialogContent><Stack spacing={2} sx={{ mt: 1 }}>
        <TextField autoFocus label="아이디 또는 이메일" value={account} onChange={(event) => setAccount(event.target.value)} required />
        <TextField select label="역할" value={role} onChange={(event) => setRole(event.target.value)}>
          {roleOptions.map(([value, label]) => <MenuItem key={value} value={value}>{label}</MenuItem>)}
        </TextField>
      </Stack></DialogContent>
      <DialogActions>
        <Button onClick={() => setInviting(false)}>취소</Button>
        <Button variant="contained" onClick={() => { setInviting(false); act(() => api(`/api/v1/organizations/${id}/members`, { method: 'POST', body: JSON.stringify({ account, role }) }), '구성원을 추가했습니다.'); setAccount('') }}>추가</Button>
      </DialogActions>
    </Dialog>

    <Dialog open={budgeting} onClose={() => setBudgeting(false)} fullWidth maxWidth="xs">
      <DialogTitle>예산 추가</DialogTitle>
      <DialogContent><Stack spacing={2} sx={{ mt: 1 }}>
        <Alert severity="info">예산은 주문 시점에 차감되고 취소·환불되면 되돌아옵니다.</Alert>
        <TextField autoFocus label="예산 이름" value={budget.name} onChange={(event) => setBudget({ ...budget, name: event.target.value })} required />
        <TextField type="number" label="금액" value={budget.amount} onChange={(event) => setBudget({ ...budget, amount: event.target.value })} helperText="기본 기간은 오늘부터 한 달입니다." />
      </Stack></DialogContent>
      <DialogActions>
        <Button onClick={() => setBudgeting(false)}>취소</Button>
        <Button variant="contained" onClick={() => { setBudgeting(false); act(() => api(`/api/v1/organizations/${id}/budgets`, { method: 'POST', body: JSON.stringify({ name: budget.name, amount: Number(budget.amount) }) }), '예산을 추가했습니다.') }}>추가</Button>
      </DialogActions>
    </Dialog>
  </>
}
