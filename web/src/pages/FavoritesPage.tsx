import { useCallback, useEffect, useState } from 'react'
import { Link as RouterLink } from 'react-router-dom'
import { Box, Card, Chip, Stack, Typography } from '@mui/material'
import ScheduleRoundedIcon from '@mui/icons-material/ScheduleRounded'
import FavoriteBorderRoundedIcon from '@mui/icons-material/FavoriteBorderRounded'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import { FavoriteButton } from '../components/FavoriteButton'
import { sellerLevelLabels, type FavoriteTalent } from '../types'

export function FavoritesPage() {
  const { notify } = useApp()
  const [items, setItems] = useState<FavoriteTalent[]>([])
  const [loading, setLoading] = useState(true)
  const load = useCallback(() => {
    api<{ items: FavoriteTalent[] }>('/api/v1/me/favorites')
      .then((data) => setItems(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '찜한 서비스를 불러오지 못했습니다.', 'error'))
      .finally(() => setLoading(false))
  }, [notify])
  useEffect(() => { load() }, [load])

  return <>
    <Typography variant="h2">찜한 서비스</Typography>
    <Typography color="text.secondary" sx={{ mt: .5, mb: 3 }}>관심 있는 상품을 모아두고 나중에 바로 주문하세요.</Typography>
    <Stack spacing={2}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5, opacity: item.status === 'published' ? 1 : .6 }}>
        <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Typography variant="h4" component={RouterLink} to={`/talents/${item.id}`} sx={{ textDecoration: 'none', color: 'inherit' }}>{item.title}</Typography>
              {item.status !== 'published' && <Chip size="small" label="현재 비공개" />}
            </Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>
              {item.seller.display_name} · {sellerLevelLabels[item.seller.level] ?? item.seller.level}
              {(item.seller.rating_count ?? 0) > 0 ? ` · ★ ${(item.seller.rating ?? 0).toFixed(1)}` : ''}
            </Typography>
            <Typography variant="caption" color="text.secondary">찜한 날짜 {dateTime(item.favorited_at)}</Typography>
          </Box>
          <Stack direction="row" alignItems="center" gap={2} flexShrink={0}>
            <Stack direction="row" spacing={.5} alignItems="center"><ScheduleRoundedIcon sx={{ fontSize: 18, color: 'text.secondary' }} /><Typography variant="body2" color="text.secondary">{item.delivery_days}일</Typography></Stack>
            <Typography variant="h4">{money(item.base_price, item.currency)}</Typography>
            <FavoriteButton talentId={item.id} title={item.title} initial count={item.favorite_count} />
          </Stack>
        </Stack>
      </Card>)}
    </Stack>
    {!loading && items.length === 0 && <Card sx={{ p: 5, textAlign: 'center' }}>
      <FavoriteBorderRoundedIcon color="disabled" sx={{ fontSize: 46 }} />
      <Typography variant="h3" sx={{ mt: 1 }}>아직 찜한 서비스가 없습니다</Typography>
      <Typography color="text.secondary" sx={{ mt: .5 }}>마음에 드는 상품의 하트를 눌러 모아보세요.</Typography>
    </Card>}
  </>
}
