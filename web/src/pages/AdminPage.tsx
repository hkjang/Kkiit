import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Link as RouterLink, NavLink, Route, Routes, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { Alert, AppBar, Box, Button, Card, Chip, Dialog, DialogActions, DialogContent, DialogTitle, Divider, Drawer, FormControlLabel, IconButton, List, ListItemButton, ListItemIcon, ListItemText, MenuItem, Select, Stack, Switch, TextField, Toolbar, Tooltip, Typography } from '@mui/material'
import DashboardRoundedIcon from '@mui/icons-material/DashboardRounded'
import PeopleAltOutlinedIcon from '@mui/icons-material/PeopleAltOutlined'
import StorefrontOutlinedIcon from '@mui/icons-material/StorefrontOutlined'
import ReceiptLongOutlinedIcon from '@mui/icons-material/ReceiptLongOutlined'
import FactCheckOutlinedIcon from '@mui/icons-material/FactCheckOutlined'
import AccountBalanceOutlinedIcon from '@mui/icons-material/AccountBalanceOutlined'
import LocalOfferOutlinedIcon from '@mui/icons-material/LocalOfferOutlined'
import GavelOutlinedIcon from '@mui/icons-material/GavelOutlined'
import PsychologyOutlinedIcon from '@mui/icons-material/PsychologyOutlined'
import AltRouteRoundedIcon from '@mui/icons-material/AltRouteRounded'
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined'
import SecurityRoundedIcon from '@mui/icons-material/SecurityRounded'
import KeyRoundedIcon from '@mui/icons-material/KeyRounded'
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined'
import LogoutRoundedIcon from '@mui/icons-material/LogoutRounded'
import ArrowBackRoundedIcon from '@mui/icons-material/ArrowBackRounded'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import SaveRoundedIcon from '@mui/icons-material/SaveRounded'
import EditRoundedIcon from '@mui/icons-material/EditRounded'
import DeleteOutlineRoundedIcon from '@mui/icons-material/DeleteOutlineRounded'
import ContentCopyRoundedIcon from '@mui/icons-material/ContentCopyRounded'
import CheckCircleRoundedIcon from '@mui/icons-material/CheckCircleRounded'
import CancelRoundedIcon from '@mui/icons-material/CancelRounded'
import InsightsOutlinedIcon from '@mui/icons-material/InsightsOutlined'
import MailOutlineRoundedIcon from '@mui/icons-material/MailOutlineRounded'
import { Brand } from '../components/Brand'
import { api, dateTime } from '../api'
import { AdminUserDetail } from './AdminUserDetail'
import { AdminOrderCase } from './AdminOrderCase'
import { useApp } from '../App'
import type { AIUsageReport, ApprovalCase, AuthProvider, FeatureFlag } from '../types'
import { EventsAdmin } from './EventsAdmin'
import { DisputeQueue } from './DisputeQueue'
import { RiskQueue } from './RiskQueue'
import { CouponsAdmin } from './CouponsAdmin'
import { ReportQueue } from './ReportQueue'
import { TrackingAdmin } from './TrackingAdmin'
import { MailAdmin } from './MailAdmin'

const drawerWidth = 274
type AdminNavItem = readonly [label:string,path:string,icon:React.ReactNode,end?:boolean]
const adminNav:ReadonlyArray<readonly [string,ReadonlyArray<AdminNavItem>]> = [
  ['운영', [['대시보드', '/admin', <DashboardRoundedIcon />, true], ['사용자', '/admin/users', <PeopleAltOutlinedIcon />], ['재능 상품', '/admin/talents', <StorefrontOutlinedIcon />], ['주문', '/admin/orders', <ReceiptLongOutlinedIcon />], ['승인 대기열', '/admin/approvals', <FactCheckOutlinedIcon />]]],
  ['거래', [['결제·정산', '/admin/finance', <AccountBalanceOutlinedIcon />], ['분쟁·위험', '/admin/risk', <GavelOutlinedIcon />], ['할인 쿠폰', '/admin/coupons', <LocalOfferOutlinedIcon />]]],
  ['자동화', [['AI 설정', '/admin/ai', <PsychologyOutlinedIcon />], ['워크플로우', '/admin/workflow', <AltRouteRoundedIcon />], ['이벤트·알림', '/admin/events', <NotificationsActiveOutlinedIcon />], ['메일 알림', '/admin/mail', <MailOutlineRoundedIcon />]]],
  ['시스템', [['기능 플래그', '/admin/features', <AltRouteRoundedIcon />], ['인증 연동', '/admin/auth', <KeyRoundedIcon />], ['역할·권한', '/admin/roles', <SecurityRoundedIcon />], ['감사 로그', '/admin/audit', <FactCheckOutlinedIcon />], ['방문 추적', '/admin/tracking', <InsightsOutlinedIcon />], ['전체 설정', '/admin/settings', <SettingsOutlinedIcon />]]],
]

export function AdminPage() {
  const { me, version } = useApp()
  const navigate=useNavigate();const location=useLocation();const mobileItems=adminNav.flatMap(([,items])=>items)
  return <Box sx={{ minHeight: '100vh', display: 'flex', bgcolor: '#f3f5f9' }}>
    <Drawer variant="permanent" sx={{ width: drawerWidth, flexShrink: 0, display: { xs: 'none', lg: 'block' }, '& .MuiDrawer-paper': { width: drawerWidth, bgcolor: '#0b203a', color: '#dce8f6', border: 0 } }}>
      <Box sx={{ px: 3, py: 2.6 }}><Brand inverse /></Box>
      <Divider sx={{ borderColor: 'rgba(255,255,255,.08)' }} />
      <Box className="admin-scroll" sx={{ flex: 1, overflowY: 'auto', px: 1.4, py: 1.5 }}>
        {adminNav.map(([group, items]) => <Box key={group} sx={{ mb: 1.6 }}><Typography variant="overline" sx={{ color: '#7890ad', px: 1.6, fontWeight: 800, letterSpacing: '.1em' }}>{group}</Typography><List disablePadding>{items.map(([label, to, icon, end]) => <ListItemButton key={to} component={NavLink} to={to} end={end} sx={{ minHeight: 44, my: .3, borderRadius: 2, color: '#b8c8dc', '& .MuiListItemIcon-root': { color: '#89a1bf' }, '&.active': { bgcolor: 'rgba(96,145,255,.18)', color: 'white', '& .MuiListItemIcon-root': { color: '#91b2ff' } } }}><ListItemIcon sx={{ minWidth: 42 }}>{icon}</ListItemIcon><ListItemText primary={label} slotProps={{ primary: { fontSize: '.95rem', fontWeight: 650 } }} /></ListItemButton>)}</List></Box>)}
      </Box>
      <Divider sx={{ borderColor: 'rgba(255,255,255,.08)' }} /><Box sx={{ p: 2.2 }}><Button component={RouterLink} to="/" fullWidth startIcon={<ArrowBackRoundedIcon />} sx={{ color: '#dce8f6', justifyContent: 'flex-start' }}>마켓으로 돌아가기</Button><Typography variant="caption" sx={{ color: '#7890ad', px: 1, mt: 1, display: 'block' }}>Kkiit v{version?.version ?? '—'}</Typography></Box>
    </Drawer>
    <Box sx={{ flex: 1, minWidth: 0, ml: { lg: `${drawerWidth}px` } }}>
      <AppBar position="sticky" color="inherit" elevation={0} sx={{ borderBottom: '1px solid', borderColor: 'divider', bgcolor: 'rgba(255,255,255,.94)', backdropFilter: 'blur(16px)' }}><Toolbar sx={{ minHeight: 72 }}><Typography fontWeight={800} sx={{display:{xs:'none',sm:'block'}}}>서비스 관리자</Typography><Select size="small" value={mobileItems.some(([,path])=>path===location.pathname)?location.pathname:'/admin'} onChange={e=>navigate(e.target.value)} sx={{display:{lg:'none'},minWidth:180}}>{mobileItems.map(([label,path])=><MenuItem key={path} value={path}>{label}</MenuItem>)}</Select><Box sx={{ flex: 1 }} /><Chip label="오프라인 운영" color="success" variant="outlined" size="small" sx={{display:{xs:'none',sm:'flex'}}}/><Typography sx={{ ml: 2, fontWeight: 700,display:{xs:'none',md:'block'} }}>{me?.display_name}</Typography><Tooltip title="마켓으로"><IconButton component={RouterLink} to="/"><LogoutRoundedIcon /></IconButton></Tooltip></Toolbar></AppBar>
      <Box component="main" sx={{ p: { xs: 2, sm: 3, xl: 4 } }}><Routes>
        <Route index element={<AdminDashboard />} />
        <Route path="users" element={<UsersAdmin />} />
        <Route path="talents" element={<TalentsAdmin />} />
        <Route path="orders" element={<OrdersAdmin />} />
        <Route path="approvals" element={<ApprovalAdmin />} />
        <Route path="finance" element={<FinanceAdmin />} />
        <Route path="risk" element={<RiskAdmin />} />
        <Route path="coupons" element={<CouponsAdmin />} />
        <Route path="ai" element={<><AIUsagePanel /><SettingsAdmin prefix="ai." title="AI 설정" description="모델 라우팅, Gateway와 비용 제한을 중앙에서 관리합니다." /></>} />
        <Route path="workflow" element={<SettingsAdmin prefix="workflow." title="워크플로우 설정" description="자동확정, 재시도와 이벤트 처리 정책을 관리합니다." />} />
        <Route path="events" element={<EventsAdmin />} />
        <Route path="features" element={<FeatureFlagsAdmin />} />
        <Route path="auth" element={<AuthAdmin />} />
        <Route path="roles" element={<RolesAdmin />} />
        <Route path="audit" element={<AuditAdmin />} />
        <Route path="tracking" element={<TrackingAdmin />} />
        <Route path="mail" element={<MailAdmin />} />
        <Route path="settings" element={<SettingsAdmin />} />
        <Route path="*" element={<AdminPlaceholder />} />
      </Routes></Box>
    </Box>
  </Box>
}

