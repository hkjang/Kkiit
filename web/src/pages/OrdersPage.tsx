import { useEffect, useState } from 'react'
import { Alert, Box, Card, Chip, Container, LinearProgress, Stack, Typography } from '@mui/material'
import ReceiptLongRoundedIcon from '@mui/icons-material/ReceiptLongRounded'
import { Link as RouterLink } from 'react-router-dom'
import { api, dateTime, money } from '../api'
import type { Order } from '../types'

const stateLabel: Record<string, string> = { CREATED: '주문 생성', PAYMENT_PENDING: '결제 대기', PAID: '결제 완료', REQUIREMENT_PENDING: '요구사항 대기', READY: '작업 준비', IN_PROGRESS: '작업 중', DELIVERED: '납품 완료', REVISION_REQUESTED: '수정 요청', ACCEPTED: '구매확정', COMPLETED: '거래 완료', CANCEL_REQUESTED: '취소 요청', CANCELLED: '취소', DISPUTED: '분쟁', REFUNDED: '환불' }
export function OrdersPage() {
  const [orders, setOrders] = useState<Order[]>([]); const [loading, setLoading] = useState(true); const [error, setError] = useState('')
  useEffect(() => { api<{ items: Order[] }>('/api/v1/orders').then((data) => setOrders(data.items)).catch((cause) => setError(cause.message)).finally(() => setLoading(false)) }, [])
  return <Container maxWidth="lg" sx={{ py: { xs: 4, md: 7 } }}>
    <Typography variant="h2">내 주문</Typography><Typography color="text.secondary" sx={{ mt: .5, mb: 4 }}>구매와 판매 주문의 현재 상태를 한눈에 확인하세요.</Typography>
    {loading && <LinearProgress sx={{ mb: 3 }} />}{error && <Alert severity="error" sx={{ mb: 3 }}>{error}</Alert>}
    <Stack spacing={2}>{orders.map((order) => <Card key={order.id} component={RouterLink} to={`/orders/${order.id}`} sx={{ p: { xs: 2.5, sm: 3 }, textDecoration: 'none', color: 'inherit', transition: 'transform .18s, box-shadow .18s', '&:hover': { transform: 'translateY(-2px)', boxShadow: '0 16px 38px rgba(28,51,84,.1)' } }}><Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={2}><Box><Stack direction="row" spacing={1.2} alignItems="center"><ReceiptLongRoundedIcon color="primary" /><Typography variant="h4">{order.talent_title}</Typography><Chip size="small" color={order.state === 'COMPLETED' ? 'success' : order.state === 'DISPUTED' ? 'error' : 'primary'} variant="outlined" label={stateLabel[order.state] ?? order.state} /></Stack><Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>{order.order_number} · {order.buyer.display_name} → {order.seller.display_name}</Typography><Typography variant="body2" color="text.secondary">등록 {dateTime(order.created_at)} · 납기 {dateTime(order.due_at)}</Typography></Box><Box sx={{ textAlign: { sm: 'right' } }}><Typography variant="caption" color="text.secondary">계약 금액</Typography><Typography variant="h3">{money(order.amount, order.currency)}</Typography></Box></Stack></Card>)}</Stack>
    {!loading && !error && orders.length === 0 && <Card sx={{ py: 8, textAlign: 'center' }}><Typography variant="h3">진행 중인 주문이 없습니다</Typography><Typography color="text.secondary" sx={{ mt: 1 }}>마켓에서 필요한 서비스를 찾아보세요.</Typography></Card>}
  </Container>
}
