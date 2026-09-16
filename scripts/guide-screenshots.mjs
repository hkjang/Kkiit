#!/usr/bin/env node
// Fills a throwaway Kkiit with demo data and photographs the marketplace and
// the operator console for docs/USER_GUIDE.md and docs/ADMIN_GUIDE.md.
//
// This script writes. It registers users, publishes talents, places and pays
// orders, opens a dispute, files a report and creates a coupon and an approval
// policy, so pointing it at a deployment somebody uses would leave all of that
// in their ledger and audit trail. Two things keep that from happening by
// accident:
//
//   - the target comes from KKIIT_GUIDE_URL, a variable no other script here
//     reads, and there is no default;
//   - the target must be a loopback address. A capture runs against an
//     instance started for the capture and thrown away afterwards, so refusing
//     everything else costs nothing and removes the whole class of mistake.
//
// It only ever creates new objects, and every step looks for what it made last
// time before making it again, so a rerun against the same database adds
// nothing but one mail delivery row (the test send it presses for the mail
// screen). The two settings it saves, the mail relay and visitor tracking, are
// saved with the same values every run and would have to be reset by hand on
// a database somebody kept — another reason the target is loopback only. The
// one global it adds, an approval policy, cannot be deleted once a request
// has gone through it (the server refuses with policy_in_use), which is why
// it is looked up by name instead.
//
//   POSTGRES_DSN=... BOOTSTRAP_ADMIN=admin@example.com BOOTSTRAP_ADMIN_PASSWORD=... \
//     ENCRYPTION_KEY=... SHUTDOWN_DRAIN_SECONDS=0 ./bin/kkiit &
//   KKIIT_GUIDE_URL=http://127.0.0.1:8080 \
//   KKIIT_GUIDE_ADMIN=admin@example.com KKIIT_GUIDE_ADMIN_PASSWORD=... \
//   KKIIT_GUIDE_DEMO_PASSWORD=... \
//     node scripts/guide-screenshots.mjs
//
// Screenshots land in docs/assets/guide/ at 1440x900, the desktop size the
// guide standard fixes. Credentials come from the environment only: the demo
// accounts are created with KKIIT_GUIDE_DEMO_PASSWORD, never a literal here.
import { execFileSync, spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outputDir = path.join(repoRoot, 'docs', 'assets', 'guide')

const VIEWPORT = { width: 1440, height: 900 }

function fail(message) {
  console.error(message)
  process.exit(1)
}

const rawTarget = (process.env.KKIIT_GUIDE_URL ?? '').trim()
if (!rawTarget) {
  fail('KKIIT_GUIDE_URL is not set. Start a throwaway Kkiit and point this at it, for example\n' +
    '  KKIIT_GUIDE_URL=http://127.0.0.1:8080 node scripts/guide-screenshots.mjs')
}
let target
try {
  target = new URL(rawTarget)
} catch {
  fail(`KKIIT_GUIDE_URL is not a URL: ${rawTarget}`)
}
if (!['127.0.0.1', 'localhost', '[::1]', '::1'].includes(target.hostname)) {
  fail(`KKIIT_GUIDE_URL must be a loopback address; this script seeds demo data and must not reach a real deployment (${target.hostname})`)
}
const base = target.origin

const adminUser = (process.env.KKIIT_GUIDE_ADMIN ?? '').trim()
const adminPassword = process.env.KKIIT_GUIDE_ADMIN_PASSWORD ?? ''
const demoPassword = process.env.KKIIT_GUIDE_DEMO_PASSWORD ?? ''
if (!adminUser || !adminPassword) {
  fail('KKIIT_GUIDE_ADMIN and KKIIT_GUIDE_ADMIN_PASSWORD are required (the bootstrap administrator of the throwaway instance).')
}
if (demoPassword.length < 12) {
  fail('KKIIT_GUIDE_DEMO_PASSWORD is required (12+ characters); it becomes the password of the demo buyer and sellers.')
}

// --- the REST API, as a browser session -------------------------------------

class Session {
  constructor() {
    this.cookies = new Map()
  }

  header() {
    return [...this.cookies].map(([name, value]) => `${name}=${value}`).join('; ')
  }

  absorb(response) {
    for (const raw of response.headers.getSetCookie()) {
      const [pair] = raw.split(';')
      const index = pair.indexOf('=')
      const name = pair.slice(0, index).trim()
      const value = pair.slice(index + 1).trim()
      if (value) this.cookies.set(name, value)
      else this.cookies.delete(name)
    }
  }

  async request(method, urlPath, body, extraHeaders = {}) {
    const headers = { Cookie: this.header(), ...extraHeaders }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    const response = await fetch(base + urlPath, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      redirect: 'manual',
    })
    this.absorb(response)
    const text = await response.text()
    let parsed
    try {
      parsed = text ? JSON.parse(text) : undefined
    } catch {
      parsed = undefined
    }
    return { status: response.status, body: parsed, text }
  }

  async must(method, urlPath, body, extraHeaders) {
    const result = await this.request(method, urlPath, body, extraHeaders)
    if (result.status >= 300) throw new Error(`${method} ${urlPath} -> ${result.status} ${result.text}`)
    return result.body
  }

  async login(username, password) {
    await this.must('POST', '/api/v1/auth/login', { username, password })
    return this
  }

  async register(username, displayName, password) {
    // A rerun against the same database finds the account already there; the
    // second attempt signs in instead so the seed stays idempotent.
    const result = await this.request('POST', '/api/v1/auth/register', {
      username, email: `${username}@example.com`, display_name: displayName, password,
    })
    if (result.status === 201) return this
    return this.login(username, password)
  }
}

