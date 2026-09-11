export type VersionInfo = { service: string; version: string; commit: string; built_at: string; api_version: string; mcp_protocol_version: string }
export type Principal = { id: string; username: string; email?: string; display_name: string; roles: string[]; permissions: string[] }
export type AuthProvider = { id?: string; slug: string; name: string; preset: string; provider_type?: string; enabled?: boolean; issuer_url?: string | null; authorization_url?: string | null; token_url?: string | null; userinfo_url?: string | null; client_id?: string; secret_configured?: boolean; scopes?: string[]; claim_mapping?: Record<string, unknown>; options?: Record<string, unknown>; login_url?: string }
export type SellerTrust = { id: string; display_name: string; level: string; score: number; rating?: number; rating_count?: number; headline?: string }
export type Talent = { id: string; title: string; slug: string; summary: string; service_type: string; base_price: number; currency: string; delivery_days: number; tags: string[]; quality_score?: number; rank_score?: number; favorite_count?: number; favorited?: boolean; accepting_orders?: boolean; seller: SellerTrust }
export const sellerLevelLabels: Record<string, string> = { NEW: '신규', RISING: '성장', PRO: '프로', ELITE: '최상위' }
export type Order = { id: string; order_number: string; state: string; amount: number; currency: string; due_at?: string; created_at: string; talent_title: string; buyer: { id: string; display_name: string }; seller: { id: string; display_name: string } }
export type ApiKey = { id: string; name: string; prefix: string; scopes: string[]; allowed_cidrs: string[]; rate_limit_per_minute: number; expires_at?: string; last_used_at?: string; revoked_at?: string; created_at: string }
export type Notification = { id: string; event_type: string; template_key: string; subject: string; body: string; data?: Record<string, unknown>; link: string; read_at?: string; created_at: string }
export type NotificationPreference = { event_type: string; channels: string[]; enabled: boolean }
export type NotificationTemplate = { key: string; channel: string; locale: string; subject_template: string; body_template: string; enabled: boolean; version: number; updated_at: string }
export type Webhook = { id: string; name: string; target_url: string; events: string[]; enabled: boolean; created_at: string; updated_at: string; pending_deliveries: number; failed_deliveries: number; last_delivered_at?: string }
export type WebhookDelivery = { id: string; webhook_id: string; webhook_name?: string; target_url?: string; owner_name?: string; event_type: string; state: string; response_status?: number; attempts: number; next_attempt_at: string; last_error?: string; created_at: string; delivered_at?: string }
export type DomainEvent = { id: string; aggregate_type: string; aggregate_id: string; event_type: string; payload?: Record<string, unknown>; status: string; attempts: number; available_at: string; processed_at?: string; last_error?: string; created_at: string }
export type EventSummary = { events_pending?: number; events_failed?: number; events_done?: number; deliveries_pending?: number; deliveries_failed?: number; notifications_unread?: number }
export type DisputeResolution = { outcome?: string; label?: string; refund_amount?: number; seller_amount?: number; note?: string }
export type AdminDispute = { id: string; order_id: string; order_number: string; order_state: string; amount: number; currency: string; talent_title: string; buyer_name: string; seller_name: string; opened_by_name: string; state: string; reason: string; resolution?: DisputeResolution; created_at: string; updated_at: string; waiting_hours?: number; note_count?: number }
export type RiskSignal = { code: string; label: string; weight: number; detail: string }
export type RiskScore = { id: string; resource_type: string; resource_id: string; level: string; score: number; signals: RiskSignal[]; actions: string[]; model_version: string; calculated_at: string; order_number?: string; order_state?: string; amount?: number; currency?: string; talent_title?: string; buyer_name?: string; seller_name?: string }
export type Portfolio = { id: string; title: string; description: string; media: Array<{ url?: string; name?: string; mime_type?: string }>; tags: string[]; created_at: string; updated_at: string }
export type FavoriteTalent = Talent & { status: string; favorited: boolean; favorited_at: string }
export type Coupon = { id: string; code: string; name: string; discount_type: string; discount_value: number; min_order_amount: number; max_discount_amount?: number | null; usage_limit?: number | null; per_user_limit: number; starts_at?: string | null; ends_at?: string | null; active: boolean; created_at: string; redemption_count: number; redeemed_amount: number }
export type Report = { id: string; resource_type: string; resource_id: string; reason: string; reason_label: string; details: string; evidence: unknown[]; state: string; resolution?: string | null; created_at: string; updated_at: string; reporter_name?: string; resource_label?: string; report_count?: number; note_count?: number }
export const reportStateLabels: Record<string, string> = { open: '접수', reviewing: '검토 중', resolved: '처리 완료' }
export type Organization = { id: string; name: string; slug: string; role: string; role_label: string; member_count: number; created_at: string }
export type OrganizationMember = { user_id: string; display_name: string; username: string; role: string; role_label: string; joined_at: string }
export type OrganizationBudget = { id: string; scope_type: string; name: string; amount: number; consumed_amount: number; remaining_amount: number; currency: string; starts_at: string; ends_at: string; active: boolean }
export type OrganizationDetail = { id: string; name: string; slug: string; role: string; role_label: string; created_at: string; members: OrganizationMember[]; budgets: OrganizationBudget[]; spend?: { order_count: number; active_orders: number; gross_amount: number; discount_amount: number; paid_amount: number } }
export type OrganizationOrder = { id: string; order_number: string; state: string; amount: number; discount_amount: number; payable_amount: number; currency: string; created_at: string; talent_title: string; buyer_name: string; seller_name: string }
export type SellerSettlement = { id: string; order_id: string; order_number: string; talent_title: string; buyer_name: string; gross_amount: number; platform_fee: number; pg_fee: number; tax_amount: number; net_amount: number; state: string; state_label?: string; hold_reason?: string | null; scheduled_at?: string | null; settled_at?: string | null; created_at: string; currency: string }
export type SellerEarnings = { upcoming_amount?: number; held_amount?: number; paid_amount?: number; lifetime_gross?: number; lifetime_fee?: number; next_payout_at?: string | null }
export type AccountSession = { id: string; ip?: string | null; user_agent: string; created_at: string; last_seen_at?: string | null; expires_at: string; current: boolean }
export type LinkedIdentity = { id: string; provider_slug: string; provider_name: string; subject: string; created_at: string; last_login_at: string }
export type AIUsageBreakdown = { feature: string; model: string; calls: number; input_tokens: number; output_tokens: number; estimated_cost: number }
export type AIUsageReport = { month_spent: number; monthly_budget: number; remaining: number; currency: string; budget_enforced: boolean; exhausted: boolean; executions: number; input_tokens: number; output_tokens: number; breakdown: AIUsageBreakdown[] }
export type FeatureFlag = { key: string; description: string; enabled: boolean }

