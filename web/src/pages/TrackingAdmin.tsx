import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, FormControlLabel, MenuItem, Stack, Switch, TextField, Typography } from '@mui/material'
import SaveRoundedIcon from '@mui/icons-material/SaveRounded'
import RefreshRoundedIcon from '@mui/icons-material/RefreshRounded'
import DeleteOutlineRoundedIcon from '@mui/icons-material/DeleteOutlineRounded'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import { api, dateTime } from '../api'
import { useApp } from '../App'

const settingKey = 'analytics.tracking'
const maxSnippetBytes = 8 * 1024

// Momento is first because it is the self-hosted collector: the only option
// where visit data never leaves the network.
const providers = [
  ['momento', 'Momento (사내 수집기)'], ['ga4', 'Google Analytics 4'], ['gtm', 'Google Tag Manager'], ['matomo', 'Matomo'], ['custom', '직접 붙여넣기'], ['none', '사용 안 함'],
] as const

type Tracking = {
  enabled: boolean; provider: string; momento_url: string; momento_site_id: string; momento_proxy: boolean
  measurement_id: string; matomo_url: string; matomo_site_id: string; custom_snippet: string; allowed_hosts: string
  include_admin: boolean; placement: string
}
type Setting = { key: string; value: Partial<Tracking>; version: number; updated_at: string }
type Violation = { origin: string; directive: string; page: string; count: number; first_seen: string; last_seen: string; allowed: boolean }

const defaults: Tracking = { enabled: false, provider: 'momento', momento_url: '', momento_site_id: '', momento_proxy: true, measurement_id: '', matomo_url: '', matomo_site_id: '', custom_snippet: '', allowed_hosts: '', include_admin: false, placement: 'head' }

const snippetBytes = (text: string) => new TextEncoder().encode(text).length

