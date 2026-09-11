import { useEffect, useState } from 'react'
import { Alert, Box, Card, Chip, Stack, Typography } from '@mui/material'
import { api, dateTime } from '../api'
import { useApp } from '../App'
import { reportStateLabels, type Report } from '../types'

const resourceLabels: Record<string, string> = { talent: '상품', user: '사용자', order: '주문', message: '메시지' }

export function MyReportsPage() {
  const { notify } = useApp()
  const [items, setItems] = useState<Report[]>([])
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    api<{ items: Report[] }>('/api/v1/me/reports')
      .then((data) => setItems(data.items))
      .catch((cause) => notify(cause instanceof Error ? cause.message : '신고 내역을 불러오지 못했습니다.', 'error'))
      .finally(() => setLoading(false))
  }, [notify])

  return <>
    <Typography variant="h2">내 신고</Typography>
    <Typography color="text.secondary" sx={{ mt: .5, mb: 3 }}>접수한 신고의 처리 상태를 확인합니다.</Typography>
    <Stack spacing={1.5}>
      {items.map((item) => <Card key={item.id} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" gap={2}>
          <Box sx={{ minWidth: 0 }}>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Typography variant="h4">{item.reason_label}</Typography>
              <Chip size="small" variant="outlined" label={resourceLabels[item.resource_type] ?? item.resource_type} />
              <Chip size="small" color={item.state === 'resolved' ? 'success' : 'warning'} label={reportStateLabels[item.state] ?? item.state} />
            </Stack>
            {item.details && <Typography color="text.secondary" sx={{ mt: .5, whiteSpace: 'pre-wrap' }}>{item.details}</Typography>}
            {item.resolution && <Alert severity="info" sx={{ mt: 1 }}>{item.resolution}</Alert>}
          </Box>
          <Typography variant="caption" color="text.secondary" flexShrink={0}>{dateTime(item.created_at)}</Typography>
        </Stack>
      </Card>)}
    </Stack>
    {!loading && items.length === 0 && <Card sx={{ p: 5, textAlign: 'center' }}><Typography color="text.secondary">접수한 신고가 없습니다.</Typography></Card>}
  </>
}
