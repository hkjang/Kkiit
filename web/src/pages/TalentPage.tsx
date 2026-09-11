import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams, Link as RouterLink } from 'react-router-dom'
import { Alert, Box, Button, Card, Checkbox, Chip, CircularProgress, Container, Dialog, DialogActions, DialogContent, DialogTitle, Divider, FormControlLabel, MenuItem, Rating, Stack, TextField, Typography } from '@mui/material'
import ShoppingBagOutlinedIcon from '@mui/icons-material/ShoppingBagOutlined'
import ScheduleRoundedIcon from '@mui/icons-material/ScheduleRounded'
import ReplayRoundedIcon from '@mui/icons-material/ReplayRounded'
import VerifiedRoundedIcon from '@mui/icons-material/VerifiedRounded'
import StarRateRoundedIcon from '@mui/icons-material/StarRateRounded'
import { api, dateTime, money } from '../api'
import { useApp } from '../App'
import { sellerLevelLabels, type Organization, type Portfolio, type ReviewSummary, type SellerTrust, type TalentReview } from '../types'
import { FavoriteButton } from '../components/FavoriteButton'
import { ReportDialog } from '../components/ReportDialog'

type Package = { id: string; package_type: string; name: string; description: string; price: number; delivery_days: number; revision_count: number; features: unknown[] }
type Requirement = { id: string; label: string; help_text: string; field_type: string; required: boolean; options: unknown[] }
type TalentOption = { id: string; name: string; description: string; price: number; additional_days: number }
type Detail = { category?: { slug: string; name: string } | null; id: string; title: string; summary: string; description: string; service_type: string; base_price: number; currency: string; delivery_days: number; revision_count: number; tags: string[]; deliverables: unknown[]; seller: SellerTrust; quality_score?: number; favorite_count?: number; favorited?: boolean; accepting_orders?: boolean; packages: Package[]; options?: TalentOption[]; requirements: Requirement[] }