function PageTitle({ title, description, action }: { title: string; description: string; action?: React.ReactNode }) { return <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'end' }} gap={2} sx={{ mb: 3 }}><Box><Typography variant="h2">{title}</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>{description}</Typography></Box>{action}</Stack> }
type DashboardStats={users:number;published_talents:number;pending_approvals:number;active_orders:number;gmv:number;settlement_pending:number;high_risks:number;open_disputes:number;breached_disputes:number;dispute_sla_hours:number;stale_approvals:number;approval_sla_hours:number;overdue_orders:number;settlement_holds:number;failed_events:number;exception_count:number}
function AdminDashboard(){
  const {notify}=useApp();const [stats,setStats]=useState<DashboardStats|null>(null)
  useEffect(()=>{api<DashboardStats>('/api/v1/admin/dashboard').then(setStats).catch(e=>notify(e.message,'error'))},[notify])

  // A count on its own is a dead number: an operator reads "분쟁 3건" and then
  // has to go and find those three themselves. Every line here carries the
  // queue it came from, already narrowed to exactly these items.
  type WorkItem={label:string;count:number;to:string;detail:string;severity:'error'|'warning'}
  const work:WorkItem[] = stats ? ([
    {label:'처리 지연 분쟁',count:stats.breached_disputes,to:'/admin/risk',severity:'error',
      detail:`약속한 ${stats.dispute_sla_hours}시간을 넘긴 미해결 분쟁`},
    {label:'미해결 분쟁',count:stats.open_disputes-stats.breached_disputes,to:'/admin/risk',severity:'warning',
      detail:'아직 기한 안이지만 처리를 기다리는 분쟁'},
    {label:'검토 지연',count:stats.stale_approvals,to:'/admin/approvals',severity:'error',
      detail:`${stats.approval_sla_hours}시간을 넘긴 승인 대기. 그동안 판매자는 팔 수 없습니다`},
    {label:'승인 대기',count:stats.pending_approvals-stats.stale_approvals,to:'/admin/approvals',severity:'warning',
      detail:'결재를 기다리는 요청'},
    {label:'납기 초과 주문',count:stats.overdue_orders,to:'/admin/orders?overdue=1',severity:'warning',
      detail:'약속한 날짜를 넘긴 미종료 주문'},
    {label:'정산 보류',count:stats.settlement_holds,to:'/admin/finance?state=hold',severity:'warning',
      detail:'판매자에게 지급이 멈춰 있는 정산'},
    {label:'위험 신호',count:stats.high_risks,to:'/admin/risk',severity:'warning',detail:'HIGH 이상 위험 평가'},
    {label:'전달 실패 이벤트',count:stats.failed_events,to:'/admin/events',severity:'error',
      detail:'알림과 웹훅이 상대에게 닿지 못한 건'},
  ] as WorkItem[]).filter(item=>item.count>0) : []

  return <><PageTitle title="운영 대시보드" description="지금 처리해야 할 일부터 보여 줍니다. 각 줄은 해당 대기열로 바로 이어집니다."/>
    {stats && work.length===0 && <Alert severity="success" sx={{mb:3}}>지금 처리해야 할 예외가 없습니다.</Alert>}
    <Stack spacing={1.5}>{work.map(item=><Card key={item.label} sx={{p:2.5}}>
      <Stack direction={{xs:'column',sm:'row'}} justifyContent="space-between" alignItems={{sm:'center'}} gap={2}>
        <Box>
          <Stack direction="row" gap={1} alignItems="center">
            <Chip size="small" color={item.severity==='error'?'error':'warning'} label={item.count}/>
            <Typography variant="h4">{item.label}</Typography>
          </Stack>
          <Typography variant="body2" color="text.secondary" sx={{mt:.5}}>{item.detail}</Typography>
        </Box>
        <Button variant="outlined" component={RouterLink} to={item.to} sx={{flexShrink:0}}>열기</Button>
      </Stack>
    </Card>)}</Stack>
    <Card sx={{mt:3,p:3}}>
      <Typography variant="h4">지금 서비스 상태</Typography>
      <Stack direction="row" gap={3} flexWrap="wrap" sx={{mt:1}}>
        <Typography variant="body2" color="text.secondary">활성 사용자 <b>{stats?.users??'—'}</b></Typography>
        <Typography variant="body2" color="text.secondary">공개 상품 <b>{stats?.published_talents??'—'}</b></Typography>
        <Typography variant="body2" color="text.secondary">진행 주문 <b>{stats?.active_orders??'—'}</b></Typography>
        <Typography variant="body2" color="text.secondary">정산 예정 <b>{stats?`${stats.settlement_pending.toLocaleString()}원`:'—'}</b></Typography>
        <Typography variant="body2" color="text.secondary">누적 결제 <b>{stats?`${stats.gmv.toLocaleString()}원`:'—'}</b></Typography>
      </Stack>
    </Card>
  </>
}

// The monthly budget is only meaningful next to what has been spent against it.
function AIUsagePanel() {
  const { notify } = useApp(); const [report, setReport] = useState<AIUsageReport | null>(null)
  useEffect(() => { api<AIUsageReport>('/api/v1/admin/ai/usage').then(setReport).catch((e) => notify(e instanceof Error ? e.message : 'AI 사용량을 불러오지 못했습니다.', 'error')) }, [notify])
  if (!report) return null
  const money = (value: number) => `${value.toFixed(4)} ${report.currency}`
  return <Card sx={{ p: 3, mb: 3 }}>
    <Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'center' }} gap={1.5}>
      <Box><Typography variant="h3">이번 달 AI 사용</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>호출 {report.executions}회 · 입력 {report.input_tokens.toLocaleString()} · 출력 {report.output_tokens.toLocaleString()} 토큰</Typography></Box>
      <Box sx={{ textAlign: { sm: 'right' } }}><Typography variant="h3">{money(report.month_spent)}</Typography><Typography variant="body2" color="text.secondary">{report.budget_enforced ? `예산 ${money(report.monthly_budget)} · 잔여 ${money(report.remaining)}` : '예산 제한 없음'}</Typography></Box>
    </Stack>
    {report.exhausted && <Alert severity="warning" sx={{ mt: 2 }}>이번 달 예산을 모두 사용해 AI 기능이 로컬 템플릿으로 대체되고 있습니다.</Alert>}
    {!report.budget_enforced && report.month_spent > 0 && <Alert severity="info" sx={{ mt: 2 }}>월 예산이 0이면 사용량만 기록하고 호출을 막지 않습니다.</Alert>}
    <Stack spacing={1} sx={{ mt: 2 }}>
      {report.breakdown.map((item) => <Stack key={`${item.feature}-${item.model}`} direction="row" justifyContent="space-between">
        <Typography variant="body2">{item.feature} · {item.model} · {item.calls}회</Typography>
        <Typography variant="body2" fontWeight={650}>{money(item.estimated_cost)}</Typography>
      </Stack>)}
    </Stack>
    {report.breakdown.length === 0 && <Typography color="text.secondary" sx={{ mt: 2 }}>이번 달 기록된 AI 호출이 없습니다.</Typography>}
  </Card>
}

// A flag that changes nothing is worse than no flag, so only switches that
// actually gate behaviour are listed here.
function FeatureFlagsAdmin() {
  const { notify } = useApp(); const [items, setItems] = useState<FeatureFlag[]>([])
  const load = useCallback(() => { api<{ items: FeatureFlag[] }>('/api/v1/admin/feature-flags').then((d) => setItems(d.items)).catch((e) => notify(e instanceof Error ? e.message : '기능 플래그를 불러오지 못했습니다.', 'error')) }, [notify])
  useEffect(() => { load() }, [load])
  const toggle = async (item: FeatureFlag, enabled: boolean) => {
    try { await api(`/api/v1/admin/feature-flags/${item.key}`, { method: 'PUT', body: JSON.stringify({ enabled }) }); load(); notify(`${item.description}을(를) ${enabled ? '켰습니다' : '껐습니다'}.`, 'success') }
    catch (e) { notify(e instanceof Error ? e.message : '기능 플래그를 바꾸지 못했습니다.', 'error') }
  }
  return <><PageTitle title="기능 플래그" description="배포에서 노출하지 않을 기능을 끕니다. 끄면 화면에서 사라지고 해당 API도 거부합니다." />
    <Card sx={{ p: 3 }}><Stack spacing={2}>
      {items.map((item) => <Stack key={item.key} direction="row" justifyContent="space-between" alignItems="center" gap={2}>
        <Box><Typography fontWeight={700}>{item.description}</Typography><Typography variant="caption" color="text.secondary">{item.key}</Typography></Box>
        <Switch checked={item.enabled} onChange={(e) => toggle(item, e.target.checked)} inputProps={{ 'aria-label': item.description }} />
      </Stack>)}
    </Stack>
    <Alert severity="info" sx={{ mt: 3 }}>변경은 몇 초 안에 적용됩니다. 이미 진행 중인 주문과 데이터는 유지되며 새 요청만 거부됩니다.</Alert>
    </Card></>
}

function AdminPlaceholder() { return <><PageTitle title="관리 기능" description="해당 운영 모듈의 API와 상세 화면을 확장할 수 있습니다." /><Card sx={{ p: 5 }}><Typography variant="h3">도메인 경계가 준비되어 있습니다</Typography><Typography color="text.secondary" sx={{ mt: 1 }}>핵심 설정·승인·권한·감사 기능부터 활성화했습니다.</Typography></Card></> }

type AdminTalent={id:string;title:string;status:string;service_type:string;base_price:number;currency:string;delivery_days:number;quality_score?:number;seller_name:string;updated_at:string}
function TalentsAdmin(){const {notify}=useApp();const [items,setItems]=useState<AdminTalent[]>([]);const [cursor,setCursor]=useState<string|null>(null);const [busy,setBusy]=useState(false);const fetchPage=useCallback((next?:string)=>{setBusy(true);api<{items:AdminTalent[];next_cursor:string|null}>(`/api/v1/admin/talents?limit=50${next?`&cursor=${encodeURIComponent(next)}`:''}`).then(d=>{setItems(current=>next?[...current,...d.items]:d.items);setCursor(d.next_cursor)}).catch(e=>notify(e.message,'error')).finally(()=>setBusy(false))},[notify]);const load=useCallback(()=>fetchPage(),[fetchPage]);useEffect(()=>{load()},[load]);const setStatus=async(item:AdminTalent,status:string)=>{const note=status==='paused'?window.prompt('비공개 사유를 입력하세요.')??'':'';if(status==='paused'&&!note)return;try{await api(`/api/v1/admin/talents/${item.id}/status`,{method:'POST',body:JSON.stringify({status,note})});load();notify('상품 상태를 변경했습니다.','success')}catch(e){notify(e instanceof Error?e.message:'상태를 변경하지 못했습니다.','error')}};return <><PageTitle title="재능 상품" description="공개·초안·검토 상태를 판매자와 함께 확인합니다."/><Stack spacing={1.5}>{items.map(item=><Card key={item.id} sx={{p:2.5}}><Stack direction={{xs:'column',md:'row'}} justifyContent="space-between" gap={2}><Box><Stack direction="row" gap={1}><Typography variant="h4">{item.title}</Typography><Chip size="small" label={item.status} color={item.status==='published'?'success':item.status==='review_pending'?'warning':'default'}/></Stack><Typography variant="body2" color="text.secondary">{item.seller_name} · {item.service_type} · {item.delivery_days}일</Typography></Box><Box sx={{textAlign:{md:'right'}}}><Typography fontWeight={800}>{item.base_price.toLocaleString()}원</Typography><Typography variant="body2" color="text.secondary">품질 점수 {item.quality_score??'—'}</Typography><Stack direction="row" gap={.5} justifyContent={{md:'flex-end'}} sx={{mt:1}}>{item.status==='published'?<Button size="small" color="warning" onClick={()=>setStatus(item,'paused')}>비공개</Button>:item.status==='paused'?<><Button size="small" onClick={()=>setStatus(item,'published')}>다시 공개</Button><Button size="small" color="error" onClick={()=>setStatus(item,'archived')}>보관</Button></>:null}</Stack></Box></Stack></Card>)}</Stack>{cursor&&<Box sx={{textAlign:'center',mt:2}}><Button onClick={()=>fetchPage(cursor)} disabled={busy}>{busy?'불러오는 중…':'더 보기'}</Button></Box>}</>}

