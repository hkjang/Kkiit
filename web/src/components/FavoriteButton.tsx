import { useState } from 'react'
import { IconButton, Tooltip, Typography, Stack } from '@mui/material'
import FavoriteRoundedIcon from '@mui/icons-material/FavoriteRounded'
import FavoriteBorderRoundedIcon from '@mui/icons-material/FavoriteBorderRounded'
import { useNavigate } from 'react-router-dom'
import { api } from '../api'
import { useApp } from '../App'

// The button owns its own state so a card can toggle without reloading the
// whole list; a signed out visitor is sent to sign in instead of failing.
export function FavoriteButton({ talentId, title, initial, count, showCount = false, size = 'medium' }: {
  talentId: string; title: string; initial?: boolean; count?: number; showCount?: boolean; size?: 'small' | 'medium'
}) {
  const { me, notify } = useApp()
  const navigate = useNavigate()
  const [favorited, setFavorited] = useState(Boolean(initial))
  const [total, setTotal] = useState(count ?? 0)
  const [busy, setBusy] = useState(false)

  const toggle = async (event: React.MouseEvent) => {
    event.preventDefault(); event.stopPropagation()
    if (!me) { navigate('/login', { state: { from: `/talents/${talentId}` } }); return }
    setBusy(true)
    try {
      const result = await api<{ favorited: boolean; favorite_count: number }>(`/api/v1/talents/${talentId}/favorite`, { method: favorited ? 'DELETE' : 'POST' })
      setFavorited(result.favorited)
      setTotal(result.favorite_count)
    } catch (cause) { notify(cause instanceof Error ? cause.message : '찜 상태를 바꾸지 못했습니다.', 'error') }
    finally { setBusy(false) }
  }

  return <Stack direction="row" alignItems="center" spacing={.2}>
    <Tooltip title={favorited ? '찜 해제' : '찜하기'}>
      <IconButton size={size} onClick={toggle} disabled={busy} aria-label={`${title} ${favorited ? '찜 해제' : '찜하기'}`} sx={{ color: favorited ? 'error.main' : 'inherit' }}>
        {favorited ? <FavoriteRoundedIcon fontSize={size} /> : <FavoriteBorderRoundedIcon fontSize={size} />}
      </IconButton>
    </Tooltip>
    {showCount && total > 0 && <Typography variant="caption" color="text.secondary">{total}</Typography>}
  </Stack>
}