// The hard part of attaching a tracker is not the script tag but the content
// security policy: the page allows scripts from its own origin only, so a
// pasted snippet is silently blocked and the administrator sees an empty
// dashboard with no explanation. This page shows what the policy blocked and
// lets it be allowed in one click.
export function TrackingAdmin() {
  const { notify } = useApp()
  const [setting, setSetting] = useState<Setting | null>(null)
  const [form, setForm] = useState<Tracking>(defaults)
  const [violations, setViolations] = useState<Violation[]>([])
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => Promise.all([
    api<{ items: Setting[] }>('/api/v1/admin/settings').then((data) => {
      const item = data.items.find((entry) => entry.key === settingKey) ?? null
      setSetting(item)
      setForm({ ...defaults, ...(item?.value ?? {}) })
    }),
    api<{ items: Violation[] }>('/api/v1/admin/analytics/violations').then((data) => setViolations(data.items)),
  ]).catch((cause) => notify(cause instanceof Error ? cause.message : '방문 추적 설정을 불러오지 못했습니다.', 'error')), [notify])
  useEffect(() => { load() }, [load])

  const update = <K extends keyof Tracking>(key: K, value: Tracking[K]) => setForm((current) => ({ ...current, [key]: value }))

  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (!setting) return
    setSaving(true)
    try {
      await api(`/api/v1/admin/settings/${encodeURIComponent(settingKey)}`, { method: 'PUT', body: JSON.stringify({ value: form, version: setting.version }) })
      notify(form.enabled ? '방문 추적을 저장했습니다. 다음 페이지부터 스니펫이 붙습니다.' : '방문 추적을 저장했습니다. 스니펫은 붙지 않습니다.', 'success')
      await load()
    } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') } finally { setSaving(false) }
  }

  const allow = async (origin: string) => {
    try {
      await api('/api/v1/admin/analytics/violations/allow', { method: 'POST', body: JSON.stringify({ origin }) })
      notify(`${origin} 을(를) 허용 목록에 넣었습니다.`, 'success')
      await load()
    } catch (cause) { notify(cause instanceof Error ? cause.message : '허용하지 못했습니다.', 'error') }
  }

  const clear = async () => {
    try { await api('/api/v1/admin/analytics/violations', { method: 'DELETE' }); await load(); notify('차단 기록을 비웠습니다. 페이지를 다시 열어 아직 막히는 것이 있는지 확인하세요.', 'success') } catch (cause) { notify(cause instanceof Error ? cause.message : '비우지 못했습니다.', 'error') }
  }

  const bytes = snippetBytes(form.custom_snippet)
  const externalMomento = form.provider === 'momento' && !form.momento_proxy
  const blocked = violations.filter((item) => !item.allowed)

  return <>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 3 }}>
      <Box><Typography variant="h2">방문 추적</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>어떤 화면이 실제로 쓰이는지 재는 스크립트를 붙입니다. 기본은 꺼짐이며, 켜기 전까지 페이지는 아무것도 달라지지 않습니다.</Typography></Box>
      <Chip label={form.enabled ? '추적 켜짐' : '추적 꺼짐'} color={form.enabled ? 'success' : 'default'} variant="outlined" />
    </Stack>

    <Card component="form" onSubmit={save} sx={{ p: 3, mb: 3 }}>
      <Stack spacing={2.5}>
        <FormControlLabel control={<Switch checked={form.enabled} onChange={(event) => update('enabled', event.target.checked)} />} label="방문 추적 사용" />
        <TextField select fullWidth label="제공자" value={form.provider} onChange={(event) => update('provider', event.target.value)} helperText="Momento 는 사내에서 직접 운영하는 수집기라 방문 데이터가 밖으로 나가지 않습니다.">
          {providers.map(([value, label]) => <MenuItem key={value} value={value}>{label}</MenuItem>)}
        </TextField>

        {form.provider === 'momento' && <>
          <Stack direction={{ xs: 'column', md: 'row' }} gap={2}>
            <TextField fullWidth type="url" label="Momento 수집기 주소" placeholder="https://momento.corp.example" value={form.momento_url} onChange={(event) => update('momento_url', event.target.value)} />
            <TextField fullWidth label="사이트 id" value={form.momento_site_id} onChange={(event) => update('momento_site_id', event.target.value)} />
          </Stack>
          <FormControlLabel control={<Switch checked={form.momento_proxy} onChange={(event) => update('momento_proxy', event.target.checked)} />} label="같은 오리진 프록시로 전달 (/momento/*)" />
          <Alert severity={form.momento_proxy ? 'success' : 'warning'}>
            {form.momento_proxy
              ? '브라우저는 이 서비스에만 요청하고, 서버가 /momento/* 를 수집기로 넘깁니다. 보안 정책에 외부 출처가 등장하지 않으므로 정책을 바꿀 수 없는 설치에서도 동작합니다.'
              : '브라우저가 수집기 주소에 직접 요청합니다. 그 출처가 script-src·connect-src·img-src 에 자동으로 더해집니다.'}
          </Alert>
        </>}

        {(form.provider === 'ga4' || form.provider === 'gtm') && <>
          <TextField fullWidth label={form.provider === 'ga4' ? '측정 id (G-…)' : '컨테이너 id (GTM-…)'} value={form.measurement_id} onChange={(event) => update('measurement_id', event.target.value)} />
          <Alert severity="warning">방문 데이터가 Google 로 나갑니다. 폐쇄망에서는 스크립트가 로드되지 않으니 Momento 를 사용하세요.</Alert>
        </>}

        {form.provider === 'matomo' && <Stack direction={{ xs: 'column', md: 'row' }} gap={2}>
          <TextField fullWidth type="url" label="Matomo 주소" placeholder="https://matomo.corp.example" value={form.matomo_url} onChange={(event) => update('matomo_url', event.target.value)} />
          <TextField fullWidth label="사이트 id" value={form.matomo_site_id} onChange={(event) => update('matomo_site_id', event.target.value)} />
        </Stack>}

        {form.provider === 'custom' && <TextField fullWidth multiline minRows={6} label="추적 코드" placeholder={'<script async src="https://tracker.example/t.js"></script>'} value={form.custom_snippet} onChange={(event) => update('custom_snippet', event.target.value)} error={bytes > maxSnippetBytes}
          helperText={`${bytes.toLocaleString()} / ${maxSnippetBytes.toLocaleString()} 바이트. 모든 <script> 태그에 요청마다 다른 nonce 가 붙고, 코드 안의 http(s) 주소는 보안 정책에 자동으로 더해집니다.`} slotProps={{ input: { sx: { fontFamily: 'ui-monospace, monospace', fontSize: '.85rem' } } }} />}

        {form.provider !== 'none' && <>
          <TextField fullWidth multiline minRows={2} label="추가로 허용할 출처" placeholder={'https://cdn.example\nhttps://pixel.example'} value={form.allowed_hosts} onChange={(event) => update('allowed_hosts', event.target.value)} helperText="스니펫에서 자동으로 읽지 못한 출처를 한 줄에 하나씩 적습니다. 아래 '막힌 출처' 에서 한 번에 넣을 수도 있습니다." />
          <Stack direction={{ xs: 'column', md: 'row' }} gap={2} alignItems={{ md: 'center' }}>
            <TextField select label="삽입 위치" value={form.placement} onChange={(event) => update('placement', event.target.value)} sx={{ minWidth: 200 }}>
              <MenuItem value="head">head 끝</MenuItem><MenuItem value="body">body 끝</MenuItem>
            </TextField>
            <FormControlLabel control={<Switch checked={form.include_admin} onChange={(event) => update('include_admin', event.target.checked)} />} label="관리 화면(/admin)에서도 추적" />
          </Stack>
        </>}

        <Alert severity="info">
          정책은 <code>'unsafe-inline'</code> 으로 풀지 않습니다. 요청마다 nonce 를 만들어 스니펫의 script 태그와 <code>script-src</code> 에 같이 넣고, 추적을 끄면 정책은 원래대로 좁아집니다.{externalMomento && ' Momento 를 직접 호출하면 수집기 출처가 정책에 더해집니다.'}
        </Alert>
        <Stack direction="row" gap={1} alignItems="center">
          <Button type="submit" variant="contained" startIcon={<SaveRoundedIcon />} disabled={saving || !setting}>저장</Button>
          {setting && <Typography variant="body2" color="text.secondary">v{setting.version} · {dateTime(setting.updated_at)}</Typography>}
        </Stack>
      </Stack>
    </Card>

    <Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 1.5 }}>
      <Box><Typography variant="h3">막힌 출처</Typography><Typography variant="body2" color="text.secondary">추적이 켜진 동안 브라우저가 보안 정책에 막혔다고 신고한 주소입니다. 최근 100건의 서로 다른 출처만 메모리에 남습니다.</Typography></Box>
      <Stack direction="row" gap={1}><Button size="small" startIcon={<RefreshRoundedIcon />} onClick={() => load()}>새로고침</Button><Button size="small" color="error" startIcon={<DeleteOutlineRoundedIcon />} onClick={clear} disabled={violations.length === 0}>기록 비우기</Button></Stack>
    </Stack>
    {violations.length === 0 && <Card sx={{ p: 4, textAlign: 'center' }}><Typography color="text.secondary">{form.enabled ? '막힌 출처가 없습니다. 페이지를 열어 수집이 들어오는지 확인하세요.' : '추적을 켜면 정책에 막힌 출처가 여기에 보입니다.'}</Typography></Card>}
    <Stack spacing={1.5}>
      {violations.map((item) => <Card key={`${item.directive} ${item.origin}`} sx={{ p: 2.5 }}>
        <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'center' }} gap={2}>
          <Box>
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap"><Typography variant="h4" sx={{ fontFamily: 'ui-monospace, monospace' }}>{item.origin}</Typography><Chip size="small" label={item.directive} variant="outlined" />{item.allowed && <Chip size="small" color="success" label="지금 설정이 허용함" />}</Stack>
            <Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>{item.count}회 · 마지막 {dateTime(item.last_seen)} · {item.page || '페이지 미상'}</Typography>
          </Box>
          {!item.allowed && <Button variant="outlined" startIcon={<AddRoundedIcon />} onClick={() => allow(item.origin)}>허용 목록에 추가</Button>}
        </Stack>
      </Card>)}
    </Stack>
    {blocked.length > 0 && <Alert severity="warning" sx={{ mt: 2 }}>허용 목록에 넣은 뒤에도 같은 출처가 다시 신고되면 브라우저가 옛 페이지를 붙들고 있는 것입니다. 새로고침한 뒤 기록을 비우고 다시 확인하세요.</Alert>}
  </>
}
