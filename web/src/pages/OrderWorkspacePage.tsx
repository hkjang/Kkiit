import { FormEvent, useCallback, useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Alert, Box, Button, Card, Checkbox, Chip, CircularProgress, Container, Dialog, DialogActions, DialogContent, DialogTitle, Divider, FormControlLabel, Rating, Stack, Tab, Tabs, TextField, Typography } from '@mui/material'
import PaymentRoundedIcon from '@mui/icons-material/PaymentRounded'
import PlayArrowRoundedIcon from '@mui/icons-material/PlayArrowRounded'
import UploadFileRoundedIcon from '@mui/icons-material/UploadFileRounded'
import CheckCircleRoundedIcon from '@mui/icons-material/CheckCircleRounded'
import ReplayRoundedIcon from '@mui/icons-material/ReplayRounded'
import GavelOutlinedIcon from '@mui/icons-material/GavelOutlined'
import SendRoundedIcon from '@mui/icons-material/SendRounded'
import AttachFileRoundedIcon from '@mui/icons-material/AttachFileRounded'
import { api, dateTime, money } from '../api'
import { ReportDialog } from '../components/ReportDialog'
import { useApp } from '../App'

type Timeline = { id: string; event_type: string; from_state?: string; to_state?: string; created_at: string; data: Record<string, unknown> }
type Delivery = { id: string; version: number; delivery_type: string; content: Record<string, unknown>; description: string; created_at: string }
type Revision = { id: string; revision_number: number; details: string; priority: string; created_at: string }
type Workspace = { overdue?: boolean; overdue_days?: number; id: string; order_number: string; state: string; amount: number; discount_amount?: number; payable_amount?: number; coupon_code?: string; currency: string; requirements: Record<string,string>; due_at?: string; accepted_at?: string; created_at: string; buyer: { id: string; display_name: string }; seller: { id: string; display_name: string }; talent: { id: string; title: string }; timeline: Timeline[]; deliveries: Delivery[]; revisions: Revision[] }
type Message = { id: string; sender_id?: string; sender_name?: string; body: string; created_at: string }
type Dispute = { id: string; state: string; reason: string; opened_by_name: string; created_at: string; resolution?: { label?: string; refund_amount?: number; seller_amount?: number; note?: string } }
const states: Record<string,string> = { CREATED:'주문 생성',PAYMENT_PENDING:'결제 대기',PAID:'결제 완료',REQUIREMENT_PENDING:'요구사항 대기',READY:'작업 준비',IN_PROGRESS:'작업 중',DELIVERED:'납품 완료',REVISION_REQUESTED:'수정 요청',ACCEPTED:'구매확정',COMPLETED:'거래 완료',CANCEL_REQUESTED:'취소 요청',CANCELLED:'취소',DISPUTED:'분쟁',REFUNDED:'환불' }