export function TalentPage() {
  const { id } = useParams(); const { me, notify } = useApp(); const navigate = useNavigate(); const [talent, setTalent] = useState<Detail | null>(null); const [selectedPackage, setSelectedPackage] = useState(''); const [requirements, setRequirements] = useState<Record<string, string>>({}); const [submitting, setSubmitting] = useState(false); const [error, setError] = useState('')
  const [portfolios, setPortfolios] = useState<Portfolio[]>([])
  const [replyingTo, setReplyingTo] = useState<string | null>(null); const [replyBody, setReplyBody] = useState('')
  const [askOpen, setAskOpen] = useState(false); const [askBody, setAskBody] = useState(''); const [askBusy, setAskBusy] = useState(false)
  const [reviews, setReviews] = useState<TalentReview[]>([]); const [reviewSummary, setReviewSummary] = useState<ReviewSummary | null>(null); const [reviewCursor, setReviewCursor] = useState<string | null>(null)
  const [organizations, setOrganizations] = useState<Organization[]>([]); const [organizationId, setOrganizationId] = useState('')
  const [chosenOptions, setChosenOptions] = useState<string[]>([])
  const [couponCode, setCouponCode] = useState(''); const [coupon, setCoupon] = useState<{ code: string; name: string; discount_amount: number; payable_amount: number } | null>(null); const [couponError, setCouponError] = useState('')
  // Reviews load on their own so a slow or empty review page never delays the
  // thing the buyer came for.
  const loadReviews = useCallback((next?: string) => { api<{ items: TalentReview[]; next_cursor: string | null; summary?: ReviewSummary }>(`/api/v1/talents/${id}/reviews?limit=20${next ? `&cursor=${encodeURIComponent(next)}` : ''}`).then((data) => { setReviews((current) => next ? [...current, ...data.items] : data.items); setReviewCursor(data.next_cursor); if (data.summary) setReviewSummary(data.summary) }).catch(() => undefined) }, [id])
  useEffect(() => { loadReviews() }, [loadReviews])
  const submitReply = async (reviewId: string) => {
    try { await api(`/api/v1/reviews/${reviewId}/reply`, { method: 'POST', body: JSON.stringify({ body: replyBody.trim() }) }); setReplyingTo(null); setReplyBody(''); loadReviews(); notify('답글을 남겼습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '답글을 남기지 못했습니다.', 'error') }
  }
  const removeReply = async (reviewId: string) => {
    if (!window.confirm('답글을 삭제할까요?')) return
    try { await api(`/api/v1/reviews/${reviewId}/reply`, { method: 'DELETE' }); loadReviews(); notify('답글을 삭제했습니다.', 'success') }
    catch (cause) { notify(cause instanceof Error ? cause.message : '답글을 삭제하지 못했습니다.', 'error') }
  }
  // Recording the view is best effort and never blocks the page.
  useEffect(() => { api(`/api/v1/talents/${id}/view`, { method: 'POST' }).catch(() => undefined) }, [id])
  useEffect(() => { if (me) api<{ items: Organization[] }>('/api/v1/me/organizations').then((data) => setOrganizations(data.items)).catch(() => undefined) }, [me])
  useEffect(() => { api<Detail>(`/api/v1/talents/${id}`).then((data) => { setTalent(data); setSelectedPackage(data.packages[0]?.id ?? ''); api<{ items: Portfolio[] }>(`/api/v1/sellers/${data.seller.id}/portfolios`).then((list) => setPortfolios(list.items)).catch(() => undefined) }).catch((e) => setError(e.message)) }, [id])
  if (error) return <Container maxWidth="lg" sx={{ py: 7 }}><Alert severity="error">{error}</Alert></Container>
  if (!talent) return <Box sx={{ minHeight: 420, display: 'grid', placeItems: 'center' }}><CircularProgress /></Box>
  const pkg = talent.packages.find((item) => item.id === selectedPackage)
  const order = async () => { if (!me) { navigate('/login', { state: { from: `/talents/${id}` } }); return }; setSubmitting(true); try { const result = await api<{ id: string }>('/api/v1/orders', { method: 'POST', body: JSON.stringify({ talent_id: talent.id, package_id: selectedPackage || undefined, requirements, options: chosenOptions.map((id) => ({ id })), coupon_code: coupon?.code || undefined, organization_id: organizationId || undefined }) }); notify('주문을 만들었습니다.', 'success'); navigate(`/orders/${result.id}`) } catch (cause) { notify(cause instanceof Error ? cause.message : '주문하지 못했습니다.', 'error') } finally { setSubmitting(false) } }
  const applyCoupon = async () => { if (!me) { navigate('/login', { state: { from: `/talents/${id}` } }); return }; setCouponError(''); try { const result = await api<{ code: string; name: string; discount_amount: number; payable_amount: number }>('/api/v1/coupons/preview', { method: 'POST', body: JSON.stringify({ code: couponCode, talent_id: talent.id, package_id: selectedPackage || undefined }) }); setCoupon(result) } catch (cause) { setCoupon(null); setCouponError(cause instanceof Error ? cause.message : '쿠폰을 적용하지 못했습니다.') } }
  // The seller reads their own listing like anyone else, which is where the
  // reviews are, so that is where they answer them.
  const isSeller = Boolean(me && me.id === talent.seller.id)
  const openAsk = () => {
    if (!me) { navigate('/login', { state: { from: `/talents/${id}` } }); return }
    setAskOpen(true)
  }
  const ask = async () => {
    const body = askBody.trim()
    if (body.length < 2) return
    setAskBusy(true)
    try {
      await api(`/api/v1/talents/${id}/inquiries`, { method: 'POST', body: JSON.stringify({ body }) })
      setAskOpen(false); setAskBody('')
      notify('문의를 보냈습니다. 프로필 → 상품 문의에서 답변을 확인하세요.', 'success')
    } catch (cause) { notify(cause instanceof Error ? cause.message : '문의를 보내지 못했습니다.', 'error') }
    finally { setAskBusy(false) }
  }
  const availableOptions = talent.options ?? []
  const optionTotal = availableOptions.filter((item) => chosenOptions.includes(item.id)).reduce((sum, item) => sum + item.price, 0)
  const listPrice = (pkg?.price ?? talent.base_price) + optionTotal
  return <Container maxWidth="lg" sx={{ py: { xs: 4, md: 7 } }}><Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'minmax(0,1fr) 360px' }, gap: 4, alignItems: 'start' }}>
    <Box><Stack direction="row" gap={1} sx={{ mb: 2 }}><Chip label={talent.service_type} color="primary" />{talent.category && <Chip label={talent.category.name} variant="outlined" component={RouterLink} to={`/?category=${talent.category.slug}`} clickable />}<Stack direction="row" alignItems="center" gap={.5} flexWrap="wrap"><VerifiedRoundedIcon color="secondary" fontSize="small" /><Typography fontWeight={700} component={RouterLink} to={`/sellers/${talent.seller.id}`} sx={{ textDecoration: 'none', color: 'inherit', '&:hover': { textDecoration: 'underline' } }}>{talent.seller.display_name}</Typography><Chip size="small" variant="outlined" label={sellerLevelLabels[talent.seller.level] ?? talent.seller.level} />{(talent.seller.rating_count ?? 0) > 0 && <Stack direction="row" alignItems="center"><StarRateRoundedIcon sx={{ fontSize: 18, color: '#f5a623' }} /><Typography fontWeight={700}>{(talent.seller.rating ?? 0).toFixed(1)}</Typography><Typography variant="body2" color="text.secondary">&nbsp;리뷰 {talent.seller.rating_count}건</Typography></Stack>}</Stack></Stack><Stack direction="row" justifyContent="space-between" alignItems="start" gap={1}><Typography variant="h1" sx={{ fontSize: 'clamp(2rem,4vw,3.5rem)' }}>{talent.title}</Typography><Stack direction="row" alignItems="center"><FavoriteButton talentId={talent.id} title={talent.title} initial={talent.favorited} count={talent.favorite_count} showCount /><ReportDialog resourceType="talent" resourceId={talent.id} /></Stack></Stack><Typography sx={{ mt: 2, fontSize: '1.1rem' }} color="text.secondary">{talent.summary}</Typography><Stack direction="row" gap={1} flexWrap="wrap" sx={{ mt: 3 }}>{talent.tags.map((tag) => <Chip key={tag} label={tag} variant="outlined" />)}</Stack><Divider sx={{ my: 4 }} /><Typography variant="h3">서비스 소개</Typography><Typography sx={{ mt: 2, whiteSpace: 'pre-wrap' }}>{talent.description}</Typography>{portfolios.length > 0 && <><Typography variant="h3" sx={{ mt: 4 }}>판매자 포트폴리오</Typography><Stack spacing={2} sx={{ mt: 2 }}>{portfolios.map((item) => <Card key={item.id} variant="outlined" sx={{ p: 2.5, boxShadow: 'none' }}><Typography variant="h4">{item.title}</Typography>{item.description && <Typography color="text.secondary" sx={{ mt: .5, whiteSpace: 'pre-wrap' }}>{item.description}</Typography>}{item.media.length > 0 && <Stack direction="row" gap={1} flexWrap="wrap" sx={{ mt: 1.5 }}>{item.media.filter((media) => media.url).map((media, index) => <Box key={index} component="img" src={media.url} alt={media.name ?? item.title} loading="lazy" sx={{ width: 148, height: 100, objectFit: 'cover', borderRadius: 2, border: '1px solid', borderColor: 'divider' }} />)}</Stack>}<Stack direction="row" gap={.6} flexWrap="wrap" sx={{ mt: 1.5 }}>{item.tags.map((tag) => <Chip key={tag} size="small" variant="outlined" label={tag} />)}</Stack></Card>)}</Stack></>}<Typography variant="h3" sx={{ mt: 4 }}>주문 전 정보</Typography>{talent.requirements.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1 }}>추가로 입력할 필수 정보가 없습니다.</Typography> : <Stack spacing={2} sx={{ mt: 2 }}>{talent.requirements.map((item) => <TextField key={item.id} required={item.required} label={item.label} helperText={item.help_text} value={requirements[item.id] ?? ''} onChange={(e) => setRequirements({ ...requirements, [item.id]: e.target.value })} multiline={item.field_type === 'textarea'} minRows={item.field_type === 'textarea' ? 3 : undefined} />)}</Stack>}
      <Typography variant="h3" sx={{ mt: 4 }}>구매자 후기</Typography>
      {reviewSummary && reviewSummary.count > 0 && <Card sx={{ p: 2.5, mt: 2 }}>
        <Stack direction={{ xs: 'column', sm: 'row' }} gap={3} alignItems={{ sm: 'center' }}>
          <Box sx={{ textAlign: 'center', minWidth: 120 }}>
            <Typography variant="h2">{Number(reviewSummary.average ?? 0).toFixed(1)}</Typography>
            <Rating value={Number(reviewSummary.average ?? 0)} precision={0.1} readOnly size="small" />
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>{reviewSummary.count}건</Typography>
          </Box>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            {[5, 4, 3, 2, 1].map((star) => { const value = reviewSummary.distribution?.[star - 1] ?? 0; const share = reviewSummary.count ? (value / reviewSummary.count) * 100 : 0; return <Stack key={star} direction="row" gap={1} alignItems="center"><Typography variant="caption" sx={{ width: 24 }}>{star}점</Typography><Box sx={{ flex: 1, height: 8, borderRadius: 4, bgcolor: 'action.hover' }}><Box sx={{ width: `${share}%`, height: 1, borderRadius: 4, bgcolor: 'primary.main' }} /></Box><Typography variant="caption" color="text.secondary" sx={{ width: 32, textAlign: 'right' }}>{value}</Typography></Stack> })}
            {reviewSummary.repurchase_rate != null && <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>재의뢰 의향 {Math.round(Number(reviewSummary.repurchase_rate) * 100)}%</Typography>}
          </Box>
        </Stack>
      </Card>}
      {reviews.length === 0 ? <Typography color="text.secondary" sx={{ mt: 1 }}>아직 등록된 후기가 없습니다.</Typography> : <Stack spacing={1.5} sx={{ mt: 2 }}>
        {reviews.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
          <Stack direction="row" justifyContent="space-between" alignItems="center" gap={1} flexWrap="wrap">
            <Stack direction="row" gap={1} alignItems="center"><Rating value={item.average} precision={0.25} readOnly size="small" /><Typography fontWeight={700}>{item.buyer_name}</Typography>{item.repurchase && <Chip size="small" variant="outlined" label="재의뢰 의향" />}</Stack>
            <Typography variant="caption" color="text.secondary">{dateTime(item.created_at)}</Typography>
          </Stack>
          {item.body && <Typography sx={{ mt: 1, whiteSpace: 'pre-wrap' }}>{item.body}</Typography>}
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1 }}>품질 {item.quality} · 소통 {item.communication} · 납기 {item.timeliness} · 전문성 {item.professionalism}</Typography>
          {item.seller_reply && <Box sx={{ mt: 1.5, pl: 2, borderLeft: '3px solid', borderColor: 'divider' }}>
            <Typography variant="caption" fontWeight={750}>판매자 답글{item.seller_replied_at ? ` · ${dateTime(item.seller_replied_at)}` : ''}</Typography>
            <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>{item.seller_reply}</Typography>
          </Box>}
          {isSeller && <Box sx={{ mt: 1.5 }}>
            {replyingTo === item.id
              ? <Stack direction="row" gap={1} alignItems="flex-start">
                  <TextField size="small" fullWidth multiline minRows={2} value={replyBody} onChange={(event) => setReplyBody(event.target.value)} placeholder="구매자와 앞으로 올 구매자에게 남기는 답글" />
                  <Stack gap={.5}>
                    <Button size="small" variant="contained" onClick={() => submitReply(item.id)} disabled={replyBody.trim().length < 2}>등록</Button>
                    <Button size="small" onClick={() => setReplyingTo(null)}>취소</Button>
                  </Stack>
                </Stack>
              : <Stack direction="row" gap={1}>
                  <Button size="small" onClick={() => { setReplyingTo(item.id); setReplyBody(item.seller_reply ?? '') }}>{item.seller_reply ? '답글 수정' : '답글 남기기'}</Button>
                  {item.seller_reply && <Button size="small" color="error" onClick={() => removeReply(item.id)}>답글 삭제</Button>}
                </Stack>}
          </Box>}
        </Card>)}
      </Stack>}
      {reviewCursor && <Box sx={{ textAlign: 'center', mt: 2 }}><Button onClick={() => loadReviews(reviewCursor)}>후기 더 보기</Button></Box>}
    </Box>
    <Card sx={{ p: 3, position: { md: 'sticky' }, top: 96 }}><Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 2 }}><Typography variant="h3">주문 구성</Typography>{talent.quality_score != null && <Chip size="small" color="secondary" variant="outlined" label={`품질 ${Number(talent.quality_score).toFixed(0)}점`} />}</Stack>{talent.packages.length > 0 && <TextField select fullWidth label="패키지" value={selectedPackage} onChange={(e) => setSelectedPackage(e.target.value)} sx={{ mt: 2 }}>{talent.packages.map((item) => <MenuItem key={item.id} value={item.id}>{item.name} · {money(item.price, talent.currency)}</MenuItem>)}</TextField>}<Stack spacing={1.3} sx={{ my: 3 }}><Stack direction="row" gap={1}><ScheduleRoundedIcon color="action" /><Typography>{pkg?.delivery_days ?? talent.delivery_days}일 납기</Typography></Stack><Stack direction="row" gap={1}><ReplayRoundedIcon color="action" /><Typography>무료 수정 {pkg?.revision_count ?? talent.revision_count}회</Typography></Stack></Stack>{availableOptions.length > 0 && <Box sx={{ mt: 2 }}><Typography variant="body2" fontWeight={700}>추가 옵션</Typography>{availableOptions.map((item) => <FormControlLabel key={item.id} sx={{ display: 'flex', ml: 0, justifyContent: 'space-between' }} labelPlacement="start" control={<Checkbox checked={chosenOptions.includes(item.id)} onChange={(e) => { setCoupon(null); setChosenOptions(e.target.checked ? [...chosenOptions, item.id] : chosenOptions.filter((id) => id !== item.id)) }} />} label={<Stack><Typography variant="body2" fontWeight={650}>{item.name}</Typography><Typography variant="caption" color="text.secondary">+{money(item.price, talent.currency)}{item.additional_days > 0 ? ` · 납기 +${item.additional_days}일` : ''}</Typography></Stack>} />)}</Box>}<Divider sx={{ mt: 2 }} />{organizations.length > 0 && <TextField select fullWidth size="small" label="주문 명의" value={organizationId} onChange={(e) => setOrganizationId(e.target.value)} sx={{ mt: 2.5 }} helperText={organizationId ? '조직 예산에서 차감됩니다.' : '개인 명의로 주문합니다.'}><MenuItem value="">개인</MenuItem>{organizations.map((item) => <MenuItem key={item.id} value={item.id}>{item.name}</MenuItem>)}</TextField>}<Box sx={{ my: 2.5 }}><Stack direction="row" gap={1}><TextField size="small" fullWidth label="쿠폰 코드" value={couponCode} onChange={(e) => { setCouponCode(e.target.value); setCoupon(null); setCouponError('') }} /><Button onClick={applyCoupon} disabled={!couponCode.trim()}>적용</Button></Stack>{couponError && <Alert severity="warning" sx={{ mt: 1 }}>{couponError}</Alert>}{coupon && <Alert severity="success" sx={{ mt: 1 }} onClose={() => { setCoupon(null); setCouponCode('') }}>{coupon.name} · {money(coupon.discount_amount, talent.currency)} 할인</Alert>}</Box><Box sx={{ mb: 3 }}>{coupon && <><Stack direction="row" justifyContent="space-between"><Typography variant="body2" color="text.secondary">정가</Typography><Typography variant="body2" sx={{ textDecoration: 'line-through' }} color="text.secondary">{money(listPrice, talent.currency)}</Typography></Stack><Stack direction="row" justifyContent="space-between"><Typography variant="body2" color="text.secondary">쿠폰 할인</Typography><Typography variant="body2" color="error.main">-{money(coupon.discount_amount, talent.currency)}</Typography></Stack></>}<Typography variant="caption" color="text.secondary">결제 금액</Typography><Typography variant="h2">{money(coupon ? coupon.payable_amount : listPrice, talent.currency)}</Typography></Box>{talent.accepting_orders === false && <Alert severity="warning" sx={{ mb: 1.5 }}>이 판매자가 동시에 진행할 수 있는 주문을 모두 소화하고 있어 지금은 주문할 수 없습니다.</Alert>}<Button fullWidth variant="contained" size="large" startIcon={<ShoppingBagOutlinedIcon />} onClick={order} disabled={submitting || talent.accepting_orders === false}>{talent.accepting_orders === false ? '지금은 주문 마감' : me ? submitting ? '주문 생성 중…' : '주문하기' : '로그인하고 주문하기'}</Button>{!isSeller && <Button fullWidth variant="outlined" onClick={openAsk} sx={{ mt: 1 }}>판매자에게 문의하기</Button>}{isSeller && <Button fullWidth component={RouterLink} to="/profile/seller" sx={{ mt: 1 }}>내 상품 관리</Button>}</Card>
  </Box>    <Dialog open={askOpen} onClose={() => setAskOpen(false)} fullWidth maxWidth="sm">
      <DialogTitle>{talent.seller.display_name}님에게 문의</DialogTitle>
      <DialogContent>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>주문 전에 궁금한 점을 물어보세요. 결제나 납기가 생기지 않는 대화이며, 답변은 프로필 → 상품 문의에서 확인합니다.</Typography>
        <TextField autoFocus fullWidth multiline minRows={4} value={askBody} onChange={(event) => setAskBody(event.target.value)} placeholder="예: 패키지에 원본 파일도 포함되나요? 수정은 몇 번까지 가능한가요?" helperText={`${askBody.trim().length}/2000자`} />
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setAskOpen(false)}>취소</Button>
        <Button variant="contained" onClick={ask} disabled={askBusy || askBody.trim().length < 2 || askBody.trim().length > 2000}>{askBusy ? '보내는 중…' : '문의 보내기'}</Button>
      </DialogActions>
    </Dialog>
</Container>
}