type AdminOrder={note_count?:number;id:string;order_number:string;state:string;amount:number;currency:string;due_at?:string;created_at:string;talent_title:string;buyer_name:string;seller_name:string}
function OrdersAdmin(){const {notify}=useApp();const [items,setItems]=useState<AdminOrder[]>([]);const [cursor,setCursor]=useState<string|null>(null);const [busy,setBusy]=useState(false);const [params,setParams]=useSearchParams();const query=params.get('q')??'';const state=params.get('state')??'';const user=params.get('user')??'';const overdue=params.get('overdue')==='1';const [draft,setDraft]=useState(query);const [inspecting,setInspecting]=useState<string|null>(null);const fetchPage=useCallback((next?:string)=>{setBusy(true);api<{items:AdminOrder[];next_cursor:string|null}>(`/api/v1/admin/orders?limit=50${query?`&q=${encodeURIComponent(query)}`:''}${state?`&state=${state}`:''}${user?`&user=${encodeURIComponent(user)}`:''}${overdue?'&overdue=1':''}${next?`&cursor=${encodeURIComponent(next)}`:''}`).then(d=>{setItems(current=>next?[...current,...d.items]:d.items);setCursor(d.next_cursor)}).catch(e=>notify(e.message,'error')).finally(()=>setBusy(false))},[notify,query,state,user,overdue]);useEffect(()=>{fetchPage()},[fetchPage]);
  const setFilter=(patch:Record<string,string>)=>{const next=new URLSearchParams(params);for(const [key,value] of Object.entries(patch)){if(value)next.set(key,value);else next.delete(key)}setParams(next,{replace:true})}
  if(inspecting)return <AdminOrderCase orderId={inspecting} onClose={()=>setInspecting(null)}/>
  return <><PageTitle title="주문 운영" description="주문 번호나 상품명으로 찾고, 상태와 계정으로 좁힙니다."/>
    <Stack direction="row" gap={1.5} flexWrap="wrap" sx={{mb:2}}>
      <TextField size="small" label="주문 번호 또는 상품명" value={draft} onChange={e=>setDraft(e.target.value)} onKeyDown={e=>{if(e.key==='Enter')setFilter({q:draft})}} sx={{minWidth:260}}/>
      <Button onClick={()=>setFilter({q:draft})}>검색</Button>
      <TextField size="small" select label="상태" value={state} onChange={e=>setFilter({state:e.target.value})} sx={{minWidth:160}}>
        <MenuItem value="">전체</MenuItem><MenuItem value="OPEN">진행 중</MenuItem><MenuItem value="DELIVERED">납품 완료</MenuItem><MenuItem value="DISPUTED">분쟁</MenuItem><MenuItem value="COMPLETED">거래 완료</MenuItem><MenuItem value="CANCELLED">취소</MenuItem><MenuItem value="REFUNDED">환불</MenuItem>
      </TextField>
      <Button variant={overdue?'contained':'outlined'} onClick={()=>setFilter({overdue:overdue?'':'1'})}>납기 초과만</Button>
      {(query||state||user||overdue)&&<Button onClick={()=>{setDraft('');setParams({},{replace:true})}}>조건 지우기</Button>}
    </Stack>
    {user&&<Alert severity="info" sx={{mb:2}}>한 계정의 주문만 보고 있습니다. <Button size="small" onClick={()=>setFilter({user:''})}>전체 보기</Button></Alert>}<Stack spacing={1.5}>{items.map(item=><Card key={item.id} sx={{p:2.5,cursor:'pointer'}} onClick={()=>setInspecting(item.id)}><Stack direction={{xs:'column',md:'row'}} justifyContent="space-between" gap={2}><Box><Stack direction="row" gap={1}><Typography variant="h4">{item.order_number}</Typography><Chip size="small" label={item.state}/>{(item.note_count??0)>0&&<Chip size="small" variant="outlined" label={`메모 ${item.note_count}`}/>}</Stack><Typography variant="body2" color="text.secondary">{item.talent_title}</Typography><Typography variant="body2">{item.buyer_name} → {item.seller_name}</Typography></Box><Box sx={{textAlign:{md:'right'}}}><Typography fontWeight={800}>{item.amount.toLocaleString()}원</Typography><Typography variant="body2" color="text.secondary">납기 {dateTime(item.due_at)}</Typography></Box></Stack></Card>)}</Stack>{cursor&&<Box sx={{textAlign:'center',mt:2}}><Button onClick={()=>fetchPage(cursor)} disabled={busy}>{busy?'불러오는 중…':'더 보기'}</Button></Box>}</>}


function RiskAdmin(){const [counts,setCounts]=useState({open_disputes:0,settlement_holds:0});const onLoaded=useCallback((value:{open_disputes:number;settlement_holds:number})=>setCounts(value),[]);return <><PageTitle title="분쟁·위험" description="고위험 거래, 미해결 분쟁과 정산 보류를 예외 대기열로 확인합니다."/><Alert severity={counts.open_disputes||counts.settlement_holds?'warning':'success'} sx={{mb:3}}>미해결 분쟁 {counts.open_disputes}건 · 정산 보류 {counts.settlement_holds}건</Alert><DisputeQueue/><Box sx={{mt:4}}><ReportQueue/></Box><Box sx={{mt:4}}><RiskQueue onLoaded={onLoaded}/></Box></>}

type Settlement={id:string;order_number:string;seller_name:string;gross_amount:number;platform_fee:number;net_amount:number;state:string;hold_reason?:string;scheduled_at?:string;settled_at?:string}
function FinanceAdmin(){const {notify}=useApp();const [items,setItems]=useState<Settlement[]>([]);const [cursor,setCursor]=useState<string|null>(null);const [busy,setBusy]=useState(false);const [params,setParams]=useSearchParams();const state=params.get('state')??'';const fetchPage=useCallback((next?:string)=>{setBusy(true);api<{items:Settlement[];next_cursor:string|null}>(`/api/v1/admin/settlements?limit=50${state?`&state=${state}`:''}${next?`&cursor=${encodeURIComponent(next)}`:''}`).then(d=>{setItems(current=>next?[...current,...d.items]:d.items);setCursor(d.next_cursor)}).catch(e=>notify(e.message,'error')).finally(()=>setBusy(false))},[notify,state]);const load=useCallback(()=>fetchPage(),[fetchPage]);useEffect(()=>{load()},[load]);const act=async(item:Settlement,action:string)=>{const reason=action==='hold'?window.prompt('정산 보류 사유')??'':'';if(action==='hold'&&!reason)return;try{await api(`/api/v1/admin/settlements/${item.id}/action`,{method:'POST',body:JSON.stringify({action,reason})});load();notify('정산 상태를 변경했습니다.','success')}catch(e){notify(e instanceof Error?e.message:'처리하지 못했습니다.','error')}};return <><PageTitle title="결제·정산" description="주문과 분리된 원장을 기준으로 정산 예정, 보류와 완료를 관리합니다."/>
    <Stack direction="row" gap={1.5} flexWrap="wrap" sx={{mb:2}}>
      <TextField size="small" select label="상태" value={state} onChange={e=>setParams(e.target.value?{state:e.target.value}:{},{replace:true})} sx={{minWidth:180}}>
        <MenuItem value="">전체</MenuItem><MenuItem value="scheduled">지급 예정</MenuItem><MenuItem value="confirmed">확정</MenuItem><MenuItem value="hold">보류</MenuItem><MenuItem value="completed">지급 완료</MenuItem><MenuItem value="cancelled">취소</MenuItem>
      </TextField>
    </Stack>
    <Stack spacing={1.5}>{items.map(item=><Card key={item.id} sx={{p:2.5}}><Stack direction={{xs:'column',md:'row'}} justifyContent="space-between" gap={2}><Box><Stack direction="row" gap={1}><Typography variant="h4">{item.order_number}</Typography><Chip size="small" color={item.state==='completed'?'success':item.state==='hold'?'warning':'primary'} label={item.state}/></Stack><Typography variant="body2" color="text.secondary">{item.seller_name} · 예정 {dateTime(item.scheduled_at)}</Typography>{item.hold_reason&&<Alert severity="warning" sx={{mt:1}}>{item.hold_reason}</Alert>}</Box><Box sx={{textAlign:{md:'right'}}}><Typography variant="h3">{item.net_amount.toLocaleString()}원</Typography><Typography variant="body2" color="text.secondary">총액 {item.gross_amount.toLocaleString()} · 수수료 {item.platform_fee.toLocaleString()}</Typography><Stack direction="row" gap={1} justifyContent={{md:'flex-end'}} sx={{mt:1}}>{item.state==='hold'?<Button onClick={()=>act(item,'release')}>보류 해제</Button>:item.state!=='completed'&&<Button color="warning" onClick={()=>act(item,'hold')}>보류</Button>}{item.state!=='completed'&&item.state!=='hold'&&<Button variant="contained" onClick={()=>act(item,'complete')}>지급 완료</Button>}</Stack></Box></Stack></Card>)}</Stack>{cursor&&<Box sx={{textAlign:'center',mt:2}}><Button onClick={()=>fetchPage(cursor)} disabled={busy}>{busy?'불러오는 중…':'더 보기'}</Button></Box>}{items.length===0&&<Card sx={{p:5,textAlign:'center'}}><Typography color="text.secondary">정산 내역이 없습니다.</Typography></Card>}</>}

