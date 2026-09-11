import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Stack, TextField, Typography } from '@mui/material'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import EditRoundedIcon from '@mui/icons-material/EditRounded'
import DeleteOutlineRoundedIcon from '@mui/icons-material/DeleteOutlineRounded'
import AttachFileRoundedIcon from '@mui/icons-material/AttachFileRounded'
import CloseRoundedIcon from '@mui/icons-material/CloseRounded'
import { api } from '../api'
import { useApp } from '../App'
import type { Portfolio } from '../types'

type Media = { url?: string; name?: string; mime_type?: string }
const emptyForm = { title: '', description: '', tags: '', media: [] as Media[] }

// Portfolio media is uploaded as a public file, so the same image is visible to
// visitors on the product page without a session.
export function PortfolioManager() {
  const { notify } = useApp()
  const [items, setItems] = useState<Portfolio[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Portfolio | null>(null)
  const [form, setForm] = useState(emptyForm)
  const [uploading, setUploading] = useState(false)

  const load = useCallback(() => {
    api<{ items: Portfolio[] }>('/api/v1/me/portfolios')
      .then((data) => setItems(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '포트폴리오를 불러오지 못했습니다.', 'error'))
  }, [notify])
  useEffect(() => { load() }, [load])

  const startCreate = () => { setEditing(null); setForm(emptyForm); setOpen(true) }
  const startEdit = (item: Portfolio) => {
    setEditing(item)
    setForm({ title: item.title, description: item.description, tags: item.tags.join(', '), media: item.media ?? [] })
    setOpen(true)
  }

  const upload = async (file?: File) => {
    if (!file) return
    setUploading(true)
    try {
      const data = new FormData()
      data.append('file', file)
      const result = await api<{ name: string; mime_type: string; download_url: string }>('/api/v1/me/files', { method: 'POST', body: data })
      setForm((current) => ({ ...current, media: [...current.media, { url: result.download_url, name: result.name, mime_type: result.mime_type }] }))
    } catch (cause) { notify(cause instanceof Error ? cause.message : '파일을 올리지 못했습니다.', 'error') }
    finally { setUploading(false) }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const body = JSON.stringify({ title: form.title, description: form.description, media: form.media, tags: form.tags.split(',').map((tag) => tag.trim()).filter(Boolean) })
    try {
      if (editing) await api(`/api/v1/me/portfolios/${editing.id}`, { method: 'PUT', body })
      else await api('/api/v1/me/portfolios', { method: 'POST', body })
      setOpen(false); load(); notify(editing ? '포트폴리오를 수정했습니다.' : '포트폴리오를 추가했습니다.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') }
  }

  const remove = async (item: Portfolio) => {
    if (!window.confirm(`${item.title} 포트폴리오를 삭제할까요?`)) return
    try { await api(`/api/v1/me/portfolios/${item.id}`, { method: 'DELETE' }); load(); notify('포트폴리오를 삭제했습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '삭제하지 못했습니다.', 'error') }
  }

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 2 }}>
      <Box><Typography variant="h3">포트폴리오</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>지난 작업을 보여주면 구매자가 상품을 판단할 근거가 생기고 상품 품질 점수에도 반영됩니다.</Typography></Box>
      <Button variant="contained" startIcon={<AddRoundedIcon />} onClick={startCreate}>작업 추가</Button>
    </Stack>
    <Stack spacing={2}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="h4">{item.title}</Typography>
            {item.description && <Typography color="text.secondary" sx={{ mt: .5, whiteSpace: 'pre-wrap' }}>{item.description}</Typography>}
            {(item.media ?? []).length > 0 && <Stack direction="row" gap={1} flexWrap="wrap" sx={{ mt: 1.5 }}>
              {(item.media ?? []).filter((media) => media.url).map((media, index) => <Box key={index} component="img" src={media.url} alt={media.name ?? item.title} loading="lazy" sx={{ width: 120, height: 84, objectFit: 'cover', borderRadius: 2, border: '1px solid', borderColor: 'divider' }} />)}
            </Stack>}
            <Stack direction="row" gap={.6} flexWrap="wrap" sx={{ mt: 1.5 }}>{item.tags.map((tag) => <Chip key={tag} size="small" variant="outlined" label={tag} />)}</Stack>
          </Box>
          <Stack direction="row" flexShrink={0} alignItems="start">
            <IconButton aria-label={`${item.title} 수정`} onClick={() => startEdit(item)}><EditRoundedIcon /></IconButton>
            <IconButton aria-label={`${item.title} 삭제`} color="error" onClick={() => void remove(item)}><DeleteOutlineRoundedIcon /></IconButton>
          </Stack>
        </Stack>
      </Card>)}
    </Stack>
    {items.length === 0 && <Card sx={{ p: 5, textAlign: 'center' }}><Typography color="text.secondary">등록한 작업이 없습니다.</Typography></Card>}

    <Dialog open={open} onClose={() => setOpen(false)} fullWidth maxWidth="sm"><Box component="form" onSubmit={submit}>
      <DialogTitle>{editing ? '작업 수정' : '작업 추가'}</DialogTitle>
      <DialogContent><Stack spacing={2} sx={{ mt: 1 }}>
        <TextField label="제목" value={form.title} onChange={(event) => setForm({ ...form, title: event.target.value })} required />
        <TextField label="설명" multiline minRows={3} value={form.description} onChange={(event) => setForm({ ...form, description: event.target.value })} />
        <TextField label="태그" value={form.tags} onChange={(event) => setForm({ ...form, tags: event.target.value })} helperText="쉼표로 구분하세요." />
        <Box>
          <Button component="label" variant="outlined" startIcon={<AttachFileRoundedIcon />} disabled={uploading}>{uploading ? '업로드 중…' : '이미지 첨부'}<input hidden type="file" accept="image/*" onChange={(event) => void upload(event.target.files?.[0])} /></Button>
          <Alert severity="info" sx={{ mt: 1 }}>첨부한 이미지는 상품 페이지에서 누구나 볼 수 있습니다.</Alert>
          <Stack direction="row" gap={1} flexWrap="wrap" sx={{ mt: 1.5 }}>
            {form.media.map((media, index) => <Box key={index} sx={{ position: 'relative' }}>
              <Box component="img" src={media.url} alt={media.name ?? '첨부 이미지'} sx={{ width: 110, height: 78, objectFit: 'cover', borderRadius: 2, border: '1px solid', borderColor: 'divider' }} />
              <IconButton size="small" aria-label="첨부 제거" onClick={() => setForm({ ...form, media: form.media.filter((_, position) => position !== index) })} sx={{ position: 'absolute', top: -8, right: -8, bgcolor: 'background.paper', boxShadow: 1 }}><CloseRoundedIcon fontSize="small" /></IconButton>
            </Box>)}
          </Stack>
        </Box>
      </Stack></DialogContent>
      <DialogActions><Button onClick={() => setOpen(false)}>취소</Button><Button type="submit" variant="contained">저장</Button></DialogActions>
    </Box></Dialog>
  </>
}
