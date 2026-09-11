import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, CardActionArea, CardContent, Chip, Container, InputAdornment, MenuItem, Skeleton, Stack, TextField, Typography } from '@mui/material'
import SearchRoundedIcon from '@mui/icons-material/SearchRounded'
import AutoAwesomeRoundedIcon from '@mui/icons-material/AutoAwesomeRounded'
import ScheduleRoundedIcon from '@mui/icons-material/ScheduleRounded'
import VerifiedRoundedIcon from '@mui/icons-material/VerifiedRounded'
import { Link as RouterLink, useSearchParams } from 'react-router-dom'
import { api, money } from '../api'
import StarRateRoundedIcon from '@mui/icons-material/StarRateRounded'
import { sellerLevelLabels, type Category, type Talent } from '../types'
import { FavoriteButton } from '../components/FavoriteButton'


export function MarketplacePage() {
  const [query, setQuery] = useState('')
  const [talents, setTalents] = useState<Talent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  // The chips used to be eight hardcoded words fed into the text search, so
  // pressing one found listings that merely mention it. They come from the
  // category tree now and filter by it.
  const [categories, setCategories] = useState<Category[]>([])
  // A category is part of the address so a chosen category can be linked to and
  // survives the back button.
  const [params, setParams] = useSearchParams()
  const category = params.get('category') ?? ''
  const setCategory = (slug: string) => setParams(slug ? { category: slug } : {}, { replace: true })
  useEffect(() => { api<{ items: Category[] }>('/api/v1/categories').then((data) => setCategories(data.items.filter((item) => !item.parent_id))).catch(() => undefined) }, [])
  const [filters, setFilters] = useState({ price_min: '', price_max: '', max_delivery_days: '', service_type: '', sort: '' })
  const [hasMore, setHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const search = useCallback(async (value: string, slug: string, active: typeof filters, offset = 0) => {
    if (offset > 0) setLoadingMore(true); else setLoading(true)
    setError('')
    const params = new URLSearchParams({ q: value, limit: '24' })
    if (slug) params.set('category', slug)
    if (offset) params.set('offset', String(offset))
    for (const [key, entry] of Object.entries(active)) if (entry) params.set(key, entry)
    try {
      const data = await api<{ items: Talent[]; has_more: boolean }>(`/api/v1/talents?${params.toString()}`)
      setTalents((current) => offset ? [...current, ...data.items] : data.items)
      setHasMore(data.has_more)
    } catch (cause) { setError(cause instanceof Error ? cause.message : '검색에 실패했습니다.') }
    finally { setLoading(false); setLoadingMore(false) }
  }, [])
  useEffect(() => { search(query, category, filters) }, [search, category, filters])
  const submit = (event: FormEvent) => { event.preventDefault(); search(query, category, filters) }
  return <>
    <Box sx={{ bgcolor: '#eef3ff', backgroundImage: 'radial-gradient(circle at 12% 20%, rgba(10,161,129,.12), transparent 26%), radial-gradient(circle at 85% 5%, rgba(53,109,243,.17), transparent 30%)', borderBottom: '1px solid', borderColor: 'divider' }}>
      <Container maxWidth="lg" sx={{ py: { xs: 7, md: 11 }, textAlign: 'center' }}>
        <Chip icon={<AutoAwesomeRoundedIcon />} label="사람과 AI의 전문 서비스를 한곳에서" color="primary" variant="outlined" sx={{ bgcolor: 'rgba(255,255,255,.7)' }} />
        <Typography variant="h1" sx={{ mt: 2.5, fontSize: 'clamp(2.25rem, 5vw, 4rem)' }}>어떤 일을 해결하고 싶나요?</Typography>
        <Typography color="text.secondary" sx={{ mt: 2, fontSize: '1.1rem' }}>필요한 결과, 예산과 일정을 자연스럽게 적어보세요.</Typography>
        <Box component="form" onSubmit={submit} sx={{ mt: 4, mx: 'auto', maxWidth: 820 }}>
          <TextField fullWidth value={query} onChange={(e) => setQuery(e.target.value)} placeholder="예: 관리자 페이지가 포함된 회사 홈페이지를 300만원 안에서 만들고 싶어요" slotProps={{ input: { startAdornment: <InputAdornment position="start"><SearchRoundedIcon color="primary" /></InputAdornment>, endAdornment: <Button type="submit" variant="contained" sx={{ mr: -.7 }}>찾기</Button> } }} sx={{ '& .MuiOutlinedInput-root': { bgcolor: 'white', borderRadius: 3, py: .75, boxShadow: '0 16px 40px rgba(40,72,120,.12)' } }} />
        </Box>
        <Stack direction="row" useFlexGap flexWrap="wrap" justifyContent="center" gap={1} sx={{ mt: 3 }}><Chip label="전체" color={category === '' ? 'primary' : 'default'} variant={category === '' ? 'filled' : 'outlined'} onClick={() => setCategory('')} sx={{ bgcolor: category === '' ? undefined : 'rgba(255,255,255,.78)' }} />{categories.map((item) => <Chip key={item.slug} label={item.name} color={category === item.slug ? 'primary' : 'default'} variant={category === item.slug ? 'filled' : 'outlined'} onClick={() => setCategory(category === item.slug ? '' : item.slug)} sx={{ bgcolor: category === item.slug ? undefined : 'rgba(255,255,255,.78)' }} />)}</Stack>
      </Container>
    </Box>
    <Container maxWidth="xl" sx={{ py: { xs: 5, md: 7 } }}>
      <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={1} sx={{ mb: 3 }}>
        <Box><Typography variant="h2">추천 서비스</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>{query ? `“${query}” 검색 결과` : '지금 살펴볼 만한 전문 서비스입니다.'}</Typography></Box>
        <TextField select size="small" label="정렬" value={filters.sort} onChange={(event) => setFilters({ ...filters, sort: event.target.value })} sx={{ minWidth: 160 }}>
          <MenuItem value="">추천순</MenuItem><MenuItem value="price_asc">가격 낮은순</MenuItem><MenuItem value="price_desc">가격 높은순</MenuItem><MenuItem value="delivery">납기 빠른순</MenuItem><MenuItem value="rating">평점순</MenuItem><MenuItem value="newest">최신순</MenuItem>
        </TextField>
      </Stack>
      <Stack direction="row" useFlexGap flexWrap="wrap" gap={1.5} sx={{ mb: 3 }}>
        <TextField size="small" type="number" label="최소 가격" value={filters.price_min} onChange={(event) => setFilters({ ...filters, price_min: event.target.value })} sx={{ width: 140 }} />
        <TextField size="small" type="number" label="최대 가격" value={filters.price_max} onChange={(event) => setFilters({ ...filters, price_max: event.target.value })} sx={{ width: 140 }} />
        <TextField size="small" select label="납기" value={filters.max_delivery_days} onChange={(event) => setFilters({ ...filters, max_delivery_days: event.target.value })} sx={{ width: 150 }}>
          <MenuItem value="">전체</MenuItem><MenuItem value="1">1일 이내</MenuItem><MenuItem value="3">3일 이내</MenuItem><MenuItem value="7">7일 이내</MenuItem><MenuItem value="14">14일 이내</MenuItem>
        </TextField>
        <TextField size="small" select label="제공 방식" value={filters.service_type} onChange={(event) => setFilters({ ...filters, service_type: event.target.value })} sx={{ width: 150 }}>
          <MenuItem value="">전체</MenuItem><MenuItem value="HUMAN">사람</MenuItem><MenuItem value="AI">AI</MenuItem><MenuItem value="HYBRID">사람+AI</MenuItem>
        </TextField>
        {(filters.price_min || filters.price_max || filters.max_delivery_days || filters.service_type) && <Button onClick={() => setFilters({ price_min: '', price_max: '', max_delivery_days: '', service_type: '', sort: filters.sort })}>조건 지우기</Button>}
      </Stack>
      {error && <Alert severity="error" sx={{ mb: 3 }}>{error}</Alert>}
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2,1fr)', lg: 'repeat(3,1fr)', xl: 'repeat(4,1fr)' }, gap: 2.5 }}>
        {loading ? Array.from({ length: 8 }).map((_, index) => <Skeleton key={index} variant="rounded" height={330} sx={{ borderRadius: 3 }} />) : talents.map((talent) => <TalentCard key={talent.id} talent={talent} />)}
      </Box>
      {hasMore && <Box sx={{ textAlign: 'center', mt: 4 }}><Button variant="outlined" onClick={() => search(query, category, filters, talents.length)} disabled={loadingMore}>{loadingMore ? '불러오는 중…' : '더 보기'}</Button></Box>}
      {!loading && talents.length === 0 && <Card sx={{ py: 8, textAlign: 'center', bgcolor: '#fbfcff' }}><Typography variant="h3">아직 공개된 서비스가 없어요</Typography><Typography color="text.secondary" sx={{ mt: 1 }}>판매자가 상품을 공개하면 이곳에서 바로 찾을 수 있습니다.</Typography></Card>}
    </Container>
  </>
}