type AdminUser={id:string;username:string;email?:string;display_name:string;status:string;roles:string[];last_login_at?:string;created_at:string}
function UsersAdmin(){const {notify}=useApp();const [items,setItems]=useState<AdminUser[]>([]);const [roleCatalog,setRoleCatalog]=useState<Role[]>([]);const [editing,setEditing]=useState<AdminUser|null>(null);const [roles,setRoles]=useState<string[]>([]);const [display,setDisplay]=useState('');const [status,setStatus]=useState('active');const [inspecting,setInspecting]=useState<string|null>(null);const [cursor,setCursor]=useState<string|null>(null);const [busy,setBusy]=useState(false);const fetchPage=useCallback((next?:string)=>{setBusy(true);api<{items:AdminUser[];next_cursor:string|null}>(`/api/v1/admin/users?limit=50${next?`&cursor=${encodeURIComponent(next)}`:''}`).then(d=>{setItems(current=>next?[...current,...d.items]:d.items);setCursor(d.next_cursor)}).catch(e=>notify(e.message,'error')).finally(()=>setBusy(false))},[notify]);const load=useCallback(()=>{fetchPage();api<{items:Role[]}>('/api/v1/admin/roles').then(d=>setRoleCatalog(d.items)).catch(e=>notify(e.message,'error'))},[fetchPage,notify]);useEffect(()=>{load()},[load]);const manageKeys=async(user:AdminUser)=>{try{const data=await api<{items:{id:string;name:string;prefix:string;last_used_at?:string;revoked_at?:string}[]}>(`/api/v1/admin/users/${user.id}/api-keys`);const live=data.items.filter(item=>!item.revoked_at);if(live.length===0){notify('이 계정에는 사용 중인 API 키가 없습니다.','info');return}const target=live.find(item=>window.confirm(`${item.name} (${item.prefix}…) 키를 폐기할까요? 최근 사용 ${item.last_used_at?dateTime(item.last_used_at):'기록 없음'}`));if(!target)return;await api(`/api/v1/admin/api-keys/${target.id}`,{method:'DELETE'});notify('API 키를 폐기했습니다.','success')}catch(e){notify(e instanceof Error?e.message:'API 키를 처리하지 못했습니다.','error')}};const resetMFA=async(user:AdminUser)=>{if(!window.confirm(`${user.display_name} 계정의 MFA를 초기화할까요? 활성 세션도 함께 종료됩니다.`))return;try{await api(`/api/v1/admin/users/${user.id}/mfa`,{method:'DELETE'});notify('MFA를 초기화했습니다.','success')}catch(e){notify(e instanceof Error?e.message:'MFA를 초기화하지 못했습니다.','error')}};const open=(user:AdminUser)=>{setEditing(user);setRoles(user.roles);setDisplay(user.display_name);setStatus(user.status)};const save=async()=>{if(!editing)return;try{await api(`/api/v1/admin/users/${editing.id}`,{method:'PATCH',body:JSON.stringify({display_name:display,status})});await api(`/api/v1/admin/users/${editing.id}/roles`,{method:'PUT',body:JSON.stringify({roles})});setEditing(null);load();notify('사용자와 역할을 저장했습니다.','success')}catch(e){notify(e instanceof Error?e.message:'저장하지 못했습니다.','error')}};if(inspecting)return <AdminUserDetail userId={inspecting} onClose={()=>{setInspecting(null);load()}}/>
  return <><PageTitle title="사용자 관리" description="계정을 눌러 거래·분쟁·활동 이력을 한 화면에서 확인합니다."/><Stack spacing={1.5}>{items.map(user=><Card key={user.id} sx={{p:2.5,cursor:'pointer'}} onClick={()=>setInspecting(user.id)}><Stack direction={{xs:'column',md:'row'}} justifyContent="space-between" gap={2}><Box><Stack direction="row" gap={1}><Typography variant="h4">{user.display_name}</Typography><Chip size="small" color={user.status==='active'?'success':'error'} label={user.status}/></Stack><Typography variant="body2" color="text.secondary">{user.email??`@${user.username}`} · 최근 로그인 {dateTime(user.last_login_at)}</Typography></Box><Stack direction="row" gap={.6} flexWrap="wrap" alignItems="center">{user.roles.map(role=><Chip key={role} size="small" label={role}/>)}<Button size="small" onClick={event=>{event.stopPropagation();open(user)}}>편집</Button></Stack></Stack></Card>)}</Stack>{cursor&&<Box sx={{textAlign:'center',mt:2}}><Button onClick={()=>fetchPage(cursor)} disabled={busy}>{busy?'불러오는 중…':'더 보기'}</Button></Box>}<Dialog open={Boolean(editing)} onClose={()=>setEditing(null)} fullWidth maxWidth="sm"><DialogTitle>사용자 관리 · {editing?.username}</DialogTitle><DialogContent><Stack spacing={2} sx={{mt:1}}><TextField label="표시 이름" value={display} onChange={e=>setDisplay(e.target.value)}/><TextField select label="상태" value={status} onChange={e=>setStatus(e.target.value)}><MenuItem value="active">활성</MenuItem><MenuItem value="suspended">정지</MenuItem></TextField><Typography fontWeight={750}>역할</Typography>{roleCatalog.map(role=><FormControlLabel key={role.code} control={<Switch checked={roles.includes(role.code)} onChange={e=>setRoles(e.target.checked?[...roles,role.code]:roles.filter(item=>item!==role.code))}/>} label={`${role.name} (${role.code})`}/>)}</Stack></DialogContent><DialogActions><Button color="warning" onClick={()=>editing&&resetMFA(editing)} sx={{mr:'auto'}}>MFA 초기화</Button><Button color="warning" onClick={()=>editing&&manageKeys(editing)}>API 키</Button><Button onClick={()=>setEditing(null)}>취소</Button><Button variant="contained" onClick={save}>저장</Button></DialogActions></Dialog></>}

type SettingValue = null | boolean | number | string | SettingValue[] | { [key: string]: SettingValue }
type Setting = { key: string; value: SettingValue; is_secret: boolean; secret_configured: boolean; connected: boolean; version: number; description: string; updated_at: string }

const settingLabels: Record<string, string> = {
  name: '서비스 이름', locale: '기본 언어', timezone: '기본 시간대', offline_mode: '오프라인 모드', public_registration: '공개 회원가입',
  session_ttl_hours: '로그인 유지 시간(시간)', cookie_secure: 'HTTPS 전용 쿠키', mfa_admin_required: '관리자 MFA 필수', allow_local_login: '로컬 로그인 허용',
  auto_create_user: '첫 로그인 시 계정 자동 생성', default_roles: '신규 계정 기본 역할', callback_base_url: '외부 서비스 주소',
  enabled: '사용', base_url: 'Gateway 주소', monthly_budget: '월 예산', fast: '빠른 모델', balanced: '균형 모델', premium: '고급 모델',
  approval_disabled_means_bypass: '승인 정책이 없으면 자동 진행', outbox_retry_limit: '이벤트 재시도 횟수',
  currency: '기본 통화', platform_fee_rate: '플랫폼 수수료(%)', auto_accept_days: '자동 구매확정 대기(일)', max_revision_count: '최대 수정 횟수',
  driver: '저장 방식', max_upload_mb: '최대 업로드 크기(MB)', allowed_mime_types: '허용 파일 형식',
  web: '웹 알림', email: '이메일', sms: 'SMS', push: '푸시', webhook: 'Webhook',
  allowed_origins: '허용 Origin', default_rate_limit_per_minute: '기본 분당 요청 수', mcp_enabled: 'MCP 사용',
  delay_days: '정산 대기(일)', batch_enabled: '일괄 정산', hold_levels: '정산 보류 위험 등급',
  high_threshold: '고위험 기준 점수', critical_threshold: '치명 위험 기준 점수', auto_hold_settlement: '위험 정산 자동 보류',
  provider: '제공자', escrow_enabled: '에스크로 사용', idempotency_required: '중복 결제 방지 필수',
  mode: '검색 방식', semantic_enabled: '의미 기반 검색', personalization_enabled: '개인화 검색',
  default_timeout_seconds: '기본 제한 시간(초)', max_retries: '최대 재시도', human_review_default: '사람 검토 기본 적용',
  otel_enabled: 'OpenTelemetry 사용', endpoint: '수집 주소', business_trace_enabled: '비즈니스 추적 사용',
  resource: '리소스 식별자', audience: '허용 대상', scopes: '허용 범위',
  momento_url: 'Momento 수집기 주소', momento_site_id: 'Momento 사이트 id', momento_proxy: '같은 오리진 프록시', measurement_id: '측정 id', matomo_url: 'Matomo 주소', matomo_site_id: 'Matomo 사이트 id', custom_snippet: '추적 코드', allowed_hosts: '추가 허용 출처', include_admin: '관리 화면에서도 추적', placement: '삽입 위치',
}

const fieldLabel = (path: string[]) => settingLabels[path[path.length - 1]] ?? path[path.length - 1].replaceAll('_', ' ')
const isSettingObject = (value: SettingValue): value is { [key: string]: SettingValue } => Boolean(value) && typeof value === 'object' && !Array.isArray(value)

function SettingFields({ value, onChange, path = [] }: { value: SettingValue; onChange: (value: SettingValue) => void; path?: string[] }) {
  if (typeof value === 'boolean') return <FormControlLabel control={<Switch checked={value} onChange={(event) => onChange(event.target.checked)} />} label={fieldLabel(path)} />
  if (typeof value === 'number') return <TextField fullWidth type="number" label={fieldLabel(path)} value={value} onChange={(event) => onChange(Number(event.target.value))} />
  if (typeof value === 'string' || value === null) {
    const label = fieldLabel(path)
    const urlField = path[path.length - 1]?.includes('url') || path[path.length - 1] === 'endpoint'
    return <TextField fullWidth type={urlField ? 'url' : 'text'} label={label} value={value ?? ''} onChange={(event) => onChange(event.target.value)} helperText={path[path.length - 1] === 'callback_base_url' ? '예: https://market.example.com — 비우면 현재 접속 주소를 사용합니다.' : undefined} />
  }
  if (Array.isArray(value)) {
    if (value.length === 0 || value.every((item) => typeof item === 'string')) {
      return <TextField fullWidth multiline minRows={2} label={fieldLabel(path)} value={(value as string[]).join('\n')} onChange={(event) => onChange(event.target.value.split('\n').map((item) => item.trim()).filter(Boolean))} helperText="여러 값은 한 줄에 하나씩 입력하세요." />
    }
    if (value.every((item) => typeof item === 'number')) {
      return <TextField fullWidth label={fieldLabel(path)} value={value.join(', ')} onChange={(event) => onChange(event.target.value.split(',').map((item) => item.trim()).filter(Boolean).map(Number).filter(Number.isFinite))} helperText="쉼표로 구분하세요." />
    }
    return <Card variant="outlined" sx={{ p: 2 }}><Typography fontWeight={750} sx={{ mb: 1 }}>{fieldLabel(path)}</Typography><Stack spacing={1.5}>{value.map((item, index) => <Card variant="outlined" sx={{ p: 2 }} key={index}><SettingFields value={item} path={[...path, String(index + 1)]} onChange={(next) => onChange(value.map((current, itemIndex) => itemIndex === index ? next : current))} /><Button color="error" size="small" sx={{ mt: 1 }} onClick={() => onChange(value.filter((_, itemIndex) => itemIndex !== index))}>항목 삭제</Button></Card>)}</Stack><Button size="small" startIcon={<AddRoundedIcon />} sx={{ mt: 1 }} onClick={() => onChange([...value, isSettingObject(value[0]) ? {} : ''])}>항목 추가</Button></Card>
  }
  return <Stack spacing={2}>{Object.entries(value).map(([key, child]) => isSettingObject(child) ? <Card variant="outlined" sx={{ p: 2 }} key={key}><Typography fontWeight={800} sx={{ mb: 2 }}>{fieldLabel([...path, key])}</Typography><SettingFields value={child} path={[...path, key]} onChange={(next) => onChange({ ...value, [key]: next })} /></Card> : <SettingFields key={key} value={child} path={[...path, key]} onChange={(next) => onChange({ ...value, [key]: next })} />)}</Stack>
}

