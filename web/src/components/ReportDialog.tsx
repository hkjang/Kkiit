import { FormEvent, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Dialog, DialogActions, DialogContent, DialogTitle, MenuItem, Stack, TextField } from '@mui/material'
import FlagOutlinedIcon from '@mui/icons-material/FlagOutlined'
import { api } from '../api'
import { useApp } from '../App'

const reasons: Array<[string, string]> = [
  ['fraud', '사기 의심'], ['inappropriate', '부적절한 내용'], ['spam', '스팸·광고'],
  ['copyright', '저작권 침해'], ['impersonation', '사칭'], ['other', '기타'],
]

// One dialog serves every reportable resource so the reasons stay consistent
// wherever a user decides to raise something.
export function ReportDialog({ resourceType, resourceId, label, size = 'small' }: {
  resourceType: 'talent' | 'user' | 'order' | 'message'; resourceId: string; label?: string; size?: 'small' | 'medium'
}) {
  const { me, notify } = useApp()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState('inappropriate')
  const [details, setDetails] = useState('')
  const [busy, setBusy] = useState(false)

  const start = () => { if (!me) { navigate('/login'); return }; setOpen(true) }
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    try {
      await api('/api/v1/reports', { method: 'POST', body: JSON.stringify({ resource_type: resourceType, resource_id: resourceId, reason, details, evidence: [] }) })
      setOpen(false); setDetails('')
      notify('신고를 접수했습니다. 운영자가 검토합니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '신고를 접수하지 못했습니다.', 'error') }
    finally { setBusy(false) }
  }

  return <>
    <Button size={size} color="inherit" startIcon={<FlagOutlinedIcon />} onClick={start} sx={{ color: 'text.secondary' }}>{label ?? '신고'}</Button>
    <Dialog open={open} onClose={() => setOpen(false)} fullWidth maxWidth="xs"><form onSubmit={submit}>
      <DialogTitle>신고하기</DialogTitle>
      <DialogContent><Stack spacing={2} sx={{ mt: 1 }}>
        <TextField select label="신고 사유" value={reason} onChange={(event) => setReason(event.target.value)}>
          {reasons.map(([value, text]) => <MenuItem key={value} value={value}>{text}</MenuItem>)}
        </TextField>
        <TextField multiline minRows={3} label="상세 내용" value={details} onChange={(event) => setDetails(event.target.value)} helperText="확인에 도움이 되는 내용을 적어주세요." />
      </Stack></DialogContent>
      <DialogActions><Button onClick={() => setOpen(false)}>취소</Button><Button type="submit" variant="contained" color="error" disabled={busy}>신고 접수</Button></DialogActions>
    </form></Dialog>
  </>
}