// --- demo data -------------------------------------------------------------

const TALENTS = [
  {
    title: '브랜드 로고 · 심볼 디자인', category: 'design-brand', service_type: 'HUMAN', base_price: 350000, delivery_days: 5,
    summary: '스타트업과 소상공인을 위한 로고와 심볼을 3안으로 제안하고, 선택안을 벡터 원본까지 정리해 드립니다.',
    description: '시안 3종 → 피드백 2회 → 최종 벡터(AI·SVG) 납품. 명함·SNS 프로필용 파생 파일을 함께 드립니다.',
    tags: ['로고', '브랜딩', 'CI'], packages: [['BASIC', '로고 1안', 350000, 5], ['STANDARD', '로고 3안 + 명함', 550000, 7], ['PREMIUM', '브랜드 가이드 포함', 1200000, 14]],
  },
  {
    title: 'Go 백엔드 API 설계·구현', category: 'development', service_type: 'HUMAN', base_price: 1800000, delivery_days: 14,
    summary: 'PostgreSQL 기반 REST API를 설계부터 테스트까지 구현합니다. OpenAPI 명세와 통합 테스트를 함께 납품합니다.',
    description: '요구사항 정리 → 스키마·API 설계 리뷰 → 구현 → 테스트·문서 납품. 도커 이미지로 바로 배포할 수 있게 드립니다.',
    tags: ['Go', 'PostgreSQL', 'REST'], packages: [['BASIC', '엔드포인트 10개 이내', 1800000, 14], ['STANDARD', '인증·권한 포함', 3200000, 21]],
  },
  {
    title: '제품 소개 영상 편집 (60초)', category: 'media-edit', service_type: 'HUMAN', base_price: 280000, delivery_days: 4,
    summary: '촬영 원본을 받아 자막·모션 그래픽·배경음악까지 넣은 60초 소개 영상을 편집합니다.',
    description: '컷 편집, 자막, 로고 모션, 저작권 안전 BGM. 세로형(9:16) 버전을 옵션으로 추가할 수 있습니다.',
    tags: ['영상', '편집', '숏폼'], packages: [['BASIC', '60초 가로형', 280000, 4], ['STANDARD', '가로형 + 세로형', 420000, 6]],
  },
  {
    title: '데이터 분석 리포트 (엑셀·CSV)', category: 'dev-data', service_type: 'HUMAN', base_price: 450000, delivery_days: 7,
    summary: '매출·고객 데이터를 받아 핵심 지표와 인사이트를 정리한 리포트를 만듭니다. 차트와 원본 노트북을 함께 드립니다.',
    description: '데이터 정리 → 탐색 분석 → 지표 정의 → 리포트(PDF)와 재현 가능한 노트북 납품.',
    tags: ['데이터', '분석', '리포트'], packages: [['BASIC', '단일 데이터셋', 450000, 7]],
  },
  {
    title: 'AI 고객 문의 자동 분류 에이전트', category: 'consulting-tech', service_type: 'AI', base_price: 90000, delivery_days: 1,
    summary: '고객 문의 텍스트를 받아 카테고리·긴급도·담당 팀으로 분류한 결과를 JSON으로 돌려주는 AI Agent 서비스입니다.',
    description: '문의 1,000건까지 처리. 분류 기준표를 요구사항으로 받아 프롬프트에 반영합니다.',
    tags: ['AI', '분류', '자동화'], packages: [['BASIC', '문의 1,000건', 90000, 1], ['STANDARD', '문의 5,000건', 350000, 2]],
  },
  {
    title: '기술 문서 · 사용자 가이드 작성', category: 'content-writing', service_type: 'HUMAN', base_price: 600000, delivery_days: 10,
    summary: '제품을 직접 써 보고 설치 가이드, 사용자 가이드, FAQ를 한국어로 작성합니다. 화면 캡처를 포함합니다.',
    description: '제품 파악 → 목차 합의 → 초안 → 검수 반영 → Markdown·PDF 납품.',
    tags: ['문서', '가이드', '테크니컬라이팅'], packages: [['BASIC', '가이드 1종', 600000, 10], ['STANDARD', '사용자 + 관리자 가이드', 1000000, 15]],
  },
]

