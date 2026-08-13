import { FormEvent, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, CardActionArea, CardContent, Chip, Container, InputAdornment, Skeleton, Stack, TextField, Typography } from '@mui/material'
import SearchRoundedIcon from '@mui/icons-material/SearchRounded'
import AutoAwesomeRoundedIcon from '@mui/icons-material/AutoAwesomeRounded'
import ScheduleRoundedIcon from '@mui/icons-material/ScheduleRounded'
import VerifiedRoundedIcon from '@mui/icons-material/VerifiedRounded'
import ArrowForwardRoundedIcon from '@mui/icons-material/ArrowForwardRounded'
import { Link as RouterLink } from 'react-router-dom'
import { api, money } from '../api'
import type { Talent } from '../types'

const categories = ['디자인', '개발·IT', '번역·통역', '문서·콘텐츠', '마케팅', '영상·사진', '컨설팅', '교육']

export function MarketplacePage() {
  const [query, setQuery] = useState('')
  const [talents, setTalents] = useState<Talent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const search = async (value = query) => { setLoading(true); setError(''); try { setTalents((await api<{ items: Talent[] }>(`/api/v1/talents?q=${encodeURIComponent(value)}`)).items) } catch (cause) { setError(cause instanceof Error ? cause.message : '검색하지 못했습니다.') } finally { setLoading(false) } }
  useEffect(() => { search('') }, [])
  const submit = (event: FormEvent) => { event.preventDefault(); search() }
  return <>
    <Box sx={{ bgcolor: '#eef3ff', backgroundImage: 'radial-gradient(circle at 12% 20%, rgba(10,161,129,.12), transparent 26%), radial-gradient(circle at 85% 5%, rgba(53,109,243,.17), transparent 30%)', borderBottom: '1px solid', borderColor: 'divider' }}>
      <Container maxWidth="lg" sx={{ py: { xs: 7, md: 11 }, textAlign: 'center' }}>
        <Chip icon={<AutoAwesomeRoundedIcon />} label="사람과 AI의 전문 서비스를 한곳에서" color="primary" variant="outlined" sx={{ bgcolor: 'rgba(255,255,255,.7)' }} />
        <Typography variant="h1" sx={{ mt: 2.5, fontSize: 'clamp(2.25rem, 5vw, 4rem)' }}>어떤 일을 해결하고 싶나요?</Typography>
        <Typography color="text.secondary" sx={{ mt: 2, fontSize: '1.1rem' }}>필요한 결과, 예산과 일정을 자연스럽게 적어보세요.</Typography>
        <Box component="form" onSubmit={submit} sx={{ mt: 4, mx: 'auto', maxWidth: 820 }}>
          <TextField fullWidth value={query} onChange={(e) => setQuery(e.target.value)} placeholder="예: 관리자 페이지가 포함된 회사 홈페이지를 300만원 안에서 만들고 싶어요" slotProps={{ input: { startAdornment: <InputAdornment position="start"><SearchRoundedIcon color="primary" /></InputAdornment>, endAdornment: <Button type="submit" variant="contained" sx={{ mr: -.7 }}>찾기</Button> } }} sx={{ '& .MuiOutlinedInput-root': { bgcolor: 'white', borderRadius: 3, py: .75, boxShadow: '0 16px 40px rgba(40,72,120,.12)' } }} />
        </Box>
        <Stack direction="row" useFlexGap flexWrap="wrap" justifyContent="center" gap={1} sx={{ mt: 3 }}>{categories.map((item) => <Chip key={item} label={item} onClick={() => { setQuery(item); search(item) }} sx={{ bgcolor: 'rgba(255,255,255,.78)' }} />)}</Stack>
      </Container>
    </Box>
    <Container maxWidth="xl" sx={{ py: { xs: 5, md: 7 } }}>
      <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={1} sx={{ mb: 3 }}>
        <Box><Typography variant="h2">추천 서비스</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>{query ? `“${query}” 검색 결과` : '지금 살펴볼 만한 전문 서비스입니다.'}</Typography></Box>
        <Button endIcon={<ArrowForwardRoundedIcon />}>전체 보기</Button>
      </Stack>
      {error && <Alert severity="error" sx={{ mb: 3 }}>{error}</Alert>}
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2,1fr)', lg: 'repeat(3,1fr)', xl: 'repeat(4,1fr)' }, gap: 2.5 }}>
        {loading ? Array.from({ length: 8 }).map((_, index) => <Skeleton key={index} variant="rounded" height={330} sx={{ borderRadius: 3 }} />) : talents.map((talent) => <TalentCard key={talent.id} talent={talent} />)}
      </Box>
      {!loading && talents.length === 0 && <Card sx={{ py: 8, textAlign: 'center', bgcolor: '#fbfcff' }}><Typography variant="h3">아직 공개된 서비스가 없어요</Typography><Typography color="text.secondary" sx={{ mt: 1 }}>판매자가 상품을 공개하면 이곳에서 바로 찾을 수 있습니다.</Typography></Card>}
    </Container>
  </>
}

function TalentCard({ talent }: { talent: Talent }) {
  const gradient = talent.service_type === 'AI' ? 'linear-gradient(135deg,#6a4df5,#a16eff)' : talent.service_type === 'HYBRID' ? 'linear-gradient(135deg,#087f69,#4bc9a8)' : 'linear-gradient(135deg,#1a4b9c,#4d83ef)'
  return <Card sx={{ overflow: 'hidden', height: '100%' }}><CardActionArea component={RouterLink} to={`/talents/${talent.id}`} sx={{ height: '100%', alignItems: 'stretch' }}>
    <Box sx={{ height: 142, background: gradient, color: 'white', p: 2.5, display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}>
      <Chip label={talent.service_type} size="small" sx={{ alignSelf: 'flex-start', bgcolor: 'rgba(255,255,255,.18)', color: 'white', backdropFilter: 'blur(8px)' }} />
      <Typography variant="h3" sx={{ color: 'white', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' }}>{talent.title}</Typography>
    </Box>
    <CardContent sx={{ display: 'flex', flexDirection: 'column', minHeight: 190 }}>
      <Stack direction="row" alignItems="center" spacing={.7}><VerifiedRoundedIcon sx={{ fontSize: 18, color: 'secondary.main' }} /><Typography variant="body2" fontWeight={700}>{talent.seller.display_name}</Typography><Typography variant="caption" color="text.secondary">{talent.seller.level}</Typography></Stack>
      <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' }}>{talent.summary || '전문가가 제공하는 맞춤 서비스입니다.'}</Typography>
      <Stack direction="row" justifyContent="space-between" alignItems="end" sx={{ mt: 'auto', pt: 2 }}><Stack direction="row" spacing={.7} alignItems="center"><ScheduleRoundedIcon sx={{ fontSize: 18, color: 'text.secondary' }} /><Typography variant="body2" color="text.secondary">{talent.delivery_days}일</Typography></Stack><Box textAlign="right"><Typography variant="caption" color="text.secondary">시작가</Typography><Typography variant="h4">{money(talent.base_price, talent.currency)}</Typography></Box></Stack>
    </CardContent>
  </CardActionArea></Card>
}
