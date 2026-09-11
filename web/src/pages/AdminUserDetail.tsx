import { useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Checkbox, Chip, CircularProgress, Divider, FormControlLabel, Stack, TextField, Typography } from '@mui/material'
import { Link as RouterLink } from 'react-router-dom'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import type { AdminUserDetail as Detail, OperatorNote } from '../types'

// One person, in one place. Investigating a complaint used to mean opening the
// user list, the order list, the dispute queue, the report queue and the audit
// log and holding the answer in your head.
export function AdminUserDetail({ userId, onClose }: { userId: string; onClose: () => void }) {
  const { me, notify } = useApp()
  const [detail, setDetail] = useState<Detail | null>(null)
  const [error, setError] = useState('')
  const [notes, setNotes] = useState<OperatorNote[]>([])
  const [draft, setDraft] = useState('')
  const [pinned, setPinned] = useState(false)

  const loadNotes = useCallback(() => {
    api<{ items: OperatorNote[] }>(`/api/v1/admin/notes?subject_type=user&subject_id=${userId}`)
      .then((data) => setNotes(data.items)).catch(() => undefined)
  }, [userId])
  useEffect(() => { loadNotes() }, [loadNotes])

  const addNote = async () => {
    const body = draft.trim()
    if (body.length < 2) return
    try {
      await api('/api/v1/admin/notes', { method: 'POST', body: JSON.stringify({ subject_type: 'user', subject_id: userId, body, pinned }) })
      setDraft(''); setPinned(false); loadNotes(); notify('메모를 남겼습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '메모를 남기지 못했습니다.', 'error') }
  }
  const removeNote = async (note: OperatorNote) => {
    if (!window.confirm('이 메모를 삭제할까요?')) return
    try { await api(`/api/v1/admin/notes/${note.id}`, { method: 'DELETE' }); loadNotes() }
    catch (cause) { notify(cause instanceof Error ? cause.message : '메모를 삭제하지 못했습니다.', 'error') }
  }

  const load = useCallback(() => {
    api<Detail>(`/api/v1/admin/users/${userId}`).then(setDetail).catch((cause) => setError(cause instanceof Error ? cause.message : '사용자를 불러오지 못했습니다.'))
  }, [userId])
  useEffect(() => { load() }, [load])

  const revokeSessions = async () => {
    if (!detail || !window.confirm(`${detail.display_name} 계정을 정지하면 모든 세션이 즉시 차단됩니다. 계속할까요?`)) return
    try {
      await api(`/api/v1/admin/users/${userId}`, { method: 'PATCH', body: JSON.stringify({ status: 'suspended', display_name: detail.display_name }) })
      load(); notify('계정을 정지했습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '정지하지 못했습니다.', 'error') }
  }

  if (error) return <Alert severity="error" sx={{ mb: 2 }}>{error}</Alert>
  if (!detail) return <Box sx={{ py: 6, display: 'grid', placeItems: 'center' }}><CircularProgress /></Box>

  // A rate needs something to divide by. "0% 완료" for someone with no orders
  // describes a new account as a bad one.
  const totalOrders = detail.orders.bought + detail.orders.sold
  const figures: [string, string][] = [
    ['구매 / 판매', `${detail.orders.bought} / ${detail.orders.sold}`],
    ['진행 중', String(detail.orders.live)],
    ['거래 완료', totalOrders > 0 ? `${detail.orders.completed}건` : '—'],
    ['취소·환불', String(detail.orders.cancelled)],
    ['결제 총액', money(detail.money.paid)],
    ['지급 완료', money(detail.money.settled)],
    ['지급 예정', money(detail.money.pending_settlement)],
  ]
  const trouble: [string, number][] = [
    ['제기한 분쟁', detail.trouble.disputes_opened],
    ['상대가 제기한 분쟁', detail.trouble.disputes_against],
    ['접수한 신고', detail.trouble.reports_filed],
    ['이 계정에 대한 신고', detail.trouble.reports_against],
  ]
  const flagged = detail.trouble.disputes_against + detail.trouble.reports_against

  return <Box>
    <Stack direction="row" justifyContent="space-between" alignItems="center" gap={2} sx={{ mb: 2 }} flexWrap="wrap">
      <Box>
        <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
          <Typography variant="h3">{detail.display_name}</Typography>
          <Chip size="small" color={detail.status === 'active' ? 'success' : 'error'} label={detail.status} />
          {detail.mfa_enabled && <Chip size="small" variant="outlined" label="MFA 사용" />}
          {detail.roles.map((role) => <Chip key={role} size="small" label={role} />)}
        </Stack>
        <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>
          @{detail.username} · {detail.email ?? '이메일 없음'} · {dateTime(detail.created_at)} 가입 · 최근 로그인 {dateTime(detail.last_login_at ?? undefined)}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          활성 세션 {detail.active_sessions} · API 키 {detail.active_api_keys} · 연결된 로그인 {detail.linked_identities}
        </Typography>
      </Box>
      <Stack direction="row" gap={1}>
        {/* The point of a case file is that it leads somewhere: the orders are
            the next thing an operator opens, and they should not have to
            search for the account again to get there. */}
        <Button component={RouterLink} to={`/admin/orders?user=${detail.id}`}>이 계정의 주문</Button>
        {detail.status === 'active' && <Button color="error" onClick={revokeSessions}>계정 정지</Button>}
        <Button onClick={onClose}>닫기</Button>
      </Stack>
    </Stack>

    {flagged > 0 && <Alert severity={flagged >= 3 ? 'error' : 'warning'} sx={{ mb: 2 }}>
      이 계정을 상대로 제기된 분쟁 {detail.trouble.disputes_against}건, 신고 {detail.trouble.reports_against}건이 있습니다.
    </Alert>}

    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr 1fr', md: 'repeat(4,1fr)' }, gap: 2 }}>
      {figures.map(([label, value]) => <Card key={label} sx={{ p: 2 }}>
        <Typography variant="caption" color="text.secondary">{label}</Typography>
        <Typography variant="h4" sx={{ mt: .3 }}>{value}</Typography>
      </Card>)}
    </Box>

    {detail.seller && <Card sx={{ p: 2.5, mt: 2 }}>
      <Typography variant="h4">판매자</Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>{detail.seller.headline || '소개 없음'}</Typography>
      <Typography variant="body2" sx={{ mt: 1 }}>
        등급 {detail.seller.level} · 점수 {Number(detail.seller.score).toFixed(0)} · 평점 {Number(detail.seller.rating).toFixed(1)} ({detail.seller.rating_count}건) · 공개 상품 {detail.seller.published_talents} · 동시 한도 {detail.seller.capacity}
      </Typography>
    </Card>}

    <Card sx={{ p: 2.5, mt: 2 }}>
      <Typography variant="h4">분쟁과 신고</Typography>
      <Stack direction="row" gap={3} flexWrap="wrap" sx={{ mt: 1 }}>
        {trouble.map(([label, count]) => <Typography key={label} variant="body2" color="text.secondary">{label} <b style={{ color: count > 0 ? undefined : 'inherit' }}>{count}</b></Typography>)}
      </Stack>
    </Card>

    {detail.organizations.length > 0 && <Card sx={{ p: 2.5, mt: 2 }}>
      <Typography variant="h4">소속 조직</Typography>
      <Stack direction="row" gap={1} flexWrap="wrap" sx={{ mt: 1 }}>
        {detail.organizations.map((org) => <Chip key={org.id} size="small" variant="outlined" label={`${org.name} · ${org.role}`} />)}
      </Stack>
    </Card>}

    <Card sx={{ p: 2.5, mt: 2 }}>
      <Typography variant="h4">운영 메모</Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mt: .3 }}>다음 담당자가 같은 조사를 처음부터 다시 하지 않도록, 판단과 그 이유를 남겨 주세요. 모든 운영자가 봅니다.</Typography>
      <Stack direction={{ xs: 'column', sm: 'row' }} gap={1} alignItems="flex-start" sx={{ mt: 1.5 }}>
        <TextField fullWidth size="small" multiline minRows={2} value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="예: 납기 지연 3회째. 경고 안내했고 다음 건까지 지켜봅니다." />
        <Stack gap={.5}>
          <Button variant="contained" onClick={addNote} disabled={draft.trim().length < 2}>남기기</Button>
          <FormControlLabel control={<Checkbox size="small" checked={pinned} onChange={(event) => setPinned(event.target.checked)} />} label={<Typography variant="caption">상단 고정</Typography>} />
        </Stack>
      </Stack>
      {notes.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1.5 }}>남겨진 메모가 없습니다.</Typography> : <Stack divider={<Divider />} sx={{ mt: 1.5 }}>
        {notes.map((note) => <Box key={note.id} sx={{ py: 1.2 }}>
          <Stack direction="row" justifyContent="space-between" alignItems="center" gap={1}>
            <Stack direction="row" gap={1} alignItems="center">
              {note.pinned && <Chip size="small" color="primary" label="고정" />}
              <Typography variant="caption" fontWeight={750}>{note.author_name}</Typography>
              <Typography variant="caption" color="text.secondary">{dateTime(note.created_at)}</Typography>
            </Stack>
            {note.author_id === me?.id && <Button size="small" color="error" onClick={() => removeNote(note)}>삭제</Button>}
          </Stack>
          <Typography sx={{ mt: .5, whiteSpace: 'pre-wrap' }}>{note.body}</Typography>
        </Box>)}
      </Stack>}
    </Card>

    <Card sx={{ p: 2.5, mt: 2 }}>
      <Typography variant="h4">최근 활동</Typography>
      {detail.recent_actions.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1 }}>기록된 작업이 없습니다.</Typography> : <Stack divider={<Divider />} sx={{ mt: 1 }}>
        {detail.recent_actions.map((entry, index) => <Stack key={`${entry.occurred_at}-${index}`} direction="row" justifyContent="space-between" gap={2} sx={{ py: .8 }}>
          <Typography variant="body2">{entry.action} <Typography component="span" variant="caption" color="text.secondary">{entry.resource_type}</Typography></Typography>
          <Stack direction="row" gap={1} alignItems="center">
            {entry.result !== 'success' && <Chip size="small" color="error" label={entry.result} />}
            <Typography variant="caption" color="text.secondary">{dateTime(entry.occurred_at)}</Typography>
          </Stack>
        </Stack>)}
      </Stack>}
    </Card>
  </Box>
}