export function OrderWorkspacePage(){
  const {id}=useParams();const {me,notify}=useApp();const [order,setOrder]=useState<Workspace|null>(null);const [messages,setMessages]=useState<Message[]>([]);const [tab,setTab]=useState(0);const [message,setMessage]=useState('');const [delivery,setDelivery]=useState('');const [revision,setRevision]=useState('');const [busy,setBusy]=useState(false);const [error,setError]=useState('');const [uploaded,setUploaded]=useState<{name:string;download_url:string;sha256:string}|null>(null);const [disputes,setDisputes]=useState<Dispute[]>([]);const [disputeReason,setDisputeReason]=useState('')
  // An organization manager may open an order they funded without being a party
  // to its conversation, so only the order itself is required to render.
  const [reviewOpen,setReviewOpen]=useState(false)
  const [scores,setScores]=useState({quality:5,communication:5,timeliness:5,professionalism:5})
  const [repurchase,setRepurchase]=useState(true)
  const [reviewBody,setReviewBody]=useState('')
  const load=useCallback(async()=>{try{const workspace=await api<Workspace>(`/api/v1/orders/${id}`);setOrder(workspace);const [chat,cases]=await Promise.all([api<{items:Message[]}>(`/api/v1/orders/${id}/messages`).catch(()=>({items:[] as Message[]})),api<{items:Dispute[]}>(`/api/v1/orders/${id}/disputes`).catch(()=>({items:[] as Dispute[]}))]);setMessages(chat.items);setDisputes(cases.items)}catch(e){setError(e instanceof Error?e.message:'주문을 불러오지 못했습니다.')}},[id])
  useEffect(()=>{void load();const protocol=window.location.protocol==='https:'?'wss:':'ws:';const socket=new WebSocket(`${protocol}//${window.location.host}/api/v1/orders/${id}/messages/ws`);socket.onmessage=()=>{void load()};const poll=window.setInterval(()=>{void load()},15000);return()=>{socket.close();window.clearInterval(poll)}},[id,load])
  if(error)return <Container maxWidth="lg" sx={{py:7}}><Alert severity="error">{error}</Alert></Container>;if(!order)return <Box sx={{minHeight:420,display:'grid',placeItems:'center'}}><CircularProgress/></Box>
  const buyer=me?.id===order.buyer.id;const seller=me?.id===order.seller.id;const admin=Boolean(me?.permissions.includes('orders.manage'))
  const action=async(fn:()=>Promise<unknown>,success:string)=>{setBusy(true);try{await fn();notify(success,'success');await load()}catch(e){notify(e instanceof Error?e.message:'처리하지 못했습니다.','error')}finally{setBusy(false)}}
  const transition=(to:string)=>action(()=>api(`/api/v1/orders/${id}/transition`,{method:'POST',body:JSON.stringify({to,note:''})}),`주문 상태를 ${states[to]??to}(으)로 변경했습니다.`)
  const pay=()=>action(()=>api(`/api/v1/orders/${id}/pay`,{method:'POST',headers:{'Idempotency-Key':crypto.randomUUID()},body:'{}'}),'결제를 에스크로에 보관했습니다.')
  const deliver=()=>action(()=>api(`/api/v1/orders/${id}/deliveries`,{method:'POST',body:JSON.stringify({delivery_type:'text',content:{text:delivery},description:'텍스트 납품'})}),'납품했습니다.').then(()=>setDelivery(''))
  const requestRevision=()=>action(()=>api(`/api/v1/orders/${id}/revision`,{method:'POST',body:JSON.stringify({details:revision,priority:'normal',attachments:[]})}),'수정을 요청했습니다.').then(()=>setRevision(''))
  const send=async(event:FormEvent)=>{event.preventDefault();if(!message.trim())return;await action(()=>api(`/api/v1/orders/${id}/messages`,{method:'POST',body:JSON.stringify({body:message,attachments:[]})}),'메시지를 보냈습니다.');setMessage('')}
  const openDispute=()=>action(()=>api(`/api/v1/orders/${id}/disputes`,{method:'POST',body:JSON.stringify({reason:disputeReason,evidence:[]})}),'분쟁을 접수했습니다. 운영자가 검토합니다.').then(()=>setDisputeReason(''))
  // The review form used to submit one star value for all four dimensions and a
  // fixed sentence as the buyer's words, so every review in the marketplace read
  // the same. A review nobody wrote persuades nobody.
  const review=()=>{setReviewOpen(false);return action(()=>api(`/api/v1/orders/${id}/review`,{method:'POST',body:JSON.stringify({...scores,repurchase,body:reviewBody.trim()})}),'리뷰를 등록했습니다.')}
  const upload=async(file?:File)=>{if(!file)return;const data=new FormData();data.append('file',file);await action(async()=>{const result=await api<{name:string;download_url:string;sha256:string}>(`/api/v1/orders/${id}/files`,{method:'POST',body:data});setUploaded(result);setDelivery(current=>current?`${current}\n첨부: ${result.download_url}`:`첨부: ${result.download_url}`)},'파일을 안전하게 저장했습니다.')}
  return <Container maxWidth="xl" sx={{py:{xs:3,md:5}}}><Stack direction={{xs:'column',md:'row'}} justifyContent="space-between" gap={2} sx={{mb:3}}><Box><Stack direction="row" gap={1} alignItems="center"><Typography variant="h2">{order.talent.title}</Typography><Chip color="primary" label={states[order.state]??order.state}/></Stack><Typography color="text.secondary" sx={{mt:.5}}>{order.order_number} · 납기 {dateTime(order.due_at)}</Typography></Box><Box sx={{textAlign:{md:'right'}}}><Typography variant="caption" color="text.secondary">계약 금액</Typography><Typography variant="h3">{money(order.amount,order.currency)}</Typography>{(order.discount_amount??0)>0&&<Typography variant="body2" color="text.secondary">쿠폰 {order.coupon_code} · 결제 {money(order.payable_amount??order.amount,order.currency)}</Typography>}</Box></Stack>
    <Box sx={{display:'grid',gridTemplateColumns:{xs:'1fr',lg:'minmax(0,1fr) 340px'},gap:3,alignItems:'start'}}><Card sx={{overflow:'hidden'}}><Tabs value={tab} onChange={(_,value)=>setTab(value)} variant="scrollable"><Tab label="개요"/><Tab label="대화"/><Tab label={`납품 ${order.deliveries.length}`}/><Tab label={`수정 ${order.revisions.length}`}/><Tab label="이력"/></Tabs><Divider/>
      <Box sx={{p:{xs:2.5,sm:3.5}}}>{tab===0&&<><Typography variant="h3">계약 당사자</Typography><Typography sx={{mt:1}}>구매자 {order.buyer.display_name} · 판매자 {order.seller.display_name}</Typography><Typography variant="h3" sx={{mt:3}}>확정 요구사항</Typography>{Object.keys(order.requirements??{}).length===0?<Typography color="text.secondary" sx={{mt:1}}>추가로 받은 요구사항이 없습니다.</Typography>:<Stack spacing={1.5} sx={{mt:1.5}}>{Object.entries(order.requirements).map(([label,value])=><Box key={label}><Typography variant="caption" fontWeight={750} color="text.secondary">{label}</Typography><Typography sx={{whiteSpace:'pre-wrap'}}>{String(value)}</Typography></Box>)}</Stack>}</>}
      {order.overdue&&<Alert severity="warning" sx={{mb:2}}>약속한 납기를 {order.overdue_days??0}일 지났습니다.{buyer&&order.state==='CANCEL_REQUESTED'&&(order.overdue_days??0)>=7?' 취소를 직접 마무리할 수 있습니다.':buyer?' 취소를 요청하면 납기 후 7일부터는 직접 마무리할 수 있습니다.':''}</Alert>}
      {buyer&&order.state==='CANCEL_REQUESTED'&&(order.overdue_days??0)>=7&&<Button color="error" variant="outlined" sx={{mb:2}} onClick={()=>action(()=>api(`/api/v1/orders/${id}/transition`,{method:'POST',body:JSON.stringify({to:'CANCELLED',note:'납기 초과로 구매자가 취소'})}),'주문을 취소하고 환불을 요청했습니다.')} disabled={busy}>납기 초과로 취소하고 환불</Button>}
      {tab===1&&!(buyer||seller||admin)&&<Alert severity="info">주문 대화는 구매자와 판매자만 볼 수 있습니다.</Alert>}
      {tab===1&&(buyer||seller||admin)&&<><Stack spacing={1.5} sx={{maxHeight:430,overflowY:'auto',mb:2}}>{messages.map(item=><Box key={item.id} sx={{alignSelf:item.sender_id===me?.id?'flex-end':'flex-start',maxWidth:'78%',bgcolor:item.sender_id===me?.id?'primary.light':'#f0f3f7',p:1.5,borderRadius:2}}><Typography variant="caption" color="text.secondary">{item.sender_name} · {dateTime(item.created_at)}</Typography><Typography>{item.body}</Typography></Box>)}</Stack><Box component="form" onSubmit={send}><Stack direction="row" gap={1}><TextField fullWidth value={message} onChange={e=>setMessage(e.target.value)} label="메시지"/><Button type="submit" variant="contained" aria-label="메시지 보내기"><SendRoundedIcon/></Button></Stack></Box></>}
      {tab===2&&<Stack spacing={2}>{order.deliveries.map(item=><Card key={item.id} variant="outlined" sx={{p:2,boxShadow:'none'}}><Typography fontWeight={750}>납품 v{item.version} · {dateTime(item.created_at)}</Typography><Typography sx={{mt:1}}>{item.description}</Typography><Box component="pre" sx={{whiteSpace:'pre-wrap'}}>{JSON.stringify(item.content,null,2)}</Box></Card>)}</Stack>}
      {tab===3&&<Stack spacing={2}>{order.revisions.map(item=><Alert key={item.id} severity="warning"><Typography fontWeight={750}>수정 #{item.revision_number}</Typography>{item.details}</Alert>)}</Stack>}
      {tab===4&&<Stack spacing={1.5}>{order.timeline.map(item=><Box key={item.id}><Typography fontWeight={700}>{states[item.to_state??'']??item.event_type}</Typography><Typography variant="body2" color="text.secondary">{dateTime(item.created_at)}</Typography></Box>)}</Stack>}</Box></Card>
      <Card sx={{p:3,position:{lg:'sticky'},top:96}}><Typography variant="h3">다음 작업</Typography><Typography color="text.secondary" sx={{mt:.7,mb:2.5}}>현재 상태에 맞는 작업만 표시됩니다.</Typography><Stack spacing={1.5}>
        {(buyer||admin)&&(order.state==='CREATED'||order.state==='PAYMENT_PENDING')&&<Button variant="contained" startIcon={<PaymentRoundedIcon/>} onClick={pay} disabled={busy}>결제하고 작업 준비</Button>}
        {(seller||admin)&&(order.state==='READY'||order.state==='REVISION_REQUESTED')&&<Button variant="contained" startIcon={<PlayArrowRoundedIcon/>} onClick={()=>transition('IN_PROGRESS')} disabled={busy}>작업 시작</Button>}
        {(seller||admin)&&order.state==='IN_PROGRESS'&&<><TextField multiline minRows={4} label="납품 내용" value={delivery} onChange={e=>setDelivery(e.target.value)}/><Button component="label" variant="outlined" startIcon={<AttachFileRoundedIcon/>}>결과 파일 첨부<input hidden type="file" onChange={e=>upload(e.target.files?.[0])}/></Button>{uploaded&&<Alert severity="success">{uploaded.name}<br/><Typography variant="caption">SHA-256 {uploaded.sha256}</Typography></Alert>}<Button variant="contained" startIcon={<UploadFileRoundedIcon/>} onClick={deliver} disabled={busy||!delivery.trim()}>납품하기</Button></>}
        {(buyer||admin)&&order.state==='DELIVERED'&&<><Button variant="contained" color="success" startIcon={<CheckCircleRoundedIcon/>} onClick={()=>action(()=>api(`/api/v1/orders/${id}/accept`,{method:'POST',body:'{}'}),'구매확정했습니다.')} disabled={busy}>구매확정</Button><TextField multiline minRows={3} label="수정할 내용" value={revision} onChange={e=>setRevision(e.target.value)}/><Button variant="outlined" color="warning" startIcon={<ReplayRoundedIcon/>} onClick={requestRevision} disabled={busy||!revision.trim()}>수정 요청</Button></>}
        {buyer&&order.state==='ACCEPTED'&&<Button variant="contained" onClick={()=>setReviewOpen(true)} disabled={busy}>리뷰 등록하고 완료</Button>}
        {order.state==='COMPLETED'&&<Alert severity="success">거래가 완료되었습니다.</Alert>}
        {order.state==='REFUNDED'&&<Alert severity="info">환불로 종료된 거래입니다.</Alert>}
        {(buyer||seller||admin)&&['IN_PROGRESS','DELIVERED','REVISION_REQUESTED','ACCEPTED'].includes(order.state)&&<><Divider sx={{my:1}}/><Typography fontWeight={700}>해결되지 않는 문제가 있나요?</Typography><TextField multiline minRows={3} label="분쟁 사유" value={disputeReason} onChange={e=>setDisputeReason(e.target.value)} helperText="접수하면 정산이 보류되고 운영자가 검토합니다."/><Button variant="outlined" color="error" startIcon={<GavelOutlinedIcon/>} onClick={openDispute} disabled={busy||disputeReason.trim().length<5}>분쟁 접수</Button></>}
        {(buyer||seller)&&<ReportDialog resourceType="order" resourceId={order.id} label="이 주문 신고"/>}
        {disputes.map(item=><Alert key={item.id} severity={item.state==='resolved'?'info':'warning'} sx={{textAlign:'left'}}><Typography fontWeight={750}>{item.state==='resolved'?`분쟁 처리 완료 · ${item.resolution?.label??''}`:'분쟁 검토 중'}</Typography><Typography variant="body2">{item.reason}</Typography>{item.resolution&&<Typography variant="caption" sx={{display:'block',mt:.5}}>환불 {money(item.resolution.refund_amount??0,order.currency)} · 판매자 지급 {money(item.resolution.seller_amount??0,order.currency)}{item.resolution.note?` · ${item.resolution.note}`:''}</Typography>}<Typography variant="caption" color="text.secondary">{item.opened_by_name} · {dateTime(item.created_at)}</Typography></Alert>)}
      </Stack></Card></Box>    <Dialog open={reviewOpen} onClose={()=>setReviewOpen(false)} fullWidth maxWidth="sm">
      <DialogTitle>서비스 평가</DialogTitle>
      <DialogContent>
        <Stack spacing={2} sx={{mt:1}}>
          {([['quality','결과물 품질'],['communication','소통'],['timeliness','납기 준수'],['professionalism','전문성']] as const).map(([key,label])=>(
            <Stack key={key} direction="row" justifyContent="space-between" alignItems="center" gap={2}>
              <Typography>{label}</Typography>
              <Rating value={scores[key]} onChange={(_,value)=>setScores({...scores,[key]:value??5})}/>
            </Stack>
          ))}
          <FormControlLabel control={<Checkbox checked={repurchase} onChange={(event)=>setRepurchase(event.target.checked)}/>} label="다시 의뢰할 의향이 있습니다"/>
          <TextField multiline minRows={4} label="다른 구매자에게 도움이 될 내용" value={reviewBody} onChange={(event)=>setReviewBody(event.target.value)} helperText="상품 페이지에 공개되며, 이름은 가운데를 가려 표시됩니다."/>
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={()=>setReviewOpen(false)}>취소</Button>
        <Button variant="contained" onClick={review} disabled={busy}>등록하고 거래 완료</Button>
      </DialogActions>
    </Dialog>
</Container>
}