function SettingsAdmin({prefix='',title='전체 설정',description='비밀 부트스트랩 값 외 모든 운영 정책은 이곳에서 변경하고 감사 기록으로 남깁니다.'}:{prefix?:string;title?:string;description?:string}={}) {
  const { notify } = useApp(); const [items, setItems] = useState<Setting[]>([]); const [editing, setEditing] = useState<Setting | null>(null); const [value, setValue] = useState<SettingValue>({}); const [secret, setSecret] = useState(''); const [unconnected, setUnconnected] = useState<Record<string,string>>({})
  const load = () => api<{ items: Setting[]; unconnected?: Record<string,string> }>('/api/v1/admin/settings').then((data) => { setItems(data.items.filter(item=>item.key.startsWith(prefix))); setUnconnected(data.unconnected ?? {}) }).catch((error) => notify(error.message, 'error'))
  useEffect(() => { void load() }, [])
  const open = (item: Setting) => { setEditing(item); setValue(structuredClone(item.value)); setSecret('') }
  const save = async () => { if (!editing) return; try { await api(`/api/v1/admin/settings/${encodeURIComponent(editing.key)}`, { method: 'PUT', body: JSON.stringify({ value, version: editing.version, ...(editing.is_secret && secret ? { secret } : {}) }) }); notify('설정을 저장했습니다.', 'success'); setEditing(null); setSecret(''); load() } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') } }
  return <><PageTitle title={title} description={description} /><Stack spacing={2}>{items.map((item) => <Card key={item.key} sx={{ p: 2.5, cursor: 'pointer' }} onClick={() => open(item)}><Stack direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={2}><Box><Stack direction="row" gap={1} alignItems="center"><Typography variant="h4">{item.key}</Typography><Chip size="small" label={`v${item.version}`} variant="outlined" />{item.is_secret && <Chip size="small" color="warning" label={item.secret_configured ? '비밀값 설정됨' : '비밀값 없음'} />}{item.connected === false && <Chip size="small" variant="outlined" label="아직 동작에 연결되지 않음" />}</Stack><Typography variant="body2" color="text.secondary" sx={{ mt: .6 }}>{item.description}</Typography></Box><Typography variant="body2" color="text.secondary">{dateTime(item.updated_at)}</Typography></Stack></Card>)}</Stack><Dialog open={Boolean(editing)} onClose={() => setEditing(null)} fullWidth maxWidth="md"><DialogTitle>{editing?.description || editing?.key}</DialogTitle><DialogContent><Stack spacing={2} sx={{ mt: 1 }}>{editing?.connected === false && <Alert severity="info">{unconnected[editing.key] ?? '이 설정은 아직 어떤 동작에도 연결되어 있지 않습니다. 값을 바꿔도 지금은 시스템이 다르게 동작하지 않습니다.'}</Alert>}<SettingFields value={value} onChange={setValue} />{editing?.is_secret && <TextField fullWidth type="password" label={editing.secret_configured ? '새 비밀값 (변경할 때만 입력)' : '비밀값'} value={secret} onChange={(event) => setSecret(event.target.value)} helperText="원문은 다시 표시되지 않으며 AES-256-GCM으로 암호화됩니다." />}</Stack></DialogContent><DialogActions><Button onClick={() => setEditing(null)}>취소</Button><Button onClick={save} variant="contained" startIcon={<SaveRoundedIcon />}>저장</Button></DialogActions></Dialog></>
}

type ProviderForm = { id?: string; preset: string; slug: string; name: string; provider_type: string; client_id: string; client_secret: string; issuer_url: string; authorization_url: string; token_url: string; userinfo_url: string; enabled: boolean; scopes: string[]; claim_mapping: Record<string, unknown>; options: Record<string, unknown>; secret_configured?: boolean }
const initialProvider: ProviderForm = { preset: 'keycloak', slug: 'keycloak', name: 'Keycloak', provider_type: 'oidc', client_id: '', client_secret: '', issuer_url: '', authorization_url: '', token_url: '', userinfo_url: '', enabled: false, scopes: ['openid', 'profile', 'email'], claim_mapping: {}, options: {} }
const providerForm = (item: AuthProvider): ProviderForm => ({ id: item.id, preset: item.preset, slug: item.slug, name: item.name, provider_type: item.provider_type ?? 'oidc', client_id: item.client_id ?? '', client_secret: '', issuer_url: item.issuer_url ?? '', authorization_url: item.authorization_url ?? '', token_url: item.token_url ?? '', userinfo_url: item.userinfo_url ?? '', enabled: Boolean(item.enabled), scopes: item.scopes ?? [], claim_mapping: item.claim_mapping ?? {}, options: item.options ?? {}, secret_configured: item.secret_configured })
const providerRequest = (form: ProviderForm, enabled = form.enabled) => ({ slug: form.slug, name: form.name, preset: form.preset, provider_type: form.provider_type, enabled, issuer_url: form.issuer_url, authorization_url: form.authorization_url, token_url: form.token_url, userinfo_url: form.userinfo_url, client_id: form.client_id, scopes: form.scopes, claim_mapping: form.claim_mapping, options: form.options, ...(form.client_secret ? { client_secret: form.client_secret } : {}) })

function AuthAdmin() {
  const { notify } = useApp(); const [items, setItems] = useState<AuthProvider[]>([]); const [dialogOpen, setDialogOpen] = useState(false); const [form, setForm] = useState<ProviderForm>(initialProvider); const [oauthSetting, setOauthSetting] = useState<Setting | null>(null); const [callbackBase, setCallbackBase] = useState(''); const [mcpSetting, setMcpSetting] = useState<Setting | null>(null)
  const load = () => Promise.all([api<{ items: AuthProvider[] }>('/api/v1/admin/auth-providers').then((data) => setItems(data.items)), api<{ items: Setting[] }>('/api/v1/admin/settings').then((data) => { const setting = data.items.find((item) => item.key === 'auth.oauth') ?? null; setOauthSetting(setting); const object = setting?.value as Record<string, unknown> | undefined; setCallbackBase(typeof object?.callback_base_url === 'string' ? object.callback_base_url : ''); setMcpSetting(data.items.find((item) => item.key === 'mcp.oauth') ?? null) })]).catch((error) => notify(error.message, 'error'))
  useEffect(() => { void load() }, [])
  const callbackURL = (slug: string) => `${callbackBase.trim().replace(/\/$/, '') || window.location.origin}/api/v1/auth/oauth/${encodeURIComponent(slug)}/callback`
  const openCreate = () => { setForm(initialProvider); setDialogOpen(true) }
  const openEdit = (item: AuthProvider) => { setForm(providerForm(item)); setDialogOpen(true) }
  const saveProvider = async (event: FormEvent) => { event.preventDefault(); try { await api(form.id ? `/api/v1/admin/auth-providers/${form.id}` : '/api/v1/admin/auth-providers', { method: form.id ? 'PUT' : 'POST', body: JSON.stringify(providerRequest(form)) }); setDialogOpen(false); await load(); notify(form.id ? '인증 제공자 설정을 변경했습니다.' : '인증 제공자를 추가했습니다.', 'success') } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') } }
  const toggle = async (item: AuthProvider) => { try { const current = providerForm(item); await api(`/api/v1/admin/auth-providers/${item.id}`, { method: 'PUT', body: JSON.stringify(providerRequest(current, !item.enabled)) }); await load(); notify(!item.enabled ? '로그인 제공자를 활성화했습니다.' : '로그인 제공자를 중지했습니다.', 'success') } catch (cause) { notify(cause instanceof Error ? cause.message : '변경하지 못했습니다.', 'error') } }
  const remove = async (item: AuthProvider) => { if (!window.confirm(`${item.name} 인증 연동을 삭제할까요?`)) return; try { await api(`/api/v1/admin/auth-providers/${item.id}`, { method: 'DELETE' }); await load(); notify('인증 제공자를 삭제했습니다.', 'success') } catch (cause) { notify(cause instanceof Error ? cause.message : '삭제하지 못했습니다.', 'error') } }
  const saveCallbackBase = async () => { if (!oauthSetting || !isSettingObject(oauthSetting.value)) return; try { const trimmed = callbackBase.trim().replace(/\/$/, ''); if (trimmed) new URL(trimmed); await api(`/api/v1/admin/settings/${encodeURIComponent(oauthSetting.key)}`, { method: 'PUT', body: JSON.stringify({ value: { ...oauthSetting.value, callback_base_url: trimmed }, version: oauthSetting.version }) }); await load(); notify('외부 서비스 주소를 저장했습니다. Keycloak의 Valid redirect URIs에도 아래 주소를 등록하세요.', 'success') } catch (cause) { notify(cause instanceof Error ? cause.message : '외부 주소를 저장하지 못했습니다.', 'error') } }
  return <><PageTitle title="인증 연동" description="Google, Naver, Apple, Kakao와 Keycloak을 프리셋으로 연결합니다." action={<Button variant="contained" startIcon={<AddRoundedIcon />} onClick={openCreate}>인증 제공자 추가</Button>} />
    <Alert severity="info" sx={{ mb: 3 }}>리버스 프록시나 별도 도메인을 사용하면 외부 서비스 주소를 먼저 저장하고, 표시되는 콜백 주소를 Keycloak Client의 <b>Valid redirect URIs</b>에 정확히 등록하세요.</Alert>
    <Card sx={{ p: 3, mb: 3 }}><Typography variant="h3">OAuth 외부 주소</Typography><Typography variant="body2" color="text.secondary" sx={{ mt: .5, mb: 2 }}>사용자가 브라우저에서 접속하는 Kkiit 주소입니다. 비우면 프록시 헤더 또는 현재 접속 주소를 사용합니다.</Typography><Stack direction={{ xs: 'column', md: 'row' }} gap={1.5}><TextField fullWidth type="url" label="외부 서비스 주소" placeholder="https://market.example.com" value={callbackBase} onChange={(event) => setCallbackBase(event.target.value)} /><Button variant="contained" startIcon={<SaveRoundedIcon />} onClick={saveCallbackBase}>저장</Button></Stack></Card>
    {mcpSetting && <McpSsoCard setting={mcpSetting} providers={items} callbackBase={callbackBase} onSaved={load} />}
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(2,1fr)' }, gap: 2 }}>{items.map((item) => <Card key={item.id} sx={{ p: 3 }}><Stack direction="row" justifyContent="space-between" gap={2}><Box><Stack direction="row" gap={1} alignItems="center"><Typography variant="h3">{item.name}</Typography><Chip label={item.preset} size="small" />{item.options?.auto_login === true && <Chip label="자동 로그인" size="small" color="primary" variant="outlined" />}</Stack><Typography variant="body2" color="text.secondary" sx={{ mt: .7, wordBreak: 'break-all' }}>{callbackURL(item.slug)}</Typography></Box><FormControlLabel control={<Switch checked={Boolean(item.enabled)} onChange={() => toggle(item)} />} label={item.enabled ? '사용 중' : '중지'} /></Stack><Divider sx={{ my: 2 }} /><Typography variant="body2">Client ID: {item.client_id || '—'}</Typography><Typography variant="body2">Secret: {item.secret_configured ? '암호화 저장됨' : '미설정'}</Typography><Stack direction="row" gap={1} sx={{ mt: 2 }}><Button size="small" startIcon={<ContentCopyRoundedIcon />} onClick={() => { void navigator.clipboard.writeText(callbackURL(item.slug)); notify('콜백 주소를 복사했습니다.', 'success') }}>주소 복사</Button><Button size="small" startIcon={<EditRoundedIcon />} onClick={() => openEdit(item)}>편집</Button><Button size="small" color="error" startIcon={<DeleteOutlineRoundedIcon />} onClick={() => remove(item)}>삭제</Button></Stack></Card>)}</Box>
    <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} fullWidth maxWidth="sm"><Box component="form" onSubmit={saveProvider}><DialogTitle>{form.id ? '인증 제공자 편집' : '인증 제공자 추가'}</DialogTitle><DialogContent><Stack spacing={2} sx={{ mt: 1 }}><TextField select label="프리셋" value={form.preset} onChange={(event) => { const preset = event.target.value; setForm({ ...form, preset, provider_type: ['naver', 'kakao'].includes(preset) ? 'oauth2' : 'oidc', ...(!form.id ? { slug: preset, name: preset.charAt(0).toUpperCase() + preset.slice(1) } : {}) }) }}>{['google','naver','apple','kakao','keycloak','custom'].map((preset) => <MenuItem key={preset} value={preset}>{preset}</MenuItem>)}</TextField>{form.preset === 'custom' && <TextField select label="연동 방식" value={form.provider_type} onChange={(event) => setForm({ ...form, provider_type: event.target.value })}><MenuItem value="oidc">OIDC</MenuItem><MenuItem value="oauth2">OAuth 2.0</MenuItem></TextField>}<TextField label="화면 이름" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} required /><TextField label="식별자" value={form.slug} onChange={(event) => setForm({ ...form, slug: event.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, '') })} required helperText="영문 소문자, 숫자, _와 -만 사용할 수 있습니다." /><TextField label="Client ID" value={form.client_id} onChange={(event) => setForm({ ...form, client_id: event.target.value })} required /><TextField label="Client Secret" type="password" value={form.client_secret} onChange={(event) => setForm({ ...form, client_secret: event.target.value })} helperText={form.secret_configured ? '기존 비밀값을 유지하려면 비워 두세요.' : 'Public Client라면 비워 둘 수 있습니다.'} />{form.provider_type === 'oidc' ? <TextField label="Issuer URL" type="url" value={form.issuer_url} onChange={(event) => setForm({ ...form, issuer_url: event.target.value })} required={form.preset === 'keycloak' || form.preset === 'custom'} helperText="Keycloak 예: https://sso.example.com/realms/my-realm" /> : <><TextField label="Authorization URL" type="url" value={form.authorization_url} onChange={(event) => setForm({ ...form, authorization_url: event.target.value })} required={form.preset === 'custom'} /><TextField label="Token URL" type="url" value={form.token_url} onChange={(event) => setForm({ ...form, token_url: event.target.value })} required={form.preset === 'custom'} /><TextField label="Userinfo URL" type="url" value={form.userinfo_url} onChange={(event) => setForm({ ...form, userinfo_url: event.target.value })} required={form.preset === 'custom'} /></>}{form.provider_type === 'oidc' && <FormControlLabel control={<Switch checked={form.options.auto_login === true} onChange={(event) => setForm({ ...form, options: { ...form.options, auto_login: event.target.checked } })} />} label="제공자에 로그인되어 있으면 자동 로그인 (prompt=none)" />}<FormControlLabel control={<Switch checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />} label="저장 후 즉시 활성화" /></Stack></DialogContent><DialogActions><Button onClick={() => setDialogOpen(false)}>취소</Button><Button type="submit" variant="contained">{form.id ? '저장' : '추가'}</Button></DialogActions></Box></Dialog></>
}

