import { useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, Divider, Stack, TextField, Typography } from '@mui/material'
import { Link as RouterLink } from 'react-router-dom'
import { api, dateTime } from '../api'
import { useApp } from '../App'
import type { Inquiry, InquiryMessage } from '../types'

// Both sides of the same list. A seller who also buys should not have to
// remember which screen they are on to find a conversation.
export function InquiriesPage() {
  const { me, notify } = useApp()
  const [items, setItems] = useState<Inquiry[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [openThread, setOpenThread] = useState<Inquiry | null>(null)
  const [messages, setMessages] = useState<InquiryMessage[]>([])
  const [draft, setDraft] = useState('')

  const fetchPage = useCallback((next?: string) => {
    setBusy(true)
    api<{ items: Inquiry[]; next_cursor: string | null }>(`/api/v1/me/inquiries?limit=20${next ? `&cursor=${encodeURIComponent(next)}` : ''}`)
      .then((data) => { setItems((current) => next ? [...current, ...data.items] : data.items); setCursor(data.next_cursor) })
      .catch((cause) => notify(cause instanceof Error ? cause.message : '문의를 불러오지 못했습니다.', 'error'))
      .finally(() => setBusy(false))
  }, [notify])
  useEffect(() => { fetchPage() }, [fetchPage])

  const open = async (thread: Inquiry) => {
    setOpenThread(thread)
    try { setMessages((await api<{ items: InquiryMessage[] }>(`/api/v1/inquiries/${thread.id}/messages`)).items); fetchPage() }
    catch (cause) { notify(cause instanceof Error ? cause.message : '문의를 열지 못했습니다.', 'error') }
  }
  const send = async () => {
    if (!openThread || draft.trim().length < 2) return
    try {
      await api(`/api/v1/inquiries/${openThread.id}/messages`, { method: 'POST', body: JSON.stringify({ body: draft.trim() }) })
      setDraft('')
      setMessages((await api<{ items: InquiryMessage[] }>(`/api/v1/inquiries/${openThread.id}/messages`)).items)
      fetchPage()
    } catch (cause) { notify(cause instanceof Error ? cause.message : '메시지를 보내지 못했습니다.', 'error') }
  }

  return <>
    <Typography variant="h2">상품 문의</Typography>
    <Typography color="text.secondary" sx={{ mt: .5, mb: 3 }}>주문 전에 주고받은 대화입니다. 내가 문의한 것과 내 상품에 들어온 문의를 함께 보여줍니다.</Typography>
    <Stack spacing={1.5}>
      {items.map((thread) => <Card key={thread.id} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={1.5}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Typography variant="h4" component={RouterLink} to={`/talents/${thread.talent_id}`} sx={{ textDecoration: 'none', color: 'inherit', '&:hover': { textDecoration: 'underline' } }}>{thread.talent_title}</Typography>
              <Chip size="small" variant="outlined" label={thread.role === 'buyer' ? '내 문의' : '받은 문의'} />
              {thread.unread > 0 && <Chip size="small" color="primary" label={`새 메시지 ${thread.unread}`} />}
            </Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>
              {thread.role === 'buyer' ? `판매자 ${thread.seller.display_name}` : `구매자 ${thread.buyer.display_name}`} · {dateTime(thread.last_message_at)}
            </Typography>
            {thread.last_message && <Typography variant="body2" sx={{ mt: .8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{thread.last_message}</Typography>}
          </Box>
          <Button onClick={() => open(thread)} sx={{ flexShrink: 0 }}>{openThread?.id === thread.id ? '닫기' : '대화 열기'}</Button>
        </Stack>
        {openThread?.id === thread.id && <Box sx={{ mt: 2 }}>
          <Divider sx={{ mb: 2 }} />
          <Stack spacing={1.2}>
            {messages.map((message) => <Box key={message.id} sx={{ alignSelf: message.sender_id === me?.id ? 'flex-end' : 'flex-start', maxWidth: '80%' }}>
              <Typography variant="caption" color="text.secondary">{message.sender_name} · {dateTime(message.created_at)}</Typography>
              <Box sx={{ px: 1.8, py: 1.2, borderRadius: 2, bgcolor: message.sender_id === me?.id ? 'primary.main' : 'action.hover', color: message.sender_id === me?.id ? 'primary.contrastText' : 'inherit' }}>
                <Typography sx={{ whiteSpace: 'pre-wrap' }}>{message.body}</Typography>
              </Box>
            </Box>)}
          </Stack>
          <Stack direction="row" gap={1} sx={{ mt: 2 }}>
            <TextField fullWidth size="small" multiline minRows={2} value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="메시지를 입력하세요" />
            <Button variant="contained" onClick={send} disabled={draft.trim().length < 2}>보내기</Button>
          </Stack>
          {thread.role === 'buyer' && <Button component={RouterLink} to={`/talents/${thread.talent_id}`} sx={{ mt: 1 }}>이 상품 주문하러 가기</Button>}
        </Box>}
      </Card>)}
    </Stack>
    {cursor && <Box sx={{ textAlign: 'center', mt: 2 }}><Button onClick={() => fetchPage(cursor)} disabled={busy}>더 보기</Button></Box>}
    {items.length === 0 && <Alert severity="info">주고받은 문의가 없습니다.</Alert>}
  </>
}