function TalentCard({ talent }: { talent: Talent }) {
  const gradient = talent.service_type === 'AI' ? 'linear-gradient(135deg,#6a4df5,#a16eff)' : talent.service_type === 'HYBRID' ? 'linear-gradient(135deg,#087f69,#4bc9a8)' : 'linear-gradient(135deg,#1a4b9c,#4d83ef)'
  return <Card sx={{ overflow: 'hidden', height: '100%', position: 'relative' }}>
    <Box sx={{ position: 'absolute', top: 8, right: 8, zIndex: 2, bgcolor: 'rgba(255,255,255,.85)', borderRadius: 999, backdropFilter: 'blur(8px)' }}>
      <FavoriteButton talentId={talent.id} title={talent.title} initial={talent.favorited} count={talent.favorite_count} size="small" />
    </Box>
    <CardActionArea component={RouterLink} to={`/talents/${talent.id}`} sx={{ height: '100%', alignItems: 'stretch' }}>
    <Box sx={{ height: 142, background: gradient, color: 'white', p: 2.5, display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}>
      <Stack direction="row" gap={.6}><Chip label={talent.service_type} size="small" sx={{ alignSelf: 'flex-start', bgcolor: 'rgba(255,255,255,.18)', color: 'white', backdropFilter: 'blur(8px)' }} />{talent.accepting_orders === false && <Chip label="주문 마감" size="small" sx={{ bgcolor: 'rgba(0,0,0,.35)', color: 'white' }} />}</Stack>
      <Typography variant="h3" sx={{ color: 'white', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' }}>{talent.title}</Typography>
    </Box>
    <CardContent sx={{ display: 'flex', flexDirection: 'column', minHeight: 190 }}>
      <Stack direction="row" alignItems="center" spacing={.7} flexWrap="wrap">
        <VerifiedRoundedIcon sx={{ fontSize: 18, color: 'secondary.main' }} />
        <Typography variant="body2" fontWeight={700}>{talent.seller.display_name}</Typography>
        <Chip size="small" variant="outlined" label={sellerLevelLabels[talent.seller.level] ?? talent.seller.level} sx={{ height: 20, fontSize: '.7rem' }} />
        {(talent.seller.rating_count ?? 0) > 0 && <Stack direction="row" alignItems="center"><StarRateRoundedIcon sx={{ fontSize: 16, color: '#f5a623' }} /><Typography variant="caption" fontWeight={700}>{(talent.seller.rating ?? 0).toFixed(1)}</Typography><Typography variant="caption" color="text.secondary">({talent.seller.rating_count})</Typography></Stack>}
      </Stack>
      <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' }}>{talent.summary || '전문가가 제공하는 맞춤 서비스입니다.'}</Typography>
      <Stack direction="row" justifyContent="space-between" alignItems="end" sx={{ mt: 'auto', pt: 2 }}><Stack direction="row" spacing={.7} alignItems="center"><ScheduleRoundedIcon sx={{ fontSize: 18, color: 'text.secondary' }} /><Typography variant="body2" color="text.secondary">{talent.delivery_days}일</Typography></Stack><Box textAlign="right"><Typography variant="caption" color="text.secondary">시작가</Typography><Typography variant="h4">{money(talent.base_price, talent.currency)}</Typography></Box></Stack>
    </CardContent>
  </CardActionArea></Card>
}