type ApprovalPolicy = { id: string; resource_type: string; name: string; enabled: boolean; priority: number; conditions: Record<string, unknown>; steps: Record<string, unknown>[] }
type ApprovalRequest = { id: string; resource_type: string; resource_id: string; policy_name: string; context: Record<string, unknown>; created_at: string; state?: string; waiting_hours?: number }
type ApprovalForm = { id?: string; name: string; enabled: boolean; priority: number; minAmount: string; maxAmount: string; qualityBelow: string; serviceTypes: string; sellerLevels: string; approverRole: string; minApprovals: number }
const initialApproval: ApprovalForm = { name: '재능 상품 공개 검토', enabled: true, priority: 100, minAmount: '', maxAmount: '', qualityBelow: '', serviceTypes: '', sellerLevels: '', approverRole: 'operator', minApprovals: 1 }
const approvalForm = (policy: ApprovalPolicy): ApprovalForm => ({ id: policy.id, name: policy.name, enabled: policy.enabled, priority: policy.priority, minAmount: String(policy.conditions.min_amount ?? ''), maxAmount: String(policy.conditions.max_amount ?? ''), qualityBelow: String(policy.conditions.quality_score_below ?? ''), serviceTypes: Array.isArray(policy.conditions.service_types) ? policy.conditions.service_types.join(', ') : '', sellerLevels: Array.isArray(policy.conditions.seller_levels) ? policy.conditions.seller_levels.join(', ') : '', approverRole: String(policy.steps[0]?.role ?? 'operator'), minApprovals: Number(policy.steps[0]?.min_approvals ?? 1) })
// McpSsoCard is the mcp.oauth setting: /mcp opened with a Keycloak access
// token next to the personal key. The server refuses a switch that could not
// take effect (no OIDC provider, no address), so the message it returns is
// shown as is.
type McpSsoForm = { enabled: boolean; provider: string; resource: string; audience: string; scopes: string }
const mcpSsoForm = (value: SettingValue): McpSsoForm => { const object = isSettingObject(value) ? value : {}; const list = (item: SettingValue) => Array.isArray(item) ? item.filter((entry): entry is string => typeof entry === 'string').join(' ') : typeof item === 'string' ? item : ''; return { enabled: object.enabled === true, provider: typeof object.provider === 'string' ? object.provider : '', resource: typeof object.resource === 'string' ? object.resource : '', audience: list(object.audience ?? null), scopes: list(object.scopes ?? null) } }
function McpSsoCard({ setting, providers, callbackBase, onSaved }: { setting: Setting; providers: AuthProvider[]; callbackBase: string; onSaved: () => Promise<unknown> }) {
  const { notify } = useApp(); const [form, setForm] = useState<McpSsoForm>(() => mcpSsoForm(setting.value)); const [busy, setBusy] = useState(false)
  useEffect(() => { setForm(mcpSsoForm(setting.value)) }, [setting])
  const oidcProviders = providers.filter((item) => item.provider_type === 'oidc' && item.issuer_url)
  const base = callbackBase.trim().replace(/\/$/, '') || window.location.origin
  const resource = form.resource.trim().replace(/\/$/, '') || `${base}/mcp`
  const metadataURL = `${resource.replace(/\/mcp$/, '')}/.well-known/oauth-protected-resource/mcp`
  const copy = (text: string, label: string) => { void navigator.clipboard.writeText(text); notify(`${label}를 복사했습니다.`, 'success') }
  const save = async () => { setBusy(true); try { await api(`/api/v1/admin/settings/${encodeURIComponent(setting.key)}`, { method: 'PUT', body: JSON.stringify({ value: { ...(isSettingObject(setting.value) ? setting.value : {}), enabled: form.enabled, provider: form.provider, resource: form.resource.trim().replace(/\/$/, ''), audience: form.audience.split(/[\s,]+/).filter(Boolean), scopes: form.scopes.split(/[\s,]+/).filter(Boolean) }, version: setting.version }) }); await onSaved(); notify(form.enabled ? 'MCP SSO 연결을 켰습니다. MCP 클라이언트에 아래 MCP 주소만 넣으면 됩니다.' : 'MCP SSO 설정을 저장했습니다.', 'success') } catch (cause) { notify(cause instanceof Error ? cause.message : '저장하지 못했습니다.', 'error') } finally { setBusy(false) } }
  return <Card sx={{ p: 3, mb: 3 }}>
    <Stack direction="row" justifyContent="space-between" alignItems="flex-start" gap={2}><Box><Typography variant="h3">MCP 를 SSO 로 연결</Typography><Typography variant="body2" color="text.secondary" sx={{ mt: .5 }}>켜면 <code>/mcp</code> 가 개인 API 키에 더해 위 OIDC 제공자(Keycloak)가 발급한 액세스 토큰도 받습니다. MCP 클라이언트에는 주소 하나만 주면 스스로 로그인해 토큰을 받아 옵니다. 계정은 만들지 않으며, 웹으로 한 번 로그인한 활성 계정만 통과합니다.</Typography></Box><FormControlLabel control={<Switch checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />} label={form.enabled ? '사용 중' : '꺼짐'} /></Stack>
    <Divider sx={{ my: 2 }} />
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(2,1fr)' }, gap: 2 }}>
      <TextField select label="인증 서버로 쓸 제공자" value={form.provider} onChange={(event) => setForm({ ...form, provider: event.target.value })} helperText={oidcProviders.length === 0 ? 'Issuer URL 이 있는 OIDC 제공자를 먼저 추가하세요.' : '비우면 유일한 활성 OIDC 제공자를 씁니다.'}><MenuItem value="">(유일한 활성 OIDC 제공자)</MenuItem>{oidcProviders.map((item) => <MenuItem key={item.slug} value={item.slug}>{item.name} ({item.slug})</MenuItem>)}</TextField>
      <TextField label="리소스 식별자 (resource)" type="url" placeholder={`${base}/mcp`} value={form.resource} onChange={(event) => setForm({ ...form, resource: event.target.value })} helperText="클라이언트가 실제로 접속하는 공개 주소 + /mcp. 비우면 OAuth 외부 주소 + /mcp 를 씁니다." />
      <TextField label="허용 대상 (audience)" placeholder="claude-mcp cursor-mcp" value={form.audience} onChange={(event) => setForm({ ...form, audience: event.target.value })} helperText="공백 구분. 토큰의 aud 또는 azp 가 이 목록에 있으면 통과합니다. Keycloak 의 MCP 클라이언트 ID 를 적으면 Audience 매퍼 없이 됩니다." />
      <TextField label="허용 범위 (scopes)" placeholder="mcp.use orders.buy" value={form.scopes} onChange={(event) => setForm({ ...form, scopes: event.target.value })} helperText="공백 구분 키 권한. SSO 로 들어온 사람은 이 범위와 자기 역할의 교집합만 씁니다. 토큰의 role·scope 는 읽지 않습니다." />
    </Box>
    <Stack spacing={.5} sx={{ mt: 2 }}>
      <Stack direction="row" alignItems="center" gap={1}><Typography variant="body2" sx={{ wordBreak: 'break-all' }}>MCP 주소: <b>{resource}</b></Typography><IconButton size="small" aria-label="MCP 주소 복사" onClick={() => copy(resource, 'MCP 주소')}><ContentCopyRoundedIcon fontSize="inherit" /></IconButton></Stack>
      <Stack direction="row" alignItems="center" gap={1}><Typography variant="body2" sx={{ wordBreak: 'break-all' }}>메타데이터 주소: {metadataURL}</Typography><IconButton size="small" aria-label="메타데이터 주소 복사" onClick={() => copy(metadataURL, '메타데이터 주소')}><ContentCopyRoundedIcon fontSize="inherit" /></IconButton></Stack>
    </Stack>
    <Stack direction="row" gap={1} sx={{ mt: 2 }}><Button variant="contained" startIcon={<SaveRoundedIcon />} onClick={save} disabled={busy}>저장</Button></Stack>
  </Card>
}