function talentPayload(spec, categoryID) {
  return {
    title: spec.title, summary: spec.summary, description: spec.description,
    service_type: spec.service_type, base_price: spec.base_price, delivery_days: spec.delivery_days, currency: 'KRW', revision_count: 2,
    category_id: categoryID,
    scope_included: ['요구사항 정리', '중간 보고 1회'], scope_excluded: ['인쇄·제작 비용'], deliverables: ['최종 결과물', '원본 파일'],
    tags: spec.tags, faq: [{ question: '작업 시작 전에 무엇을 준비하나요?', answer: '참고 자료와 목표를 요구사항 양식에 적어 주세요.' }],
    refund_policy: '작업 시작 전 전액 환불', instant_order: true, quote_required: false, subscription_enabled: false,
    packages: spec.packages.map(([type, name, price, days], index) => ({
      package_type: type, name, description: name, price, delivery_days: days, revision_count: 2, features: [], deliverables: [], sort_order: index, active: true,
    })),
    options: [{ name: '소스 파일 추가 제공', description: '작업 원본 파일을 함께 드립니다.', price: Math.round(spec.base_price * 0.2), additional_days: 1, sort_order: 0, active: true }],
    requirements: [
      { label: '작업 목표와 참고 자료', help_text: '무엇을 만들고 싶은지, 참고할 링크나 파일이 있으면 적어 주세요.', field_type: 'textarea', required: true, options: [], validation: {}, sort_order: 0 },
      { label: '희망 완료일', help_text: '', field_type: 'text', required: false, options: [], validation: {}, sort_order: 1 },
    ],
  }
}

