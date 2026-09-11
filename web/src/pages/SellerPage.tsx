import { useEffect, useState } from 'react'
import { Link as RouterLink, useParams } from 'react-router-dom'
import { Alert, Box, Button, Card, CardActionArea, CardContent, Chip, CircularProgress, Container, Divider, Rating, Stack, Typography } from '@mui/material'
import VerifiedRoundedIcon from '@mui/icons-material/VerifiedRounded'
import { api, dateTime, money } from '../api'
import { sellerLevelLabels, type Portfolio, type SellerProfile, type SellerReviewItem, type SellerTalent } from '../types'

// The page a buyer looks at before handing someone a large amount of money.
// Everything here except the headline and biography is derived from completed
// transactions: a seller writes their introduction, not their numbers.
export function SellerPage() {
  const { id } = useParams()
  const [seller, setSeller] = useState<SellerProfile | null>(null)
  const [talents, setTalents] = useState<SellerTalent[]>([])
  const [portfolios, setPortfolios] = useState<Portfolio[]>([])
  const [reviews, setReviews] = useState<SellerReviewItem[]>([])
  const [reviewCursor, setReviewCursor] = useState<string | null>(null)
  const [error, setError] = useState('')

  const loadReviews = (next?: string) => {
    api<{ items: SellerReviewItem[]; next_cursor: string | null }>(`/api/v1/sellers/${id}/reviews?limit=10${next ? `&cursor=${encodeURIComponent(next)}` : ''}`)
      .then((data) => { setReviews((current) => next ? [...current, ...data.items] : data.items); setReviewCursor(data.next_cursor) })
      .catch(() => undefined)
  }

  useEffect(() => {
    api<SellerProfile>(`/api/v1/sellers/${id}`).then(setSeller).catch((cause) => setError(cause instanceof Error ? cause.message : '판매자를 불러오지 못했습니다.'))
    api<{ items: SellerTalent[] }>(`/api/v1/sellers/${id}/talents`).then((data) => setTalents(data.items)).catch(() => undefined)
    api<{ items: Portfolio[] }>(`/api/v1/sellers/${id}/portfolios`).then((data) => setPortfolios(data.items)).catch(() => undefined)
    api<{ items: SellerReviewItem[]; next_cursor: string | null }>(`/api/v1/sellers/${id}/reviews?limit=10`)
      .then((data) => { setReviews(data.items); setReviewCursor(data.next_cursor) }).catch(() => undefined)
  }, [id])

  if (error) return <Container maxWidth="lg" sx={{ py: 7 }}><Alert severity="error">{error}</Alert></Container>
  if (!seller) return <Box sx={{ minHeight: 420, display: 'grid', placeItems: 'center' }}><CircularProgress /></Box>

  // A rate needs something to divide by, and "0% on time" out of no orders is a
  // lie about a new seller rather than a fact about them.
  const onTime = seller.completed_orders > 0 ? Math.round((seller.on_time_orders / seller.completed_orders) * 100) : null

  return <Container maxWidth="lg" sx={{ py: { xs: 4, md: 7 } }}>
    <Card sx={{ p: { xs: 2.5, md: 4 } }}>
      <Stack direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={3}>
        <Box sx={{ minWidth: 0 }}>
          <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
            <Typography variant="h2">{seller.display_name}</Typography>
            {seller.verified && <Chip size="small" color="secondary" icon={<VerifiedRoundedIcon />} label="인증 판매자" />}
            <Chip size="small" variant="outlined" label={sellerLevelLabels[seller.level] ?? seller.level} />
            {!seller.accepting_orders && <Chip size="small" color="warning" label="주문 마감" />}
          </Stack>
          {seller.headline && <Typography sx={{ mt: 1 }}>{seller.headline}</Typography>}
          <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>{dateTime(seller.member_since)} 가입 · 공개 상품 {seller.published_talents}개</Typography>
          {seller.skills.length > 0 && <Stack direction="row" gap={.6} flexWrap="wrap" sx={{ mt: 1.5 }}>{seller.skills.map((skill) => <Chip key={skill} size="small" variant="outlined" label={skill} />)}</Stack>}
        </Box>
        <Stack gap={1} sx={{ minWidth: 220 }}>
          <Box>
            <Stack direction="row" gap={1} alignItems="center"><Rating value={Number(seller.rating)} precision={0.1} readOnly size="small" /><Typography fontWeight={750}>{Number(seller.rating).toFixed(1)}</Typography></Stack>
            <Typography variant="caption" color="text.secondary">후기 {seller.rating_count}건</Typography>
          </Box>
          <Divider />
          <Typography variant="body2">거래 완료 <b>{seller.completed_orders}</b>건</Typography>
          <Typography variant="body2">정시 납품 <b>{onTime === null ? '—' : `${onTime}%`}</b></Typography>
          <Typography variant="body2">진행 중 <b>{seller.active_orders}</b>건</Typography>
        </Stack>
      </Stack>
      {seller.biography && <Typography sx={{ mt: 3, whiteSpace: 'pre-wrap' }}>{seller.biography}</Typography>}
    </Card>

    <Typography variant="h3" sx={{ mt: 5 }}>판매 중인 서비스</Typography>
    {talents.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1 }}>공개된 상품이 없습니다.</Typography> : <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2,1fr)', lg: 'repeat(3,1fr)' }, gap: 2.5, mt: 2 }}>
      {talents.map((talent) => <Card key={talent.id}><CardActionArea component={RouterLink} to={`/talents/${talent.id}`}><CardContent>
        <Chip size="small" label={talent.service_type} sx={{ mb: 1 }} />
        <Typography variant="h4">{talent.title}</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>{talent.summary}</Typography>
        <Typography sx={{ mt: 1.5 }} fontWeight={800}>{money(talent.base_price, talent.currency)}</Typography>
        <Typography variant="caption" color="text.secondary">{talent.delivery_days}일 이내</Typography>
      </CardContent></CardActionArea></Card>)}
    </Box>}

    <Typography variant="h3" sx={{ mt: 5 }}>구매자 후기</Typography>
    {reviews.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1 }}>아직 등록된 후기가 없습니다.</Typography> : <Stack spacing={1.5} sx={{ mt: 2 }}>
      {reviews.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Stack direction="row" justifyContent="space-between" alignItems="center" gap={1} flexWrap="wrap">
          <Stack direction="row" gap={1} alignItems="center"><Rating value={item.average} precision={0.25} readOnly size="small" /><Typography fontWeight={700}>{item.buyer_name}</Typography></Stack>
          <Typography variant="caption" color="text.secondary" component={RouterLink} to={`/talents/${item.talent_id}`} sx={{ textDecoration: 'none', color: 'text.secondary' }}>{item.talent_title} · {dateTime(item.created_at)}</Typography>
        </Stack>
        {item.body && <Typography sx={{ mt: 1, whiteSpace: 'pre-wrap' }}>{item.body}</Typography>}
        {item.seller_reply && <Box sx={{ mt: 1.5, pl: 2, borderLeft: '3px solid', borderColor: 'divider' }}>
          <Typography variant="caption" fontWeight={750}>판매자 답글</Typography>
          <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>{item.seller_reply}</Typography>
        </Box>}
      </Card>)}
    </Stack>}
    {reviewCursor && <Box sx={{ textAlign: 'center', mt: 2 }}><Button onClick={() => loadReviews(reviewCursor)}>후기 더 보기</Button></Box>}

    {portfolios.length > 0 && <>
      <Typography variant="h3" sx={{ mt: 5 }}>포트폴리오</Typography>
      <Stack spacing={2} sx={{ mt: 2 }}>{portfolios.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Typography variant="h4">{item.title}</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>{item.description}</Typography>
        <Stack direction="row" gap={.6} flexWrap="wrap" sx={{ mt: 1.5 }}>{item.tags.map((tag) => <Chip key={tag} size="small" variant="outlined" label={tag} />)}</Stack>
      </Card>)}</Stack>
    </>}
  </Container>
}