export type TalentReview = { id: string; quality: number; communication: number; timeliness: number; professionalism: number; repurchase: boolean; body: string; created_at: string; average: number; buyer_name: string; seller_reply?: string | null; seller_replied_at?: string | null }
export type ReviewSummary = { count: number; average?: number | null; quality?: number | null; communication?: number | null; timeliness?: number | null; professionalism?: number | null; repurchase_rate?: number | null; distribution?: number[] }
export type Category = { id: string; parent_id: string | null; slug: string; name: string; description: string; sort_order: number }
export type Inquiry = { id: string; talent_id: string; talent_title: string; buyer: { id: string; display_name: string }; seller: { id: string; display_name: string }; state: string; last_message_at: string; created_at: string; last_message?: string | null; last_sender_id?: string | null; unread: number; role: 'buyer' | 'seller' }
export type InquiryMessage = { id: string; sender_id: string; sender_name: string; body: string; created_at: string }
export type SellerProfile = { id: string; display_name: string; member_since: string; headline: string; biography: string; skills: string[]; seller_type: string; verified: boolean; level: string; score: number; rating: number; rating_count: number; published_talents: number; completed_orders: number; active_orders: number; on_time_orders: number; accepting_orders: boolean }
export type SellerTalent = { id: string; title: string; slug: string; summary: string; service_type: string; base_price: number; currency: string; delivery_days: number; tags: string[]; quality_score?: number | null; published_at?: string | null; favorite_count: number }
export type SellerReviewItem = { id: string; quality: number; communication: number; timeliness: number; professionalism: number; repurchase: boolean; body: string; created_at: string; average: number; buyer_name: string; seller_reply?: string | null; seller_replied_at?: string | null; talent_id: string; talent_title: string }
export type AdminUserDetail = {
  id: string; username: string; email?: string | null; display_name: string; status: string
  created_at: string; last_login_at?: string | null; roles: string[]
  mfa_enabled: boolean; active_sessions: number; active_api_keys: number; linked_identities: number
  seller?: { headline: string; level: string; score: number; rating: number; rating_count: number; capacity: number; published_talents: number } | null
  orders: { bought: number; sold: number; completed: number; cancelled: number; live: number }
  money: { paid: number; settled: number; pending_settlement: number }
  trouble: { disputes_opened: number; disputes_against: number; reports_filed: number; reports_against: number }
  organizations: { id: string; name: string; role: string }[]
  recent_actions: { occurred_at: string; action: string; resource_type: string; result: string }[]
}
export type OperatorNote = { id: string; body: string; pinned: boolean; created_at: string; author_id: string; author_name: string }
export type DisputeCase = {
  id: string; state: string; reason: string; created_at: string
  opened_by: { id: string; display_name: string; side: 'buyer' | 'seller' }
  order: { id: string; order_number: string; state: string; amount: number; currency: string; requirements: Record<string, string>; due_at?: string | null; overdue_days: number; talent_title: string; escrow: number; settlement_state?: string | null; reclaimable: number }
  buyer: { id: string; display_name: string; orders: number; disputes_opened: number }
  seller: { id: string; display_name: string; orders: number; disputes_against: number; rating: number; level: string }
  timeline: { at: string; event: string; from?: string | null; to?: string | null; note?: string | null }[]
  deliveries: { at: string; type: string; description: string; content: unknown }[]
  messages: { at: string; sender: string; side: 'buyer' | 'seller'; body: string }[]
}
export type AdminOrderCase = {
  id: string; order_number: string; state: string; amount: number; discount_amount: number; currency: string
  requirements: Record<string, string>; due_at?: string | null; created_at: string; accepted_at?: string | null
  overdue: boolean; overdue_days: number
  talent: { id: string; title: string }
  buyer: { id: string; display_name: string }
  seller: { id: string; display_name: string }
  organization?: { id: string; name: string } | null
  money: { escrow: number; paid: number; refunded: number; settlement_state?: string | null; settlement_net?: number | null }
  disputes: { id: string; state: string; reason: string; at: string }[]
  timeline: { at: string; event: string; from?: string | null; to?: string | null; note?: string | null }[]
  deliveries: { at: string; type: string; description: string }[]
  messages: { at: string; sender: string; side: 'buyer' | 'seller'; body: string }[]
}
export type ReportCase = {
  id: string; state: string; reason: string; details: string; created_at: string
  resource_type: string; resource_id: string
  reporter: { id: string; display_name: string; status: string; created_at: string; reports_filed: number; reports_open: number }
  history: { id: string; at: string; reason: string; state: string; resolution?: string | null }[]
  subject?: (
    | { kind: 'talent'; title: string; summary: string; description: string; status: string; base_price: number; tags: string[]; seller: { id: string; display_name: string; status: string } }
    | { kind: 'user'; display_name: string; status: string; created_at: string; headline: string; biography: string; orders: number }
    | { kind: 'order'; order_number: string; state: string; amount: number; currency: string; talent_title: string; buyer: string; seller: string }
    | { kind: 'message'; body: string; at: string; sender: { id: string; display_name: string; status: string }; order_number: string }
  ) | null
}
export type ApprovalCase = {
  id: string; state: string; current_step: number; created_at: string; resource_type: string; resource_id: string
  policy: { name: string; steps: unknown; conditions: unknown }
  requested_by?: { id: string; display_name: string } | null
  previous: { at?: string | null; state: string; note?: string | null }[]
  talent?: {
    id: string; title: string; summary: string; description: string; status: string; service_type: string
    base_price: number; currency: string; delivery_days: number; revision_count: number; tags: string[]
    refund_policy: string; quality_score?: number | null; category?: string | null
    packages: { name: string; price: number; delivery_days: number; description: string }[]
    seller: { id: string; display_name: string; status: string; level: string; rating: number; published_talents: number; rejected_talents: number; reports_against: number }
  } | null
}