async function seed() {
  const admin = await new Session().login(adminUser, adminPassword)

  // Mail goes first so the orders below leave delivery rows for the mail
  // screen. The relay is a name that does not resolve: the screen is about the
  // record every attempt leaves and the reason a relay gave, and a throwaway
  // instance has no relay to deliver to. Saving is idempotent (no version, so
  // no conflict) and a rerun changes nothing.
  await admin.must('PUT', '/api/v1/admin/settings/mail', {
    value: {
      enabled: true, smtp_host: 'mail.corp.example', smtp_port: 25, security: 'auto', skip_tls_verify: false, username: '',
      from_address: 'kkiit@corp.example', from_name: 'Kkiit', base_url: base, timeout_seconds: 5,
      notify_order_paid: true, notify_order_delivered: true, notify_revision_requested: true, notify_quote: true, notify_dispute: true, notify_settlement_held: true,
    },
  })
  // Visitor tracking, Momento through the same-origin proxy. The collector
  // does not exist either; the browser asks Kkiit for /momento/tracker.js and
  // gets a 502, which no page shows. One blocked origin is reported the way a
  // browser would so the tracking screen has something to allow.
  await admin.must('PUT', '/api/v1/admin/settings/analytics.tracking', {
    value: {
      enabled: true, provider: 'momento', momento_url: 'https://momento.corp.example', momento_site_id: 'kkiit-market', momento_proxy: true,
      measurement_id: '', matomo_url: '', matomo_site_id: '', custom_snippet: '', allowed_hosts: '', include_admin: false, placement: 'head',
    },
  })
  await admin.must('POST', '/api/v1/analytics/csp-report', {
    'csp-report': { 'blocked-uri': 'https://static.corp.example/momento/plugins/heatmap.js', 'effective-directive': 'script-src', 'document-uri': `${base}/` },
  })

  const categories = (await admin.must('GET', '/api/v1/categories')).items ?? []
  const categoryBySlug = new Map(categories.map((item) => [item.slug, item.id]))
  const categoryFor = (slug) => categoryBySlug.get(slug) ?? categories[0]?.id

  // Two sellers so the marketplace, the seller pages and the admin user list
  // have more than one name on them.
  const sellerA = await new Session().register('hong-studio', '홍 스튜디오', demoPassword)
  await sellerA.must('PUT', '/api/v1/me/seller-profile', {
    seller_type: 'individual', headline: '브랜드 디자인과 영상 편집을 함께 하는 1인 스튜디오', biography: '10년차 브랜드 디자이너. 스타트업 40여 곳의 로고와 소개 영상을 만들었습니다.',
    skills: ['로고', '브랜딩', '영상 편집'], capacity: 5, settings: {},
  })
  const sellerB = await new Session().register('demo-devlab', '데모 개발연구소', demoPassword)
  await sellerB.must('PUT', '/api/v1/me/seller-profile', {
    seller_type: 'team', headline: 'Go·PostgreSQL 백엔드와 AI Agent를 만드는 소규모 개발팀', biography: '세 명의 백엔드 엔지니어가 API 설계부터 배포까지 맡습니다.',
    skills: ['Go', 'PostgreSQL', 'AI Agent'], capacity: 3, settings: {},
  })

  const talentIDs = []
  const ownerOf = (index) => (index === 1 || index === 4 ? sellerB : sellerA)
  for (const [index, spec] of TALENTS.entries()) {
    const owner = ownerOf(index)
    const existing = ((await owner.must('GET', '/api/v1/me/talents')).items ?? []).find((item) => item.title === spec.title)
    if (existing) { talentIDs.push(existing.id); continue }
    const created = await owner.must('POST', '/api/v1/talents', talentPayload(spec, categoryFor(spec.category)))
    await owner.must('POST', `/api/v1/talents/${created.id}/publish`)
    talentIDs.push(created.id)
  }

  const buyer = await new Session().register('kim-buyer', '김바다', demoPassword)
  await buyer.must('PATCH', '/api/v1/me', { display_name: '김바다', locale: 'ko-KR', timezone: 'Asia/Seoul', profile: { company: '데모 회사', bio: '데모 회사 마케팅팀. 로고와 소개 영상을 의뢰합니다.' } })
  const buyerMe = await buyer.must('GET', '/api/v1/me')

  const coupon = await admin.request('POST', '/api/v1/admin/coupons', {
    code: 'WELCOME10', name: '첫 주문 10% 할인', discount_type: 'percent', discount_value: 10, min_order_amount: 100000, max_discount_amount: 50000,
    per_user_limit: 1, usage_limit: 100, active: true,
  })
  if (coupon.status >= 300 && coupon.status !== 409) throw new Error(`coupon: ${coupon.status} ${coupon.text}`)

  const existingOrders = (await buyer.must('GET', '/api/v1/orders')).items ?? []
  const placeOrder = async (talentIndex, requirements, extra = {}) => {
    const order = await buyer.must('POST', '/api/v1/orders', { talent_id: talentIDs[talentIndex], requirements, options: [], ...extra })
    return order.id
  }
  const pay = (orderID) => buyer.must('POST', `/api/v1/orders/${orderID}/pay`, {}, { 'Idempotency-Key': `guide-${orderID}` })
  const move = (session, orderID, to) => session.must('POST', `/api/v1/orders/${orderID}/transition`, { to, note: '' })

  let workspaceOrder
  let completedOrder
  if (existingOrders.length === 0) {
    // Order 1: finished end to end — paid, delivered, accepted, reviewed.
    completedOrder = await placeOrder(0, { '작업 목표와 참고 자료': '데모 회사의 새 로고. 파란 계열, 심플한 심볼을 원합니다.', '희망 완료일': '다음 주 금요일' }, { coupon_code: 'WELCOME10' })
    await pay(completedOrder)
    await move(sellerA, completedOrder, 'IN_PROGRESS')
    await sellerA.must('POST', `/api/v1/orders/${completedOrder}/messages`, { body: '안녕하세요, 참고 자료 잘 받았습니다. 시안 3종을 목요일까지 보내 드리겠습니다.', attachments: [] })
    await buyer.must('POST', `/api/v1/orders/${completedOrder}/messages`, { body: '감사합니다. 심볼은 파란 계열로 부탁드려요.', attachments: [] })
    await sellerA.must('POST', `/api/v1/orders/${completedOrder}/deliveries`, { delivery_type: 'text', content: { text: '로고 시안 3종과 최종 벡터 파일(AI·SVG)입니다. 명함용 파생 파일도 포함했습니다.' }, description: '최종 로고 납품' })
    await buyer.must('POST', `/api/v1/orders/${completedOrder}/accept`, {})
    await buyer.must('POST', `/api/v1/orders/${completedOrder}/review`, { quality: 5, communication: 5, timeliness: 4, professionalism: 5, repurchase: true, body: '피드백 반영이 빠르고 결과물 품질이 좋았습니다. 다음 프로젝트도 맡기고 싶어요.' })

    // Order 2: in progress with a conversation — the order workspace picture.
    workspaceOrder = await placeOrder(2, { '작업 목표와 참고 자료': '신제품 출시 소개 영상. 촬영 원본 12개, 자막은 한국어, 마지막에 로고 모션.', '희망 완료일': '이번 달 말' })
    await pay(workspaceOrder)
    await move(sellerA, workspaceOrder, 'IN_PROGRESS')
    await buyer.must('POST', `/api/v1/orders/${workspaceOrder}/messages`, { body: '촬영 원본은 공유 드라이브 링크로 보냈습니다. 확인 부탁드립니다.', attachments: [] })
    await sellerA.must('POST', `/api/v1/orders/${workspaceOrder}/messages`, { body: '받았습니다. 1차 컷 편집본을 내일 오후에 공유하겠습니다.', attachments: [] })
    await buyer.must('POST', `/api/v1/orders/${workspaceOrder}/messages`, { body: '좋습니다. 자막 폰트는 고딕 계열로 부탁드려요.', attachments: [] })

    // Order 3: delivered and disputed — feeds the admin risk queue.
    const disputed = await placeOrder(3, { '작업 목표와 참고 자료': '최근 6개월 매출 CSV 분석. 지역별·상품별 추이가 필요합니다.' })
    await pay(disputed)
    await move(sellerA, disputed, 'IN_PROGRESS')
    await sellerA.must('POST', `/api/v1/orders/${disputed}/deliveries`, { delivery_type: 'text', content: { text: '분석 리포트 초안입니다.' }, description: '리포트 초안' })
    await buyer.must('POST', `/api/v1/orders/${disputed}/disputes`, { reason: '요구한 지역별 추이가 리포트에 빠져 있습니다. 재작업이 필요합니다.', evidence: [] })

    // Order 4: created but not yet paid, so the order list shows that state too.
    await placeOrder(5, { '작업 목표와 참고 자료': '사내 도구의 사용자 가이드. 화면 20개 정도, 캡처 포함.' })
  } else {
    completedOrder = existingOrders.find((item) => ['ACCEPTED', 'COMPLETED'].includes(item.state))?.id
    workspaceOrder = existingOrders.find((item) => item.state === 'IN_PROGRESS')?.id ?? existingOrders[0].id
  }

  // Around the orders: an inquiry, a favourite, an RFQ with a quote, a report,
  // an organisation with a budget and an API key.
  const inquiries = (await buyer.must('GET', '/api/v1/me/inquiries')).items ?? []
  if (inquiries.length === 0) {
    const inquiry = await buyer.must('POST', `/api/v1/talents/${talentIDs[1]}/inquiries`, { body: '기존 Node.js API를 Go로 옮기는 작업도 가능한가요? 엔드포인트는 25개 정도입니다.' })
    await sellerB.must('POST', `/api/v1/inquiries/${inquiry.id}/messages`, { body: '네, 가능합니다. STANDARD 패키지로 견적을 드릴 수 있어요. 기존 API 문서를 공유해 주시겠어요?' })
  }
  await buyer.must('POST', `/api/v1/talents/${talentIDs[1]}/favorite`)
  await buyer.must('POST', `/api/v1/talents/${talentIDs[4]}/favorite`)

  const rfqs = (await buyer.must('GET', '/api/v1/rfqs')).items ?? []
  if (rfqs.length === 0) {
    const rfq = await buyer.must('POST', '/api/v1/rfqs', {
      title: '사내 포털 백엔드 재구축', description: '레거시 PHP 백엔드를 Go + PostgreSQL로 재구축합니다. 인증은 사내 OIDC와 연동해야 합니다.',
      requirements: { '규모': '엔드포인트 40개', '연동': 'OIDC, 기존 DB 이관' }, budget_min: 5000000, budget_max: 9000000, currency: 'KRW',
    })
    await sellerB.must('POST', '/api/v1/quotes', { rfq_id: rfq.id, amount: 7800000, currency: 'KRW', delivery_days: 45, scope: { '포함': '설계·구현·이관 스크립트·테스트', '제외': '프런트엔드' }, milestones: [{ name: '설계 검토', ratio: 30 }, { name: '구현', ratio: 50 }, { name: '이관·인수', ratio: 20 }], talent_id: talentIDs[1] })
  }

  const reports = (await buyer.must('GET', '/api/v1/me/reports')).items ?? []
  if (reports.length === 0) {
    await buyer.must('POST', '/api/v1/reports', { resource_type: 'talent', resource_id: talentIDs[4], reason: 'inappropriate', details: '상품 설명의 처리 건수가 실제 상세 설명과 다릅니다. 확인 부탁드립니다.', evidence: [] })
  }

  const organizations = (await buyer.must('GET', '/api/v1/me/organizations')).items ?? []
  if (organizations.length === 0) {
    const org = await buyer.must('POST', '/api/v1/organizations', { name: '데모 회사', slug: 'demo-company' })
    await buyer.must('POST', `/api/v1/organizations/${org.id}/budgets`, { name: '2026년 하반기 마케팅', amount: 20000000, currency: 'KRW', starts_at: '2026-07-01T00:00:00+09:00', ends_at: '2026-12-31T23:59:59+09:00' })
  }

  const keys = (await buyer.must('GET', '/api/v1/me/api-keys')).items ?? []
  if (keys.length === 0) {
    await buyer.must('POST', '/api/v1/me/api-keys', { name: '사내 자동화 스크립트', scopes: ['orders.buy'], allowed_cidrs: [], rate_limit_per_minute: 60 })
  }

  // The approval queue is empty unless a policy asks for review.
  const policyName = '고가 상품 공개 검토'
  const policies = (await admin.must('GET', '/api/v1/admin/approvals/policies')).items ?? []
  if (!policies.some((item) => item.name === policyName)) {
    await admin.must('POST', '/api/v1/admin/approvals/policies', {
      resource_type: 'talent_publish', name: policyName, enabled: true, priority: 10,
      conditions: { min_amount: 1000000 }, steps: [{ role: 'operator', min_approvals: 1 }],
    })
  }
  const pendingTitle = '쇼핑몰 구축 (결제·배송 연동)'
  const pendingExists = ((await sellerB.must('GET', '/api/v1/me/talents')).items ?? []).some((item) => item.title === pendingTitle)
  if (!pendingExists) {
    const pending = await sellerB.must('POST', '/api/v1/talents', talentPayload({
      title: pendingTitle, category: 'development', service_type: 'HUMAN', base_price: 4500000, delivery_days: 30,
      summary: '상품 등록부터 결제·배송 연동까지 갖춘 쇼핑몰을 구축합니다.', description: '요구사항 정리 → 설계 → 구현 → 결제·배송사 연동 → 인수 테스트.',
      tags: ['쇼핑몰', '결제'], packages: [['BASIC', '기본 쇼핑몰', 4500000, 30]],
    }, categoryFor('development')))
    await sellerB.must('POST', `/api/v1/talents/${pending.id}/publish`)
  }

  await admin.must('POST', '/api/v1/admin/risk/rescan', {})
  const notes = (await admin.must('GET', `/api/v1/admin/notes?subject_type=user&subject_id=${buyerMe.id}`)).items ?? []
  if (notes.length === 0) {
    await admin.must('POST', '/api/v1/admin/notes', { subject_type: 'user', subject_id: buyerMe.id, body: '첫 주문에서 쿠폰 사용. 분쟁 1건은 판매자와 재작업 협의 중.', pinned: true })
  }

  return { admin, buyer, sellerA, sellerB, talentIDs, workspaceOrder, completedOrder, buyerID: buyerMe.id }
}

