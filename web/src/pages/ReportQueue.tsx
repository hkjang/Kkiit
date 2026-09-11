import { useCallback, useEffect, useState } from 'react'
import { Link as RouterLink } from 'react-router-dom'
import { Alert, Box, Button, Card, Chip, Dialog, DialogActions, DialogContent, DialogTitle, Divider, MenuItem, Stack, TextField, Typography } from '@mui/material'
import GavelOutlinedIcon from '@mui/icons-material/GavelOutlined'
import { api, dateTime } from '../api'
import { useApp } from '../App'
import { reportStateLabels, type Report, type ReportCase } from '../types'

const resourceLabels: Record<string, string> = { talent: '상품', user: '사용자', order: '주문', message: '메시지' }
const actionLabels: Array<[string, string, string]> = [
  ['dismiss', '신고 기각', '신고 내용이 정책 위반이 아닐 때'],
  ['warn', '경고 안내', '대상에게 알림만 보낼 때'],
  ['hide_talent', '상품 비공개', '상품 신고에만 적용됩니다'],
  ['suspend_user', '계정 정지', '사용자 또는 상품 신고에 적용됩니다'],
]

// Each action is a real state change, so the dialog spells out what the choice
// will do before the operator commits to it.
export function ReportQueue() {
  const { notify } = useApp()
  const [items, setItems] = useState<Report[]>([])
  const [state, setState] = useState('open')
  const [target, setTarget] = useState<Report | null>(null)
  const [action, setAction] = useState('dismiss')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [caseFile, setCaseFile] = useState<ReportCase | null>(null)

  const load = useCallback(() => {
    api<{ items: Report[] }>(`/api/v1/admin/reports${state ? `?state=${state}` : ''}`)
      .then((data) => setItems(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '신고를 불러오지 못했습니다.', 'error'))
  }, [state, notify])
  useEffect(() => { load() }, [load])

  const resolve = async () => {
    if (!target) return
    setBusy(true)
    try {
      await api(`/api/v1/admin/reports/${target.id}/resolve`, { method: 'POST', body: JSON.stringify({ action, note }) })
      setTarget(null); load(); notify('신고를 처리했습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '처리하지 못했습니다.', 'error') }
    finally { setBusy(false) }
  }

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 2 }}>
      <Box><Typography variant="h3">신고 대기열</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>처리하면 상품 비공개나 계정 정지가 즉시 적용됩니다.</Typography></Box>
      <TextField select size="small" label="상태" value={state} onChange={(event) => setState(event.target.value)} sx={{ minWidth: 160 }}>
        <MenuItem value="open">접수</MenuItem><MenuItem value="reviewing">검토 중</MenuItem><MenuItem value="resolved">처리 완료</MenuItem><MenuItem value="">전체</MenuItem>
      </TextField>
    </Stack>
    <Stack spacing={1.5}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Typography variant="h4">{item.reason_label}</Typography>
              <Chip size="small" variant="outlined" label={resourceLabels[item.resource_type] ?? item.resource_type} />
              <Chip size="small" color={item.state === 'resolved' ? 'default' : 'warning'} label={reportStateLabels[item.state] ?? item.state} />
              {(item.report_count ?? 0) > 1 && <Chip size="small" color="error" label={`같은 대상 ${item.report_count}건`} />}{(item.note_count ?? 0) > 0 && <Chip size="small" variant="outlined" label={`메모 ${item.note_count}`} />}
            </Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>
              대상 {item.resource_label || item.resource_id} · 신고자 {item.reporter_name} · {dateTime(item.created_at)}
            </Typography>
            {item.details && <Typography sx={{ mt: 1, whiteSpace: 'pre-wrap' }}>{item.details}</Typography>}
            {item.resolution && <Alert severity="info" sx={{ mt: 1 }}>{item.resolution}</Alert>}
          </Box>
          <Stack gap={1} flexShrink={0} alignItems={{ md: 'end' }}>
            {item.resource_type === 'talent' && <Button size="small" component={RouterLink} to={`/talents/${item.resource_id}`}>상품 보기</Button>}
            {item.state !== 'resolved' && <Button variant="contained" startIcon={<GavelOutlinedIcon />} onClick={() => { setTarget(item); setAction(item.resource_type === 'talent' ? 'hide_talent' : 'dismiss'); setNote(''); setCaseFile(null); api<ReportCase>(`/api/v1/admin/reports/${item.id}`).then(setCaseFile).catch(() => undefined) }}>신고 처리</Button>}
          </Stack>
        </Stack>
      </Card>)}
    </Stack>
    {items.length === 0 && <Alert severity="success">해당 상태의 신고가 없습니다.</Alert>}

    <Dialog open={Boolean(target)} onClose={() => setTarget(null)} fullWidth maxWidth="lg">
      <DialogTitle>신고 처리 · {target?.reason_label}</DialogTitle>
      <DialogContent><Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1.1fr .9fr' }, gap: 3, mt: 1 }}>
      <Box sx={{ minWidth: 0 }}>
        {!caseFile ? <Typography color="text.secondary">신고 자료를 불러오는 중…</Typography> : <Stack spacing={2}>
          <Box>
            <Typography variant="caption" color="text.secondary">신고 내용</Typography>
            <Typography sx={{ whiteSpace: 'pre-wrap' }}>{caseFile.details || '추가 설명이 없습니다.'}</Typography>
          </Box>
          <Divider />
          <Box>
            <Typography variant="caption" color="text.secondary">신고된 대상</Typography>
            {!caseFile.subject ? <Alert severity="warning" sx={{ mt: .5 }}>대상을 찾을 수 없습니다. 이미 삭제되었을 수 있습니다.</Alert>
              : caseFile.subject.kind === 'talent' ? <Box sx={{ mt: .5 }}>
                  <Typography fontWeight={700}>{caseFile.subject.title} <Chip size="small" label={caseFile.subject.status} sx={{ ml: 1 }} /></Typography>
                  <Typography variant="body2" color="text.secondary">{caseFile.subject.summary}</Typography>
                  <Typography variant="body2" sx={{ mt: 1, whiteSpace: 'pre-wrap', maxHeight: 220, overflowY: 'auto' }}>{caseFile.subject.description}</Typography>
                  <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1 }}>판매자 {caseFile.subject.seller.display_name} ({caseFile.subject.seller.status})</Typography>
                </Box>
              : caseFile.subject.kind === 'user' ? <Box sx={{ mt: .5 }}>
                  <Typography fontWeight={700}>{caseFile.subject.display_name} <Chip size="small" label={caseFile.subject.status} sx={{ ml: 1 }} /></Typography>
                  <Typography variant="body2" color="text.secondary">{dateTime(caseFile.subject.created_at)} 가입 · 거래 {caseFile.subject.orders}건</Typography>
                  {caseFile.subject.headline && <Typography variant="body2" sx={{ mt: 1 }}>{caseFile.subject.headline}</Typography>}
                  {caseFile.subject.biography && <Typography variant="body2" sx={{ mt: .5, whiteSpace: 'pre-wrap' }}>{caseFile.subject.biography}</Typography>}
                </Box>
              : caseFile.subject.kind === 'order' ? <Box sx={{ mt: .5 }}>
                  <Typography fontWeight={700}>{caseFile.subject.order_number} <Chip size="small" label={caseFile.subject.state} sx={{ ml: 1 }} /></Typography>
                  <Typography variant="body2" color="text.secondary">{caseFile.subject.talent_title} · {caseFile.subject.buyer} → {caseFile.subject.seller}</Typography>
                </Box>
              : <Box sx={{ mt: .5 }}>
                  <Typography variant="caption" color="text.secondary">{caseFile.subject.sender.display_name} · {caseFile.subject.order_number} · {dateTime(caseFile.subject.at)}</Typography>
                  <Typography sx={{ mt: .5, whiteSpace: 'pre-wrap' }}>{caseFile.subject.body}</Typography>
                </Box>}
          </Box>
          <Box>
            <Typography variant="caption" color="text.secondary">같은 대상에 접수된 다른 신고 {caseFile.history.length}건</Typography>
            {caseFile.history.length === 0 ? <Typography variant="body2" sx={{ mt: .5 }}>이번이 처음입니다.</Typography>
              : <Stack spacing={.3} sx={{ mt: .5, maxHeight: 140, overflowY: 'auto' }}>{caseFile.history.map((entry) => <Typography key={entry.id} variant="caption" color="text.secondary">
                  {dateTime(entry.at)} · {entry.reason} · {entry.state}{entry.resolution ? ` · ${entry.resolution}` : ''}
                </Typography>)}</Stack>}
          </Box>
        </Stack>}
      </Box>
      <Stack spacing={2} sx={{ minWidth: 0 }}>
        <Alert severity="warning">선택한 조치는 즉시 적용됩니다. 계정을 정지하면 해당 사용자의 세션도 함께 종료됩니다.</Alert>
        {caseFile && <Card variant="outlined" sx={{ p: 2, boxShadow: 'none' }}>
          <Typography variant="caption" color="text.secondary">신고자</Typography>
          <Typography variant="body2">{caseFile.reporter.display_name} · {dateTime(caseFile.reporter.created_at)} 가입</Typography>
          {/* Someone filing faster than anyone can read them is a different
              situation from someone reporting for the first time. */}
          <Typography variant="body2" color={caseFile.reporter.reports_open > 5 ? 'warning.main' : 'text.secondary'}>
            누적 신고 {caseFile.reporter.reports_filed}건 · 미처리 {caseFile.reporter.reports_open}건
          </Typography>
        </Card>}
        <TextField select label="조치" value={action} onChange={(event) => setAction(event.target.value)}>
          {actionLabels.map(([value, label, hint]) => <MenuItem key={value} value={value}><Stack><span>{label}</span><Typography variant="caption" color="text.secondary">{hint}</Typography></Stack></MenuItem>)}
        </TextField>
        <TextField multiline minRows={2} label="처리 메모" value={note} onChange={(event) => setNote(event.target.value)} helperText="신고자에게 전달되는 처리 결과에 함께 기록됩니다." />
      </Stack>
      </Box></DialogContent>
      <DialogActions><Button onClick={() => setTarget(null)}>취소</Button><Button variant="contained" onClick={resolve} disabled={busy}>처리 확정</Button></DialogActions>
    </Dialog>
  </>
}