function ApprovalAdmin() {
  const { notify } = useApp(); const [policies, setPolicies] = useState<ApprovalPolicy[]>([]); const [requests, setRequests] = useState<ApprovalRequest[]>([]); const [open, setOpen] = useState(false); const [form, setForm] = useState<ApprovalForm>(initialApproval); const [approvalSla, setApprovalSla] = useState(48); const [reviewing, setReviewing] = useState<ApprovalCase | null>(null)
  const load = () => Promise.all([api<{ items: ApprovalPolicy[] }>('/api/v1/admin/approvals/policies').then((d) => setPolicies(d.items)), api<{ items: ApprovalRequest[]; sla_hours?: number }>('/api/v1/admin/approvals/requests').then((d) => { setRequests(d.items); if (d.sla_hours) setApprovalSla(d.sla_hours) })]).catch((e) => notify(e.message, 'error')); useEffect(() => { load() }, [])
  const openCreate = () => { setForm(initialApproval); setOpen(true) }
  const openEdit = (policy: ApprovalPolicy) => { setForm(approvalForm(policy)); setOpen(true) }
  const save = async (event: FormEvent) => { event.preventDefault(); const number = (value: string) => value.trim() === '' ? undefined : Number(value); const conditions = { ...(number(form.minAmount) !== undefined ? { min_amount: number(form.minAmount) } : {}), ...(number(form.maxAmount) !== undefined ? { max_amount: number(form.maxAmount) } : {}), ...(number(form.qualityBelow) !== undefined ? { quality_score_below: number(form.qualityBelow) } : {}), ...(form.serviceTypes.trim() ? { service_types: form.serviceTypes.split(',').map((item) => item.trim()).filter(Boolean) } : {}), ...(form.sellerLevels.trim() ? { seller_levels: form.sellerLevels.split(',').map((item) => item.trim()).filter(Boolean) } : {}) }; try { await api(form.id ? `/api/v1/admin/approvals/policies/${form.id}` : '/api/v1/admin/approvals/policies', { method: form.id ? 'PUT' : 'POST', body: JSON.stringify({ resource_type: 'talent_publish', name: form.name, enabled: form.enabled, priority: form.priority, conditions, steps: [{ role: form.approverRole, min_approvals: form.minApprovals }] }) }); setOpen(false); await load(); notify(form.id ? '승인 정책을 변경했습니다.' : '승인 정책을 추가했습니다.', 'success') } catch (e) { notify(e instanceof Error ? e.message : '저장 실패', 'error') } }
  const toggle = async (policy: ApprovalPolicy) => { try { await api(`/api/v1/admin/approvals/policies/${policy.id}`, { method: 'PUT', body: JSON.stringify({ ...policy, enabled: !policy.enabled }) }); load(); notify(!policy.enabled ? '승인 절차를 활성화했습니다.' : '승인 절차를 비활성화했습니다. 이후 요청은 절차를 건너뜁니다.', 'success') } catch (e) { notify(e instanceof Error ? e.message : '저장 실패', 'error') } }
  const remove = async (policy: ApprovalPolicy) => { if (!window.confirm(`${policy.name} 정책을 삭제할까요?`)) return; try { await api(`/api/v1/admin/approvals/policies/${policy.id}`, { method: 'DELETE' }); await load(); notify('승인 정책을 삭제했습니다.', 'success') } catch (e) { notify(e instanceof Error ? e.message : '삭제 실패', 'error') } }
  const review = async (id: string) => { setReviewing(null); try { setReviewing(await api<ApprovalCase>(`/api/v1/admin/approvals/requests/${id}`)) } catch (cause) { notify(cause instanceof Error ? cause.message : '승인 요청을 불러오지 못했습니다.', 'error') } }
  const decide = async (id: string, decision: 'approved' | 'rejected') => { const note = window.prompt(decision === 'approved' ? '승인 메모(선택)' : '반려 사유') ?? ''; try { await api(`/api/v1/admin/approvals/requests/${id}/decision`, { method: 'POST', body: JSON.stringify({ decision, note }) }); load(); notify(decision === 'approved' ? '승인했습니다.' : '반려했습니다.', 'success') } catch (e) { notify(e instanceof Error ? e.message : '처리 실패', 'error') } }

  const listing = reviewing?.talent
  const reviewDialog = <Dialog open={Boolean(reviewing)} onClose={() => setReviewing(null)} fullWidth maxWidth="md">
    <DialogTitle>승인 검토 · {listing?.title ?? reviewing?.resource_type}</DialogTitle>
    <DialogContent>
      {!listing ? <Alert severity="info" sx={{ mt: 1 }}>이 요청의 대상을 펼쳐 보여줄 수 없습니다. 이미 삭제되었을 수 있습니다.</Alert> : <Stack spacing={2} sx={{ mt: 1 }}>
        <Alert severity="info">공개를 승인하면 이 내용이 그대로 마켓에 노출됩니다.</Alert>
        <Box>
          <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
            <Chip size="small" label={listing.service_type} />
            {listing.category && <Chip size="small" variant="outlined" label={listing.category} />}
            <Typography fontWeight={750}>{listing.base_price.toLocaleString()}원 · {listing.delivery_days}일 · 수정 {listing.revision_count}회</Typography>
          </Stack>
          <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>{listing.summary}</Typography>
          <Typography sx={{ mt: 1.5, whiteSpace: 'pre-wrap', maxHeight: 260, overflowY: 'auto' }}>{listing.description}</Typography>
          {listing.tags.length > 0 && <Stack direction="row" gap={.6} flexWrap="wrap" sx={{ mt: 1.5 }}>{listing.tags.map((tag) => <Chip key={tag} size="small" variant="outlined" label={tag} />)}</Stack>}
        </Box>
        {listing.packages.length > 0 && <Box>
          <Typography variant="caption" color="text.secondary">패키지</Typography>
          <Stack spacing={.5} sx={{ mt: .5 }}>{listing.packages.map((pkg, index) => <Typography key={index} variant="body2">{pkg.name} · {pkg.price.toLocaleString()}원 · {pkg.delivery_days}일</Typography>)}</Stack>
        </Box>}
        <Divider />
        <Box>
          <Typography variant="caption" color="text.secondary">판매자</Typography>
          <Typography variant="body2">{listing.seller.display_name} · {listing.seller.level} · 평점 {Number(listing.seller.rating).toFixed(1)}</Typography>
          {/* Prior rejections and reports are what decides whether this listing
              gets a careful read or a quick one. */}
          <Typography variant="body2" color={listing.seller.rejected_talents > 0 || listing.seller.reports_against > 0 ? 'warning.main' : 'text.secondary'}>
            공개 상품 {listing.seller.published_talents} · 반려 이력 {listing.seller.rejected_talents} · 이 계정에 대한 신고 {listing.seller.reports_against}
          </Typography>
        </Box>
        {reviewing && reviewing.previous.length > 0 && <Box>
          <Typography variant="caption" color="text.secondary">이 상품의 이전 결재 {reviewing.previous.length}건</Typography>
          <Stack spacing={.3} sx={{ mt: .5 }}>{reviewing.previous.map((entry, index) => <Typography key={index} variant="caption" color="text.secondary">{dateTime(entry.at ?? undefined)} · {entry.state}{entry.note ? ` · ${entry.note}` : ''}</Typography>)}</Stack>
        </Box>}
      </Stack>}
    </DialogContent>
    <DialogActions>
      <Button onClick={() => setReviewing(null)}>닫기</Button>
      {reviewing?.state === 'pending' && <>
        <Button color="error" onClick={() => { const id = reviewing.id; setReviewing(null); decide(id, 'rejected') }}>반려</Button>
        <Button variant="contained" onClick={() => { const id = reviewing.id; setReviewing(null); decide(id, 'approved') }}>승인</Button>
      </>}
    </DialogActions>
  </Dialog>

  return <>{reviewDialog}<PageTitle title="승인 정책과 대기열" description="활성 정책이 없으면 검토·승인·반려 단계는 자동으로 제외됩니다." action={<Button variant="contained" startIcon={<AddRoundedIcon />} onClick={openCreate}>정책 추가</Button>} /><Alert severity={policies.some((p) => p.enabled) ? 'info' : 'success'} sx={{ mb: 3 }}>{policies.some((p) => p.enabled) ? '활성 정책에 일치하는 요청은 관리자 검토 후 진행됩니다.' : '현재 활성 정책이 없어 새 요청은 승인 절차를 건너뜁니다.'}</Alert><Typography variant="h3" sx={{ mb: 2 }}>정책</Typography><Stack spacing={1.5}>{policies.map((policy) => <Card key={policy.id} sx={{ p: 2.5 }}><Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'center' }} gap={2}><Box><Typography variant="h4">{policy.name}</Typography><Typography variant="body2" color="text.secondary">{policy.resource_type} · 우선순위 {policy.priority}</Typography></Box><Stack direction="row" alignItems="center"><FormControlLabel control={<Switch checked={policy.enabled} onChange={() => toggle(policy)} />} label={policy.enabled ? '활성' : '비활성'} /><IconButton aria-label={`${policy.name} 편집`} onClick={() => openEdit(policy)}><EditRoundedIcon /></IconButton><IconButton aria-label={`${policy.name} 삭제`} color="error" onClick={() => remove(policy)}><DeleteOutlineRoundedIcon /></IconButton></Stack></Stack></Card>)}</Stack><Typography variant="h3" sx={{ mt: 4, mb: 2 }}>대기 중 요청</Typography><Stack spacing={1.5}>{requests.map((request) => <Card key={request.id} sx={{ p: 2.5 }}><Stack direction={{ xs: 'column', sm: 'row' }} justifyContent="space-between" alignItems={{ sm: 'center' }} gap={2}><Box><Typography variant="h4">{String(request.context.title ?? request.resource_id)}</Typography><Typography variant="body2" color="text.secondary">{request.policy_name} · {dateTime(request.created_at)}</Typography>{request.state === 'pending' && request.waiting_hours != null && <Chip size="small" sx={{ mt: .5 }} color={request.waiting_hours >= approvalSla ? 'error' : 'default'} label={request.waiting_hours >= 24 ? `대기 ${Math.floor(request.waiting_hours / 24)}일` : `대기 ${request.waiting_hours}시간`} />}</Box><Stack direction="row" gap={1}><Button size="small" onClick={() => review(request.id)}>내용 보기</Button><Button color="error" startIcon={<CancelRoundedIcon />} onClick={() => decide(request.id, 'rejected')}>반려</Button><Button variant="contained" color="success" startIcon={<CheckCircleRoundedIcon />} onClick={() => decide(request.id, 'approved')}>승인</Button></Stack></Stack></Card>)}</Stack>{requests.length === 0 && <Card sx={{ p: 4, textAlign: 'center' }}><Typography color="text.secondary">대기 중인 승인 요청이 없습니다.</Typography></Card>}<Dialog open={open} onClose={() => setOpen(false)} fullWidth maxWidth="sm"><Box component="form" onSubmit={save}><DialogTitle>{form.id ? '승인 정책 편집' : '승인 정책 추가'}</DialogTitle><DialogContent><Stack spacing={2} sx={{ mt: 1 }}><TextField fullWidth label="정책 이름" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} required /><TextField fullWidth type="number" label="우선순위" value={form.priority} onChange={(event) => setForm({ ...form, priority: Number(event.target.value) })} helperText="숫자가 작을수록 먼저 적용됩니다." /><Typography fontWeight={800}>적용 조건 (비우면 제한 없음)</Typography><Stack direction={{ xs: 'column', sm: 'row' }} gap={1.5}><TextField fullWidth type="number" label="최소 금액" value={form.minAmount} onChange={(event) => setForm({ ...form, minAmount: event.target.value })} /><TextField fullWidth type="number" label="최대 금액" value={form.maxAmount} onChange={(event) => setForm({ ...form, maxAmount: event.target.value })} /></Stack><TextField fullWidth type="number" label="품질 점수가 이 값 미만" value={form.qualityBelow} onChange={(event) => setForm({ ...form, qualityBelow: event.target.value })} /><TextField fullWidth label="서비스 유형" placeholder="HUMAN, AI, HYBRID" value={form.serviceTypes} onChange={(event) => setForm({ ...form, serviceTypes: event.target.value })} helperText="여러 값은 쉼표로 구분하세요." /><TextField fullWidth label="판매자 등급" placeholder="NEW, VERIFIED" value={form.sellerLevels} onChange={(event) => setForm({ ...form, sellerLevels: event.target.value })} helperText="여러 값은 쉼표로 구분하세요." /><Typography fontWeight={800}>승인 단계</Typography><Stack direction={{ xs: 'column', sm: 'row' }} gap={1.5}><TextField fullWidth label="승인 역할" value={form.approverRole} onChange={(event) => setForm({ ...form, approverRole: event.target.value })} required /><TextField fullWidth type="number" label="필요 승인 수" value={form.minApprovals} onChange={(event) => setForm({ ...form, minApprovals: Math.max(1, Number(event.target.value)) })} required /></Stack><FormControlLabel control={<Switch checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />} label="정책 활성화" /></Stack></DialogContent><DialogActions><Button onClick={() => setOpen(false)}>취소</Button><Button type="submit" variant="contained">저장</Button></DialogActions></Box></Dialog></>

}