// --- Chrome, over the DevTools protocol --------------------------------------

const chromeBinary = ['google-chrome', 'chromium', 'chromium-browser', 'google-chrome-stable'].find((candidate) => {
  try {
    execFileSync('command', ['-v', candidate], { shell: true, stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})
if (!chromeBinary) fail('Chrome/Chromium was not found.')

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

async function launchChrome() {
  const profile = mkdtempSync(path.join(tmpdir(), 'kkiit-guide-chrome-'))
  const child = spawn(chromeBinary, [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--hide-scrollbars',
    '--disable-dev-shm-usage', '--force-color-profile=srgb', '--font-render-hinting=none',
    '--lang=ko-KR',
    `--window-size=${VIEWPORT.width},${VIEWPORT.height}`,
    `--user-data-dir=${profile}`, '--remote-debugging-port=0', 'about:blank',
  ], { stdio: 'ignore' })
  const portFile = path.join(profile, 'DevToolsActivePort')
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (existsSync(portFile)) {
      const [port] = readFileSync(portFile, 'utf8').split('\n')
      if (port) {
        const version = await fetch(`http://127.0.0.1:${port}/json/version`).then((r) => r.json())
        return { child, profile, webSocketDebuggerUrl: version.webSocketDebuggerUrl }
      }
    }
    await sleep(100)
  }
  child.kill()
  throw new Error('Chrome never reported a DevTools port')
}

class CDP {
  constructor(socket) {
    this.socket = socket
    this.nextID = 1
    this.pending = new Map()
    this.listeners = []
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(event.data)
      if (message.id && this.pending.has(message.id)) {
        const { resolve, reject } = this.pending.get(message.id)
        this.pending.delete(message.id)
        if (message.error) reject(new Error(`${message.error.message} (${JSON.stringify(message.error)})`))
        else resolve(message.result)
        return
      }
      for (const listener of this.listeners) listener(message)
    })
  }

  static async connect(url) {
    const socket = new WebSocket(url)
    await new Promise((resolve, reject) => {
      socket.addEventListener('open', resolve, { once: true })
      socket.addEventListener('error', reject, { once: true })
    })
    return new CDP(socket)
  }

  send(method, params = {}, sessionId) {
    const id = this.nextID++
    const message = { id, method, params }
    if (sessionId) message.sessionId = sessionId
    this.socket.send(JSON.stringify(message))
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }))
  }

  once(method, sessionId, timeoutMs = 30000) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.listeners = this.listeners.filter((entry) => entry !== listener)
        reject(new Error(`timed out waiting for ${method}`))
      }, timeoutMs)
      const listener = (message) => {
        if (message.method !== method) return
        if (sessionId && message.sessionId !== sessionId) return
        clearTimeout(timer)
        this.listeners = this.listeners.filter((entry) => entry !== listener)
        resolve(message.params)
      }
      this.listeners.push(listener)
    })
  }
}

