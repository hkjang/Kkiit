CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS system_settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL DEFAULT '{}'::jsonb,
    encrypted_value bytea,
    is_secret boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1,
    description text NOT NULL DEFAULT '',
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY,
    username text NOT NULL,
    email text,
    password_hash text,
    display_name text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
    locale text NOT NULL DEFAULT 'ko-KR',
    timezone text NOT NULL DEFAULT 'Asia/Seoul',
    profile jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_login_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_uq ON users (lower(username));
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_uq ON users (lower(email)) WHERE email IS NOT NULL;

CREATE TABLE IF NOT EXISTS roles (
    code text PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    system_role boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS permissions (
    code text PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS role_permissions (
    role_code text NOT NULL REFERENCES roles(code) ON DELETE CASCADE,
    permission_code text NOT NULL REFERENCES permissions(code) ON DELETE CASCADE,
    PRIMARY KEY (role_code, permission_code)
);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_code text NOT NULL REFERENCES roles(code) ON DELETE CASCADE,
    granted_by uuid REFERENCES users(id),
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_code)
);

CREATE TABLE IF NOT EXISTS auth_providers (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    provider_type text NOT NULL CHECK (provider_type IN ('oidc','oauth2')),
    preset text NOT NULL DEFAULT 'custom',
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    issuer_url text,
    authorization_url text,
    token_url text,
    userinfo_url text,
    client_id text NOT NULL DEFAULT '',
    client_secret_encrypted bytea,
    scopes text[] NOT NULL DEFAULT ARRAY['openid','profile','email']::text[],
    claim_mapping jsonb NOT NULL DEFAULT '{"subject":"sub","email":"email","name":"name"}'::jsonb,
    options jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS external_identities (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id uuid NOT NULL REFERENCES auth_providers(id) ON DELETE CASCADE,
    subject text NOT NULL,
    claims jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(provider_id, subject)
);

CREATE TABLE IF NOT EXISTS oauth_states (
    state_hash bytea PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES auth_providers(id) ON DELETE CASCADE,
    verifier_encrypted bytea NOT NULL,
    redirect_uri text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    ip_address inet,
    user_agent text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_user_active_idx ON sessions(user_id, expires_at) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS api_keys (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    prefix text NOT NULL,
    secret_hash bytea NOT NULL UNIQUE,
    scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    allowed_cidrs cidr[] NOT NULL DEFAULT ARRAY[]::cidr[],
    rate_limit_per_minute integer NOT NULL DEFAULT 60 CHECK (rate_limit_per_minute > 0),
    expires_at timestamptz,
    last_used_at timestamptz,
    rotated_from uuid REFERENCES api_keys(id),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS api_keys_owner_idx ON api_keys(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
    id uuid PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    actor_user_id uuid REFERENCES users(id),
    actor_roles text[] NOT NULL DEFAULT ARRAY[]::text[],
    ip_address inet,
    user_agent text NOT NULL DEFAULT '',
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text,
    before_data jsonb,
    after_data jsonb,
    request_id text NOT NULL,
    session_id uuid REFERENCES sessions(id),
    result text NOT NULL DEFAULT 'success'
);
CREATE INDEX IF NOT EXISTS audit_logs_resource_idx ON audit_logs(resource_type, resource_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_logs_actor_idx ON audit_logs(actor_user_id, occurred_at DESC);

CREATE TABLE IF NOT EXISTS categories (
    id uuid PRIMARY KEY,
    parent_id uuid REFERENCES categories(id),
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    sort_order integer NOT NULL DEFAULT 0,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS seller_profiles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    seller_type text NOT NULL DEFAULT 'individual' CHECK (seller_type IN ('individual','business','team')),
    headline text NOT NULL DEFAULT '',
    biography text NOT NULL DEFAULT '',
    skills text[] NOT NULL DEFAULT ARRAY[]::text[],
    capacity integer NOT NULL DEFAULT 5 CHECK (capacity >= 0),
    level text NOT NULL DEFAULT 'NEW',
    score numeric(5,2) NOT NULL DEFAULT 0,
    verified boolean NOT NULL DEFAULT false,
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS talents (
    id uuid PRIMARY KEY,
    seller_id uuid NOT NULL REFERENCES users(id),
    category_id uuid REFERENCES categories(id),
    title text NOT NULL,
    slug text NOT NULL UNIQUE,
    summary text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','review_pending','published','rejected','paused','archived')),
    service_type text NOT NULL DEFAULT 'HUMAN' CHECK (service_type IN ('HUMAN','AI','HYBRID')),
    base_price bigint NOT NULL DEFAULT 0 CHECK (base_price >= 0),
    currency char(3) NOT NULL DEFAULT 'KRW',
    delivery_days integer NOT NULL DEFAULT 1 CHECK (delivery_days > 0),
    revision_count integer NOT NULL DEFAULT 0 CHECK (revision_count >= 0),
    scope_included jsonb NOT NULL DEFAULT '[]'::jsonb,
    scope_excluded jsonb NOT NULL DEFAULT '[]'::jsonb,
    deliverables jsonb NOT NULL DEFAULT '[]'::jsonb,
    tags text[] NOT NULL DEFAULT ARRAY[]::text[],
    faq jsonb NOT NULL DEFAULT '[]'::jsonb,
    refund_policy text NOT NULL DEFAULT '',
    instant_order boolean NOT NULL DEFAULT true,
    quote_required boolean NOT NULL DEFAULT false,
    subscription_enabled boolean NOT NULL DEFAULT false,
    quality_score numeric(5,2),
    search_document tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(summary, '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(description, '')), 'C')
    ) STORED,
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS talents_search_idx ON talents USING gin(search_document);
CREATE INDEX IF NOT EXISTS talents_seller_idx ON talents(seller_id, created_at DESC);
CREATE INDEX IF NOT EXISTS talents_published_idx ON talents(status, published_at DESC);

CREATE TABLE IF NOT EXISTS talent_packages (
    id uuid PRIMARY KEY,
    talent_id uuid NOT NULL REFERENCES talents(id) ON DELETE CASCADE,
    package_type text NOT NULL CHECK (package_type IN ('BASIC','STANDARD','PREMIUM','CUSTOM')),
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    price bigint NOT NULL CHECK (price >= 0),
    delivery_days integer NOT NULL CHECK (delivery_days > 0),
    revision_count integer NOT NULL DEFAULT 0,
    features jsonb NOT NULL DEFAULT '[]'::jsonb,
    deliverables jsonb NOT NULL DEFAULT '[]'::jsonb,
    sort_order integer NOT NULL DEFAULT 0,
    active boolean NOT NULL DEFAULT true,
    UNIQUE(talent_id, package_type)
);

CREATE TABLE IF NOT EXISTS talent_requirements (
    id uuid PRIMARY KEY,
    talent_id uuid NOT NULL REFERENCES talents(id) ON DELETE CASCADE,
    label text NOT NULL,
    help_text text NOT NULL DEFAULT '',
    field_type text NOT NULL,
    required boolean NOT NULL DEFAULT true,
    options jsonb NOT NULL DEFAULT '[]'::jsonb,
    validation jsonb NOT NULL DEFAULT '{}'::jsonb,
    sort_order integer NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS orders (
    id uuid PRIMARY KEY,
    order_number text NOT NULL UNIQUE,
    buyer_id uuid NOT NULL REFERENCES users(id),
    seller_id uuid NOT NULL REFERENCES users(id),
    talent_id uuid NOT NULL REFERENCES talents(id),
    package_id uuid REFERENCES talent_packages(id),
    state text NOT NULL DEFAULT 'CREATED',
    amount bigint NOT NULL CHECK (amount >= 0),
    currency char(3) NOT NULL DEFAULT 'KRW',
    requirements jsonb NOT NULL DEFAULT '{}'::jsonb,
    requirement_score numeric(5,2),
    due_at timestamptz,
    accepted_at timestamptz,
    completed_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS orders_buyer_idx ON orders(buyer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS orders_seller_idx ON orders(seller_id, created_at DESC);
CREATE INDEX IF NOT EXISTS orders_state_idx ON orders(state, due_at);

CREATE TABLE IF NOT EXISTS order_timeline (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    actor_user_id uuid REFERENCES users(id),
    from_state text,
    to_state text,
    data jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS order_timeline_order_idx ON order_timeline(order_id, created_at);

CREATE TABLE IF NOT EXISTS messages (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    sender_id uuid REFERENCES users(id),
    message_type text NOT NULL DEFAULT 'text',
    body text NOT NULL,
    attachments jsonb NOT NULL DEFAULT '[]'::jsonb,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS deliveries (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    seller_id uuid NOT NULL REFERENCES users(id),
    version integer NOT NULL,
    delivery_type text NOT NULL,
    content jsonb NOT NULL,
    description text NOT NULL DEFAULT '',
    content_hash text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(order_id, version)
);

CREATE TABLE IF NOT EXISTS revisions (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    delivery_id uuid REFERENCES deliveries(id),
    revision_number integer NOT NULL,
    details text NOT NULL,
    priority text NOT NULL DEFAULT 'normal',
    attachments jsonb NOT NULL DEFAULT '[]'::jsonb,
    charge_amount bigint NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'requested',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(order_id, revision_number)
);

CREATE TABLE IF NOT EXISTS payments (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id),
    provider text NOT NULL,
    provider_reference text,
    state text NOT NULL,
    amount bigint NOT NULL,
    currency char(3) NOT NULL DEFAULT 'KRW',
    idempotency_key text NOT NULL UNIQUE,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id uuid PRIMARY KEY,
    transaction_id uuid NOT NULL,
    order_id uuid REFERENCES orders(id),
    account text NOT NULL,
    direction text NOT NULL CHECK (direction IN ('debit','credit')),
    amount bigint NOT NULL CHECK (amount >= 0),
    currency char(3) NOT NULL DEFAULT 'KRW',
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ledger_transaction_idx ON ledger_entries(transaction_id);

CREATE TABLE IF NOT EXISTS settlements (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
    seller_id uuid NOT NULL REFERENCES users(id),
    gross_amount bigint NOT NULL,
    platform_fee bigint NOT NULL,
    pg_fee bigint NOT NULL DEFAULT 0,
    tax_amount bigint NOT NULL DEFAULT 0,
    net_amount bigint NOT NULL,
    state text NOT NULL DEFAULT 'scheduled',
    hold_reason text,
    scheduled_at timestamptz,
    settled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reviews (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
    buyer_id uuid NOT NULL REFERENCES users(id),
    seller_id uuid NOT NULL REFERENCES users(id),
    quality smallint NOT NULL CHECK (quality BETWEEN 1 AND 5),
    communication smallint NOT NULL CHECK (communication BETWEEN 1 AND 5),
    timeliness smallint NOT NULL CHECK (timeliness BETWEEN 1 AND 5),
    professionalism smallint NOT NULL CHECK (professionalism BETWEEN 1 AND 5),
    repurchase boolean NOT NULL,
    body text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS disputes (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id),
    opened_by uuid NOT NULL REFERENCES users(id),
    state text NOT NULL DEFAULT 'open',
    reason text NOT NULL,
    evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
    ai_summary jsonb,
    resolution jsonb,
    assigned_to uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS approval_policies (
    id uuid PRIMARY KEY,
    resource_type text NOT NULL,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    priority integer NOT NULL DEFAULT 100,
    conditions jsonb NOT NULL DEFAULT '{}'::jsonb,
    steps jsonb NOT NULL DEFAULT '[{"role":"operator","min_approvals":1}]'::jsonb,
    created_by uuid REFERENCES users(id),
    updated_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS approval_policies_active_idx ON approval_policies(resource_type, priority) WHERE enabled;

CREATE TABLE IF NOT EXISTS approval_requests (
    id uuid PRIMARY KEY,
    policy_id uuid NOT NULL REFERENCES approval_policies(id),
    resource_type text NOT NULL,
    resource_id uuid NOT NULL,
    requested_by uuid REFERENCES users(id),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','approved','rejected','cancelled')),
    current_step integer NOT NULL DEFAULT 0,
    context jsonb NOT NULL DEFAULT '{}'::jsonb,
    decided_by uuid REFERENCES users(id),
    decision_note text,
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS approval_requests_queue_idx ON approval_requests(state, resource_type, created_at);

CREATE TABLE IF NOT EXISTS workflow_rules (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    event_type text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    priority integer NOT NULL DEFAULT 100,
    conditions jsonb NOT NULL DEFAULT '{}'::jsonb,
    actions jsonb NOT NULL DEFAULT '[]'::jsonb,
    version integer NOT NULL DEFAULT 1,
    created_by uuid REFERENCES users(id),
    updated_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS domain_events (
    id uuid PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS domain_events_pending_idx ON domain_events(status, available_at) WHERE status IN ('pending','retry');

CREATE TABLE IF NOT EXISTS organizations (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    slug text NOT NULL UNIQUE,
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS organization_users (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL,
    team text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);

CREATE TABLE IF NOT EXISTS feature_flags (
    key text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT false,
    rules jsonb NOT NULL DEFAULT '[]'::jsonb,
    description text NOT NULL DEFAULT '',
    updated_by uuid REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ai_executions (
    id uuid PRIMARY KEY,
    user_id uuid REFERENCES users(id),
    order_id uuid REFERENCES orders(id),
    feature text NOT NULL,
    model text NOT NULL,
    state text NOT NULL,
    input_tokens integer NOT NULL DEFAULT 0,
    output_tokens integer NOT NULL DEFAULT 0,
    estimated_cost numeric(18,6) NOT NULL DEFAULT 0,
    latency_ms integer,
    request_data jsonb,
    response_data jsonb,
    error text,
    created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO permissions(code, name, description) VALUES
('admin.access','관리자 접근','서비스 관리자 페이지 접근'),
('settings.read','설정 조회','중앙 운영 설정 조회'),
('settings.write','설정 변경','중앙 운영 설정 변경'),
('users.manage','사용자 관리','사용자와 역할 관리'),
('roles.manage','권한 관리','역할 및 권한 체계 변경'),
('approvals.manage','승인 관리','승인 정책과 대기열 관리'),
('audit.read','감사 조회','감사 로그 조회'),
('talents.write','재능 등록','재능 상품 작성'),
('talents.review','재능 검토','재능 상품 승인과 반려'),
('orders.buy','주문 구매','상품 주문'),
('orders.sell','주문 수행','판매 주문 수행'),
('orders.manage','주문 관리','전체 주문 관리'),
('keys.manage.self','개인 키 관리','자신의 API 키 생성과 회전'),
('keys.manage.any','전체 키 관리','모든 사용자 키 권한 관리'),
('mcp.use','MCP 사용','Marketplace MCP 도구 사용')
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description;

INSERT INTO roles(code, name, description, system_role) VALUES
('super_admin','Super Admin','시스템 전체 권한',true),
('operator','운영자','상품·거래·분쟁 운영',true),
('finance_admin','재무 관리자','결제·정산 운영',true),
('security_admin','보안 관리자','감사·권한·보안 설정',true),
('buyer','구매자','상품 구매 역할',true),
('seller','판매자','상품 판매 역할',true)
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description;

INSERT INTO role_permissions(role_code, permission_code)
SELECT 'super_admin', code FROM permissions ON CONFLICT DO NOTHING;
INSERT INTO role_permissions(role_code, permission_code) VALUES
('operator','admin.access'),('operator','approvals.manage'),('operator','talents.review'),('operator','orders.manage'),
('finance_admin','admin.access'),('finance_admin','orders.manage'),
('security_admin','admin.access'),('security_admin','audit.read'),('security_admin','roles.manage'),('security_admin','keys.manage.any'),
('buyer','orders.buy'),('buyer','keys.manage.self'),('buyer','mcp.use'),
('seller','talents.write'),('seller','orders.sell'),('seller','keys.manage.self'),('seller','mcp.use')
ON CONFLICT DO NOTHING;

INSERT INTO system_settings(key, value, description) VALUES
('service.general','{"name":"Kkiit","locale":"ko-KR","timezone":"Asia/Seoul","offline_mode":true,"public_registration":true}'::jsonb,'서비스 기본 설정'),
('auth.security','{"session_ttl_hours":12,"cookie_secure":false,"mfa_admin_required":false,"allow_local_login":true}'::jsonb,'인증 및 세션 정책'),
('auth.oauth','{"auto_create_user":true,"default_roles":["buyer"],"callback_base_url":""}'::jsonb,'소셜 및 OIDC 로그인 정책'),
('marketplace.policy','{"currency":"KRW","platform_fee_rate":10,"auto_accept_days":0,"max_revision_count":10}'::jsonb,'거래 정책'),
('matching.weights','{"requirement":30,"skill":20,"rating":10,"on_time":10,"price":10,"response":5,"completion":10,"repeat":5}'::jsonb,'매칭 가중치'),
('workflow.defaults','{"approval_disabled_means_bypass":true,"outbox_retry_limit":10}'::jsonb,'워크플로우 기본 정책'),
('ai.gateway','{"enabled":false,"base_url":"","models":{"fast":"","balanced":"","premium":""},"monthly_budget":0}'::jsonb,'OpenAI 호환 AI Gateway 설정'),
('storage.policy','{"driver":"database","max_upload_mb":50,"allowed_mime_types":[]}'::jsonb,'오프라인 파일 저장 정책'),
('notification.channels','{"web":true,"email":false,"sms":false,"push":false,"webhook":false}'::jsonb,'알림 채널 설정'),
('api.policy','{"allowed_origins":[],"default_rate_limit_per_minute":60,"mcp_enabled":true}'::jsonb,'API 및 MCP 정책')
ON CONFLICT (key) DO NOTHING;

INSERT INTO feature_flags(key, enabled, description) VALUES
('ai_matching',false,'AI 매칭'),('smart_quote',false,'스마트 견적'),('enterprise',false,'기업 기능'),
('agent_marketplace',false,'AI Agent 상품'),('subscription',false,'구독 상품'),('milestone_payment',false,'마일스톤 결제')
ON CONFLICT (key) DO NOTHING;
