import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Badge, Box, Button, CircularProgress, Divider, IconButton, List, ListItemButton, ListItemText, Popover, Stack, Tooltip, Typography } from '@mui/material'
import NotificationsNoneRoundedIcon from '@mui/icons-material/NotificationsNoneRounded'
import DoneAllRoundedIcon from '@mui/icons-material/DoneAllRounded'
import { api, dateTime } from '../api'
import { useApp } from '../App'
import type { Notification } from '../types'

// The bell keeps an open socket for live counts and falls back to a poll when
// the socket cannot be established, so an offline deployment behind a proxy
// that blocks upgrades still shows fresh notifications.
export function NotificationBell() {
  const { me, notify } = useApp()
  const navigate = useNavigate()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const [items, setItems] = useState<Notification[]>([])
  const [unread, setUnread] = useState(0)
  const [loading, setLoading] = useState(false)
  const socketRef = useRef<WebSocket | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api<{ items: Notification[]; unread_count: number }>('/api/v1/me/notifications?limit=20')
      setItems(data.items)
      setUnread(data.unread_count)
    } catch { /* 알림 조회 실패는 헤더를 막지 않습니다. */ }
    finally { setLoading(false) }
  }, [])

  useEffect(() => {
    if (!me) { setItems([]); setUnread(0); return }
    void load()
    let stopped = false
    let poll: ReturnType<typeof setInterval> | undefined
    const startPolling = () => { if (!poll) poll = setInterval(() => { void load() }, 60000) }
    try {
      const socket = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/v1/me/notifications/ws`)
      socketRef.current = socket
      socket.onmessage = (event) => {
        try {
          const payload = JSON.parse(event.data) as { type: string; unread_count?: number }
          if (payload.type === 'connected' && typeof payload.unread_count === 'number') setUnread(payload.unread_count)
          if (payload.type === 'notification.created') void load()
        } catch { /* 형식이 다른 프레임은 무시합니다. */ }
      }
      socket.onclose = () => { if (!stopped) startPolling() }
      socket.onerror = () => { if (!stopped) startPolling() }
    } catch { startPolling() }
    return () => { stopped = true; if (poll) clearInterval(poll); socketRef.current?.close(); socketRef.current = null }
  }, [me, load])

  const open = (event: React.MouseEvent<HTMLElement>) => { setAnchor(event.currentTarget); void load() }

  const readAll = async () => {
    try {
      const result = await api<{ unread_count: number }>('/api/v1/me/notifications/read', { method: 'POST', body: JSON.stringify({ all: true }) })
      setUnread(result.unread_count)
      setItems((previous) => previous.map((item) => ({ ...item, read_at: item.read_at ?? new Date().toISOString() })))
    } catch (cause) { notify(cause instanceof Error ? cause.message : '읽음 처리에 실패했습니다.', 'error') }
  }

  const openItem = async (item: Notification) => {
    setAnchor(null)
    if (!item.read_at) {
      try {
        const result = await api<{ unread_count: number }>('/api/v1/me/notifications/read', { method: 'POST', body: JSON.stringify({ ids: [item.id] }) })
        setUnread(result.unread_count)
      } catch { /* 이동을 막지 않습니다. */ }
    }
    if (item.link) navigate(item.link)
  }

  if (!me) return null
  return <>
    <Tooltip title="알림">
      <IconButton onClick={open} aria-label={unread > 0 ? `읽지 않은 알림 ${unread}건` : '알림'} aria-haspopup="true" aria-expanded={anchor ? 'true' : undefined}>
        <Badge badgeContent={unread} color="error" max={99}><NotificationsNoneRoundedIcon /></Badge>
      </IconButton>
    </Tooltip>
    <Popover open={Boolean(anchor)} anchorEl={anchor} onClose={() => setAnchor(null)} anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }} transformOrigin={{ vertical: 'top', horizontal: 'right' }} slotProps={{ paper: { sx: { width: { xs: 320, sm: 400 }, maxHeight: 520, mt: 1 } } }}>
      <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ px: 2, py: 1.5 }}>
        <Typography fontWeight={800}>알림</Typography>
        <Button size="small" startIcon={<DoneAllRoundedIcon />} onClick={readAll} disabled={unread === 0}>모두 읽음</Button>
      </Stack>
      <Divider />
      {loading && items.length === 0 && <Box sx={{ p: 4, display: 'grid', placeItems: 'center' }}><CircularProgress size={26} aria-label="알림 불러오는 중" /></Box>}
      {!loading && items.length === 0 && <Box sx={{ p: 4, textAlign: 'center' }}><Typography color="text.secondary">받은 알림이 없습니다.</Typography></Box>}
      <List disablePadding>
        {items.map((item) => <ListItemButton key={item.id} onClick={() => void openItem(item)} sx={{ alignItems: 'flex-start', py: 1.4, bgcolor: item.read_at ? 'transparent' : 'rgba(96,145,255,.08)' }}>
          <ListItemText
            primary={item.subject || item.event_type}
            secondary={<><Typography variant="body2" color="text.secondary" component="span" sx={{ display: 'block' }}>{item.body}</Typography><Typography variant="caption" color="text.secondary">{dateTime(item.created_at)}</Typography></>}
            slotProps={{ primary: { fontWeight: item.read_at ? 600 : 800 } }}
          />
        </ListItemButton>)}
      </List>
    </Popover>
  </>
}