class Page {
  constructor(cdp, sessionId) {
    this.cdp = cdp
    this.sessionId = sessionId
  }

  static async open(cdp) {
    const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' })
    const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true })
    const page = new Page(cdp, sessionId)
    await page.send('Page.enable')
    await page.send('Network.enable')
    await page.send('Runtime.enable')
    await page.send('Emulation.setDeviceMetricsOverride', {
      width: VIEWPORT.width, height: VIEWPORT.height, deviceScaleFactor: 1, mobile: false,
    })
    return page
  }

  send(method, params) {
    return this.cdp.send(method, params, this.sessionId)
  }

  async signInAs(session) {
    await this.send('Network.clearBrowserCookies')
    for (const [name, value] of session.cookies) {
      await this.send('Network.setCookie', { name, value, url: base, path: '/' })
    }
  }

  async evaluate(expression) {
    const result = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
    if (result.exceptionDetails) throw new Error(`evaluate failed: ${result.exceptionDetails.text}`)
    return result.result.value
  }

  async goto(urlPath) {
    const loaded = this.cdp.once('Page.loadEventFired', this.sessionId)
    await this.send('Page.navigate', { url: base + urlPath })
    await loaded
    await this.settle()
  }

  // A spinner frozen into a screenshot is a retake, so the wait is for the
  // page to have stopped showing one rather than for a fixed delay.
  async settle() {
    for (let attempt = 0; attempt < 100; attempt += 1) {
      const busy = await this.evaluate(
        "document.querySelectorAll('.MuiCircularProgress-root, .MuiSkeleton-root').length",
      )
      if (busy === 0) break
      await sleep(100)
    }
    await sleep(800)
  }

  async click(selector) {
    const found = await this.evaluate(`(() => { const el = document.querySelector(${JSON.stringify(selector)}); if (!el) return false; el.click(); return true })()`)
    if (!found) throw new Error(`nothing matches ${selector}`)
    await this.settle()
  }

  // Clicks the first element whose visible text is exactly `text`, which is how
  // a person finds a tab or a button.
  async clickText(text, tag = 'button, [role="tab"], a') {
    const found = await this.evaluate(`(() => {
      const nodes = [...document.querySelectorAll(${JSON.stringify(tag)})]
      const el = nodes.find((node) => node.textContent.trim() === ${JSON.stringify(text)})
      if (!el) return false
      el.click(); return true
    })()`)
    if (!found) throw new Error(`no element with text ${text}`)
    await this.settle()
  }

  async type(selector, value) {
    await this.evaluate(`(() => {
      const field = document.querySelector(${JSON.stringify(selector)})
      if (!field) throw new Error('missing ' + ${JSON.stringify(selector)})
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set
      setter.call(field, ${JSON.stringify(value)})
      field.dispatchEvent(new Event('input', { bubbles: true }))
      return true
    })()`)
  }

  async scrollTo(y) {
    await this.evaluate(`window.scrollTo(0, ${Number(y)})`)
    await sleep(400)
  }

  // Scrolls so the heading with exactly this text sits at the top of the
  // viewport, the way a person scrolls to a section before reading it.
  async scrollToText(text, tag = 'h2, h3, h4') {
    const found = await this.evaluate(`(() => {
      const el = [...document.querySelectorAll(${JSON.stringify(tag)})].find((node) => node.textContent.trim() === ${JSON.stringify(text)})
      if (!el) return false
      el.scrollIntoView({ block: 'start' }); window.scrollBy(0, -88); return true
    })()`)
    if (!found) throw new Error(`no heading with text ${text}`)
    await sleep(400)
  }

  // Waits for the outcome of something the page does in the background, such
  // as a test send, by watching for the text it shows when done.
  async waitForText(selector, text, timeoutMs = 30000) {
    const deadline = Date.now() + timeoutMs
    while (Date.now() < deadline) {
      const found = await this.evaluate(`[...document.querySelectorAll(${JSON.stringify(selector)})].some((el) => el.textContent.includes(${JSON.stringify(text)}))`)
      if (found) { await this.settle(); return }
      await sleep(200)
    }
    throw new Error(`timed out waiting for ${JSON.stringify(text)} in ${selector}`)
  }

  async shoot(name) {
    const { data } = await this.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })
    const file = path.join(outputDir, `${name}.png`)
    writeFileSync(file, Buffer.from(data, 'base64'))
    console.log(`  ${path.relative(repoRoot, file)}`)
  }
}

