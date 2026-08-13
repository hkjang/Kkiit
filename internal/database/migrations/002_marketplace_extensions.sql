CREATE TABLE IF NOT EXISTS talent_options (
    id uuid PRIMARY KEY,
    talent_id uuid NOT NULL REFERENCES talents(id) ON DELETE CASCADE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    price bigint NOT NULL DEFAULT 0 CHECK (price >= 0),
    additional_days integer NOT NULL DEFAULT 0 CHECK (additional_days >= 0),
    pricing_rule jsonb NOT NULL DEFAULT '{}'::jsonb,
    active boolean NOT NULL DEFAULT true,
    sort_order integer NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS portfolios (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    media jsonb NOT NULL DEFAULT '[]'::jsonb,
    tags text[] NOT NULL DEFAULT ARRAY[]::text[],
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS file_objects (
    id uuid PRIMARY KEY,
    owner_id uuid REFERENCES users(id),
    order_id uuid REFERENCES orders(id),
    storage_key text NOT NULL UNIQUE,
    original_name text NOT NULL,
    mime_type text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    sha256 char(64) NOT NULL,
    scan_state text NOT NULL DEFAULT 'pending',
    storage_driver text NOT NULL DEFAULT 'database',
    data bytea,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS refunds (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id),
    payment_id uuid REFERENCES payments(id),
    requested_by uuid REFERENCES users(id),
    amount bigint NOT NULL CHECK (amount > 0),
    reason text NOT NULL,
    state text NOT NULL DEFAULT 'requested',
    provider_reference text,
    decided_by uuid REFERENCES users(id),
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notifications (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel text NOT NULL,
    template_key text NOT NULL,
    subject text NOT NULL DEFAULT '',
    body text NOT NULL,
    data jsonb NOT NULL DEFAULT '{}'::jsonb,
    state text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notifications_delivery_idx ON notifications(state, available_at);

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    channels text[] NOT NULL DEFAULT ARRAY['web']::text[],
    enabled boolean NOT NULL DEFAULT true,
    PRIMARY KEY(user_id, event_type)
);

CREATE TABLE IF NOT EXISTS notification_templates (
    key text PRIMARY KEY,
    channel text NOT NULL,
    locale text NOT NULL DEFAULT 'ko-KR',
    subject_template text NOT NULL DEFAULT '',
    body_template text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1,
    updated_by uuid REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS favorites (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    talent_id uuid NOT NULL REFERENCES talents(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id, talent_id)
);

CREATE TABLE IF NOT EXISTS coupons (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    discount_type text NOT NULL,
    discount_value bigint NOT NULL CHECK (discount_value >= 0),
    min_order_amount bigint NOT NULL DEFAULT 0,
    max_discount_amount bigint,
    usage_limit integer,
    per_user_limit integer NOT NULL DEFAULT 1,
    starts_at timestamptz,
    ends_at timestamptz,
    conditions jsonb NOT NULL DEFAULT '{}'::jsonb,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reports (
    id uuid PRIMARY KEY,
    reporter_id uuid REFERENCES users(id),
    resource_type text NOT NULL,
    resource_id uuid NOT NULL,
    reason text NOT NULL,
    details text NOT NULL DEFAULT '',
    evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
    state text NOT NULL DEFAULT 'open',
    assigned_to uuid REFERENCES users(id),
    resolution text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rfqs (
    id uuid PRIMARY KEY,
    buyer_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    title text NOT NULL,
    description text NOT NULL,
    requirements jsonb NOT NULL DEFAULT '{}'::jsonb,
    budget_min bigint,
    budget_max bigint,
    currency char(3) NOT NULL DEFAULT 'KRW',
    desired_due_at timestamptz,
    state text NOT NULL DEFAULT 'draft',
    ai_analysis jsonb,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS quotes (
    id uuid PRIMARY KEY,
    rfq_id uuid NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    seller_id uuid NOT NULL REFERENCES users(id),
    amount bigint NOT NULL CHECK (amount >= 0),
    currency char(3) NOT NULL DEFAULT 'KRW',
    delivery_days integer NOT NULL CHECK (delivery_days > 0),
    scope jsonb NOT NULL DEFAULT '{}'::jsonb,
    milestones jsonb NOT NULL DEFAULT '[]'::jsonb,
    ai_draft jsonb,
    match_score numeric(5,2),
    state text NOT NULL DEFAULT 'submitted',
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(rfq_id, seller_id)
);

CREATE TABLE IF NOT EXISTS milestones (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    amount bigint NOT NULL CHECK (amount >= 0),
    sequence integer NOT NULL,
    state text NOT NULL DEFAULT 'pending',
    due_at timestamptz,
    delivered_at timestamptz,
    accepted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(order_id, sequence)
);

CREATE TABLE IF NOT EXISTS change_requests (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    requested_by uuid NOT NULL REFERENCES users(id),
    description text NOT NULL,
    additional_amount bigint NOT NULL DEFAULT 0,
    additional_days integer NOT NULL DEFAULT 0,
    seller_state text NOT NULL DEFAULT 'pending',
    buyer_state text NOT NULL DEFAULT 'pending',
    payment_state text NOT NULL DEFAULT 'not_required',
    addendum jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    talent_id uuid NOT NULL REFERENCES talents(id),
    package_id uuid REFERENCES talent_packages(id),
    state text NOT NULL DEFAULT 'active',
    interval_unit text NOT NULL,
    interval_count integer NOT NULL DEFAULT 1,
    amount bigint NOT NULL CHECK (amount >= 0),
    currency char(3) NOT NULL DEFAULT 'KRW',
    credits_per_period bigint NOT NULL DEFAULT 0,
    current_period_start timestamptz NOT NULL,
    current_period_end timestamptz NOT NULL,
    next_billing_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS capacities (
    seller_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    period text NOT NULL DEFAULT 'month',
    available_units numeric(12,2) NOT NULL DEFAULT 0,
    committed_units numeric(12,2) NOT NULL DEFAULT 0,
    tentative_units numeric(12,2) NOT NULL DEFAULT 0,
    unit text NOT NULL DEFAULT 'hours',
    calendar jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS time_entries (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id),
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    minutes integer NOT NULL DEFAULT 0 CHECK (minutes >= 0),
    description text NOT NULL DEFAULT '',
    billable boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS seller_scores (
    id uuid PRIMARY KEY,
    seller_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    score numeric(5,2) NOT NULL,
    components jsonb NOT NULL,
    algorithm_version text NOT NULL,
    calculated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS seller_scores_latest_idx ON seller_scores(seller_id, calculated_at DESC);

CREATE TABLE IF NOT EXISTS talent_scores (
    id uuid PRIMARY KEY,
    talent_id uuid NOT NULL REFERENCES talents(id) ON DELETE CASCADE,
    score numeric(5,2) NOT NULL,
    components jsonb NOT NULL,
    result text NOT NULL,
    algorithm_version text NOT NULL,
    calculated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS matching_scores (
    id uuid PRIMARY KEY,
    user_id uuid REFERENCES users(id),
    rfq_id uuid REFERENCES rfqs(id),
    talent_id uuid NOT NULL REFERENCES talents(id) ON DELETE CASCADE,
    seller_id uuid NOT NULL REFERENCES users(id),
    score numeric(5,2) NOT NULL,
    components jsonb NOT NULL,
    explanation text NOT NULL DEFAULT '',
    algorithm_version text NOT NULL,
    calculated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS risk_scores (
    id uuid PRIMARY KEY,
    resource_type text NOT NULL,
    resource_id uuid NOT NULL,
    level text NOT NULL CHECK (level IN ('LOW','MEDIUM','HIGH','CRITICAL')),
    score numeric(6,2) NOT NULL,
    signals jsonb NOT NULL DEFAULT '[]'::jsonb,
    actions jsonb NOT NULL DEFAULT '[]'::jsonb,
    model_version text NOT NULL,
    calculated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS risk_scores_queue_idx ON risk_scores(level, calculated_at DESC);

CREATE TABLE IF NOT EXISTS ai_usage (
    id uuid PRIMARY KEY,
    execution_id uuid REFERENCES ai_executions(id) ON DELETE CASCADE,
    user_id uuid REFERENCES users(id),
    order_id uuid REFERENCES orders(id),
    feature text NOT NULL,
    model text NOT NULL,
    input_tokens integer NOT NULL DEFAULT 0,
    output_tokens integer NOT NULL DEFAULT 0,
    estimated_cost numeric(18,6) NOT NULL DEFAULT 0,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS budgets (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    scope_type text NOT NULL,
    scope_id text,
    name text NOT NULL,
    amount bigint NOT NULL CHECK (amount >= 0),
    consumed_amount bigint NOT NULL DEFAULT 0 CHECK (consumed_amount >= 0),
    currency char(3) NOT NULL DEFAULT 'KRW',
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS seller_teams (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id),
    name text NOT NULL,
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS seller_members (
    team_id uuid NOT NULL REFERENCES seller_teams(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL,
    skills text[] NOT NULL DEFAULT ARRAY[]::text[],
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(team_id, user_id)
);

CREATE TABLE IF NOT EXISTS webhooks (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    target_url text NOT NULL,
    events text[] NOT NULL,
    secret_encrypted bytea NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id uuid PRIMARY KEY,
    webhook_id uuid NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_id uuid NOT NULL REFERENCES domain_events(id),
    state text NOT NULL DEFAULT 'pending',
    response_status integer,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz
);

CREATE TABLE IF NOT EXISTS tracked_events (
    id uuid PRIMARY KEY,
    user_id uuid REFERENCES users(id),
    session_key text,
    event_name text NOT NULL,
    resource_type text,
    resource_id uuid,
    properties jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tracked_events_analysis_idx ON tracked_events(event_name, occurred_at DESC);

INSERT INTO system_settings(key,value,is_secret,description) VALUES
('ai.gateway.credentials','{}'::jsonb,true,'AI Gateway 인증 비밀'),
('payment.policy','{"provider":"manual","escrow_enabled":true,"idempotency_required":true}'::jsonb,false,'결제 Adapter 및 에스크로 정책'),
('payment.credentials','{}'::jsonb,true,'결제사 인증 비밀'),
('settlement.policy','{"delay_days":3,"batch_enabled":false,"hold_levels":["HIGH","CRITICAL"]}'::jsonb,false,'정산 실행 및 보류 정책'),
('risk.policy','{"high_threshold":70,"critical_threshold":90,"auto_hold_settlement":true}'::jsonb,false,'Fraud Risk 임계치와 조치'),
('sla.policy','{"inquiry_hours":4,"order_confirmation_hours":2,"revision_hours":24,"dispute_response_hours":24}'::jsonb,false,'운영 및 판매자 SLA'),
('search.policy','{"mode":"postgres_fts","semantic_enabled":false,"personalization_enabled":false}'::jsonb,false,'검색 및 개인화 정책'),
('agent.runtime','{"enabled":false,"default_timeout_seconds":300,"max_retries":2,"human_review_default":true}'::jsonb,false,'AI Agent 실행 정책'),
('notification.credentials','{}'::jsonb,true,'메일·SMS·Push 인증 비밀'),
('storage.credentials','{}'::jsonb,true,'S3 Compatible Storage 인증 비밀'),
('security.policy','{"admin_mfa_required":false,"malware_scan_required":false,"immutable_audit":true,"webhook_signature_required":true}'::jsonb,false,'보안 고도화 정책'),
('observability.policy','{"otel_enabled":false,"endpoint":"","business_trace_enabled":true}'::jsonb,false,'OpenTelemetry 및 Business Trace 정책')
ON CONFLICT (key) DO NOTHING;

INSERT INTO feature_flags(key,enabled,description) VALUES
('smart_rfq',false,'Smart RFQ'),('dynamic_pricing',false,'Dynamic Pricing'),('requirement_agent',false,'Requirement Agent'),
('change_request',false,'Change Request'),('seller_capacity',false,'Seller Capacity'),('risk_engine',false,'Risk Engine'),
('ai_workspace',false,'AI Workspace Assistant'),('seller_team',false,'Seller Team'),('api_marketplace',false,'API Marketplace')
ON CONFLICT (key) DO NOTHING;