type Role = { code: string; name: string; description: string; system_role: boolean; permissions: string[] }
type Permission = { code: string; name: string; description: string }
function RolesAdmin() { const { notify } = useApp(); const [roles, setRoles] = useState<Role[]>([]); const [permissions, setPermissions] = useState<Permission[]>([]); const [selected, setSelected] = useState<Role | null>(null); const [scopes, setScopes] = useState<string[]>([]); const load = () => api<{ items: Role[]; permissions: Permission[] }>('/api/v1/admin/roles').then((d) => { setRoles(d.items); setPermissions(d.permissions) }).catch((e) => notify(e.message, 'error')); useEffect(() => { void load() }, []); const open = (role: Role) => { setSelected(role); setScopes(role.permissions) }; const save = async () => { if (!selected) return; try { await api(`/api/v1/admin/roles/${selected.code}/permissions`, { method: 'PUT', body: JSON.stringify({ permissions: scopes }) }); setSelected(null); load(); notify('역할 권한을 변경했습니다. 새 요청부터 즉시 적용됩니다.', 'success') } catch (e) { notify(e instanceof Error ? e.message : '저장 실패', 'error') } }; return <><PageTitle title="역할과 키 권한" description="역할 권한을 바꾸면 개인 API 키의 유효 권한도 역할 범위 안으로 자동 제한됩니다." /><Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', lg: 'repeat(2,1fr)' }, gap: 2 }}>{roles.map((role) => <Card key={role.code} sx={{ p: 2.5 }}><Typography variant="h4">{role.name}</Typography><Typography variant="body2" color="text.secondary">{role.code} · {role.description}</Typography><Stack direction="row" flexWrap="wrap" gap={.6} sx={{ my: 2 }}>{role.permissions.slice(0, 6).map((permission) => <Chip key={permission} label={permission} size="small" variant="outlined" />)}{role.permissions.length > 6 && <Chip label={`+${role.permissions.length - 6}`} size="small" />}</Stack><Button onClick={() => open(role)}>권한 편집</Button></Card>)}</Box><Dialog open={Boolean(selected)} onClose={() => setSelected(null)} fullWidth maxWidth="md"><DialogTitle>{selected?.name} 권한</DialogTitle><DialogContent><Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2,1fr)' }, gap: 1, mt: 1 }}>{permissions.map((permission) => <FormControlLabel key={permission.code} control={<Switch checked={scopes.includes(permission.code)} onChange={(e) => setScopes(e.target.checked ? [...scopes, permission.code] : scopes.filter((item) => item !== permission.code))} />} label={<Box><Typography fontWeight={700}>{permission.name}</Typography><Typography variant="caption" color="text.secondary">{permission.code}</Typography></Box>} />)}</Box></DialogContent><DialogActions><Button onClick={() => setSelected(null)}>취소</Button><Button variant="contained" onClick={save}>저장</Button></DialogActions></Dialog></> }

type Audit = { id: string; occurred_at: string; actor_user_id?: string; actor_roles: string[]; ip?: string; action: string; resource_type: string; resource_id?: string; request_id: string; result: string }
function AuditAdmin() { const [items, setItems] = useState<Audit[]>([]); const [cursor, setCursor] = useState<string | null>(null); const [busy, setBusy] = useState(false); const [action, setAction] = useState(''); const [result, setResult] = useState(''); const { notify } = useApp(); const fetchPage = useCallback((next?: string) => { setBusy(true); api<{ items: Audit[]; next_cursor: string | null }>(`/api/v1/admin/audit?limit=50${action ? `&action=${encodeURIComponent(action)}` : ''}${result ? `&result=${result}` : ''}${next ? `&cursor=${encodeURIComponent(next)}` : ''}`).then((d) => { setItems((current) => next ? [...current, ...d.items] : d.items); setCursor(d.next_cursor) }).catch((e) => notify(e.message, 'error')).finally(() => setBusy(false)) }, [notify, action, result]); useEffect(() => { fetchPage() }, [fetchPage]); return <><PageTitle title="감사 로그" description="관리자와 보안 중요 작업의 행위자, 자원, 요청 ID를 추적합니다." />
    <Stack direction="row" gap={1.5} flexWrap="wrap" sx={{ mb: 2 }}>
      <TextField size="small" label="작업 접두사" placeholder="예: settings, order, api_key" value={action} onChange={(event) => setAction(event.target.value)} sx={{ minWidth: 220 }} />
      <TextField size="small" select label="결과" value={result} onChange={(event) => setResult(event.target.value)} sx={{ minWidth: 140 }}>
        <MenuItem value="">전체</MenuItem><MenuItem value="success">성공</MenuItem><MenuItem value="failure">실패</MenuItem>
      </TextField>
      {(action || result) && <Button onClick={() => { setAction(''); setResult('') }}>조건 지우기</Button>}
    </Stack><Stack spacing={1}>{items.map((item) => <Card key={item.id} sx={{ p: 2 }}><Stack direction={{ xs: 'column', md: 'row' }} gap={2} alignItems={{ md: 'center' }}><Typography variant="body2" color="text.secondary" sx={{ width: 170 }}>{dateTime(item.occurred_at)}</Typography><Box sx={{ flex: 1 }}><Typography fontWeight={750}>{item.action}</Typography><Typography variant="body2" color="text.secondary">{item.resource_type} {item.resource_id ?? ''} · {item.request_id}</Typography></Box><Chip size="small" color={item.result === 'success' ? 'success' : 'error'} label={item.result} /></Stack></Card>)}</Stack>{cursor && <Box sx={{ textAlign: 'center', mt: 2 }}><Button onClick={() => fetchPage(cursor)} disabled={busy}>{busy ? '불러오는 중…' : '더 보기'}</Button></Box>}{items.length === 0 && <Card sx={{ p: 4, textAlign: 'center' }}><Typography color="text.secondary">기록된 관리자 작업이 없습니다.</Typography></Card>}</> }