// --- the shots ---------------------------------------------------------------

async function capture(seeded) {
  const chrome = await launchChrome()
  const cdp = await CDP.connect(chrome.webSocketDebuggerUrl)
  try {
    const page = await Page.open(cdp)

    console.log('사용자 화면')
    await page.goto('/login')
    await page.type('input[autocomplete="username"]', 'kim-buyer')
    await page.type('input[autocomplete="current-password"]', demoPassword)
    await page.shoot('login')

    await page.goto('/')
    await page.shoot('marketplace')
    await page.scrollTo(600)
    await page.shoot('marketplace-results')

    await page.goto(`/talents/${seeded.talentIDs[0]}`)
    await page.shoot('talent-detail')

    await page.signInAs(seeded.buyer)
    await page.goto('/orders')
    await page.shoot('orders')

    await page.goto(`/orders/${seeded.workspaceOrder}`)
    await page.shoot('order-workspace')
    await page.clickText('대화')
    await page.shoot('order-workspace-messages')

    if (seeded.completedOrder) {
      await page.goto(`/orders/${seeded.completedOrder}`)
      await page.clickText('이력')
      await page.shoot('order-workspace-timeline')
    }

    await page.goto('/profile')
    await page.shoot('profile')
    await page.goto('/profile/keys')
    await page.shoot('profile-keys')
    await page.goto('/profile/security')
    await page.shoot('profile-security')
    await page.goto('/profile/favorites')
    await page.shoot('profile-favorites')
    await page.goto('/profile/organizations')
    await page.shoot('profile-organizations')
    await page.goto('/profile/inquiries')
    await page.shoot('profile-inquiries')
    await page.goto('/profile/projects')
    await page.shoot('profile-projects')
    await page.goto('/profile/notifications')
    await page.shoot('profile-notifications')
    await page.goto('/profile/reports')
    await page.shoot('profile-reports')

    await page.goto('/')
    // The bell's label carries the unread count once there is one.
    await page.click('button[aria-label="알림"], button[aria-label^="읽지 않은 알림"]')
    await page.shoot('notifications')

    console.log('판매자 화면')
    await page.signInAs(seeded.sellerA)
    await page.goto('/profile/seller')
    await page.shoot('profile-seller')
    await page.goto('/profile/earnings')
    await page.shoot('profile-earnings')
    await page.goto('/profile/reviews')
    await page.shoot('profile-reviews')
    await page.goto(`/sellers/${(await seeded.sellerA.must('GET', '/api/v1/me')).id}`)
    await page.shoot('seller-public')

    console.log('관리자 화면')
    await page.signInAs(seeded.admin)
    const adminScreens = [
      ['', 'admin-dashboard'], ['/users', 'admin-users'], ['/talents', 'admin-talents'], ['/orders', 'admin-orders'],
      ['/approvals', 'admin-approvals'], ['/finance', 'admin-finance'], ['/risk', 'admin-risk'], ['/coupons', 'admin-coupons'],
      ['/ai', 'admin-ai'], ['/workflow', 'admin-workflow'], ['/events', 'admin-events'], ['/features', 'admin-features'],
      ['/auth', 'admin-auth'], ['/roles', 'admin-roles'], ['/audit', 'admin-audit'], ['/settings', 'admin-settings'],
    ]
    for (const [suffix, name] of adminScreens) {
      await page.goto(`/admin${suffix}`)
      await page.shoot(name)
    }
    // The user list opens a detail view when a card is clicked.
    await page.goto('/admin/users')
    await page.clickText('김바다', 'h4')
    await page.shoot('admin-user-detail')

    // Tracking: the form, then the blocked origin the seed reported.
    await page.goto('/admin/tracking')
    await page.shoot('admin-tracking')
    await page.scrollToText('막힌 출처')
    await page.shoot('admin-tracking-violations')

    // Mail: the form, then a test send pressed for real. The relay does not
    // exist, so the result is the failure an operator sees when the relay is
    // wrong — the reason this button is on the screen. This is the one step
    // that adds a row on every run: a test send is an attempt, and attempts
    // are what the record below it keeps.
    await page.goto('/admin/mail')
    await page.shoot('admin-mail')
    await page.clickText('시험 발송')
    await page.waitForText('.MuiAlert-root', 'SMTP')
    await page.scrollToText('시험 발송')
    await page.shoot('admin-mail-deliveries')
  } finally {
    cdp.socket.close()
    // Chrome keeps writing its profile for a moment after the signal; removing
    // the directory underneath it fails with ENOTEMPTY, so the exit comes first.
    const exited = new Promise((resolve) => chrome.child.once('exit', resolve))
    chrome.child.kill()
    await Promise.race([exited, sleep(5000)])
    rmSync(chrome.profile, { recursive: true, force: true })
  }
}

mkdirSync(outputDir, { recursive: true })
console.log(`대상: ${base}`)
const seeded = await seed()
await capture(seeded)
console.log('완료')
