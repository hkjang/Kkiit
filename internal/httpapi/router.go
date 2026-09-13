package httpapi

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/hkjang/Kkiit/internal/analytics"
	"github.com/hkjang/Kkiit/internal/ui"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /api/v1/version", s.version)
	mux.HandleFunc("GET /api/v1/auth/providers", s.listEnabledProviders)
	mux.HandleFunc("POST /api/v1/auth/login", s.throttle("login", s.login))
	mux.HandleFunc("POST /api/v1/auth/register", s.register)
	mux.HandleFunc("POST /api/v1/auth/logout", s.require("", s.logout))
	mux.HandleFunc("GET /api/v1/auth/oauth/{slug}/start", s.oauthStart)
	mux.HandleFunc("GET /api/v1/auth/oauth/{slug}/callback", s.oauthCallback)
	mux.HandleFunc("GET /api/v1/me", s.require("", s.me))
	mux.HandleFunc("PATCH /api/v1/me", s.require("", s.updateMe))
	mux.HandleFunc("POST /api/v1/me/password", s.require("", s.throttle("password_change", s.changePassword)))
	mux.HandleFunc("GET /api/v1/me/identities", s.require("", s.listMyIdentities))
	mux.HandleFunc("DELETE /api/v1/me/identities/{id}", s.require("", s.unlinkIdentity))
	mux.HandleFunc("GET /api/v1/me/sessions", s.require("", s.listMySessions))
	mux.HandleFunc("DELETE /api/v1/me/sessions", s.require("", s.revokeMySessions))
	mux.HandleFunc("GET /api/v1/me/mfa", s.require("", s.mfaStatus))
	mux.HandleFunc("POST /api/v1/me/mfa/totp/setup", s.require("", s.setupTOTP))
	mux.HandleFunc("POST /api/v1/me/mfa/totp/confirm", s.require("", s.confirmTOTP))
	mux.HandleFunc("DELETE /api/v1/me/mfa/totp", s.require("", s.disableTOTP))
	mux.HandleFunc("GET /api/v1/me/seller-profile", s.require("", s.getMySellerProfile))
	mux.HandleFunc("PUT /api/v1/me/seller-profile", s.require("", s.putMySellerProfile))
	mux.HandleFunc("GET /api/v1/me/talents", s.require("talents.write", s.listMyTalents))
	mux.HandleFunc("GET /api/v1/me/notifications", s.require("", s.listMyNotifications))
	mux.HandleFunc("POST /api/v1/me/notifications/read", s.require("", s.markNotificationsRead))
	mux.HandleFunc("GET /api/v1/me/notifications/ws", s.require("", s.notificationWebSocket))
	mux.HandleFunc("GET /api/v1/me/notification-preferences", s.require("", s.listMyNotificationPreferences))
	mux.HandleFunc("PUT /api/v1/me/notification-preferences", s.require("", s.putMyNotificationPreferences))
	mux.HandleFunc("GET /api/v1/me/webhooks", s.require("webhooks.manage.self", s.listMyWebhooks))
	mux.HandleFunc("POST /api/v1/me/webhooks", s.require("webhooks.manage.self", s.createMyWebhook))
	mux.HandleFunc("PUT /api/v1/me/webhooks/{id}", s.require("webhooks.manage.self", s.updateMyWebhook))
	mux.HandleFunc("DELETE /api/v1/me/webhooks/{id}", s.require("webhooks.manage.self", s.deleteMyWebhook))
	mux.HandleFunc("POST /api/v1/me/webhooks/{id}/test", s.require("webhooks.manage.self", s.throttle("webhook_test", s.testMyWebhook)))
	mux.HandleFunc("GET /api/v1/me/webhooks/{id}/deliveries", s.require("webhooks.manage.self", s.listMyDeliveries))
	mux.HandleFunc("POST /api/v1/me/webhooks/{id}/deliveries/{deliveryId}/retry", s.require("webhooks.manage.self", s.retryMyDelivery))
	mux.HandleFunc("GET /api/v1/me/settlements", s.require("orders.sell", s.listMySettlements))
	mux.HandleFunc("GET /api/v1/me/favorites", s.require("", s.listMyFavorites))
	mux.HandleFunc("POST /api/v1/me/files", s.require("", s.uploadMyFile))
	mux.HandleFunc("GET /api/v1/me/portfolios", s.require("", s.listMyPortfolios))
	mux.HandleFunc("POST /api/v1/me/portfolios", s.require("talents.write", s.createMyPortfolio))
	mux.HandleFunc("PUT /api/v1/me/portfolios/{id}", s.require("talents.write", s.updateMyPortfolio))
	mux.HandleFunc("DELETE /api/v1/me/portfolios/{id}", s.require("talents.write", s.deleteMyPortfolio))
	mux.HandleFunc("GET /api/v1/me/api-keys", s.require("keys.manage.self", s.listMyAPIKeys))
	mux.HandleFunc("POST /api/v1/me/api-keys", s.require("keys.manage.self", s.createMyAPIKey))
	mux.HandleFunc("POST /api/v1/me/api-keys/{id}/rotate", s.require("keys.manage.self", s.rotateMyAPIKey))
	mux.HandleFunc("DELETE /api/v1/me/api-keys/{id}", s.require("keys.manage.self", s.revokeMyAPIKey))
	mux.HandleFunc("GET /api/v1/features", s.listFeatures)
	mux.HandleFunc("GET /api/v1/categories", s.listCategories)
	mux.HandleFunc("POST /api/v1/ai/talents/draft", s.require("talents.write", s.requireFeature("ai_matching", s.aiTalentDraft)))
	mux.HandleFunc("POST /api/v1/ai/requirements/analyze", s.require("orders.buy", s.requireFeature("ai_matching", s.aiRequirementAnalysis)))
	mux.HandleFunc("GET /api/v1/talents", s.listTalents)
	mux.HandleFunc("POST /api/v1/talents", s.require("talents.write", s.createTalent))
	mux.HandleFunc("GET /api/v1/talents/{id}", s.getTalent)
	mux.HandleFunc("GET /api/v1/talents/{id}/reviews", s.listTalentReviews)
	mux.HandleFunc("GET /api/v1/me/reviews", s.require("orders.sell", s.listMyReviews))
	mux.HandleFunc("POST /api/v1/talents/{id}/inquiries", s.require("", s.throttle("inquiry_create", s.startInquiry)))
	mux.HandleFunc("GET /api/v1/me/inquiries", s.require("", s.listMyInquiries))
	mux.HandleFunc("GET /api/v1/inquiries/{id}/messages", s.require("", s.listInquiryMessages))
	mux.HandleFunc("POST /api/v1/inquiries/{id}/messages", s.require("", s.throttle("inquiry_create", s.replyToInquiry)))
	mux.HandleFunc("POST /api/v1/reviews/{id}/reply", s.require("orders.sell", s.replyToReview))
	mux.HandleFunc("DELETE /api/v1/reviews/{id}/reply", s.require("orders.sell", s.deleteReviewReply))
	mux.HandleFunc("POST /api/v1/talents/{id}/view", s.throttle("talent_view", s.recordTalentView))
	mux.HandleFunc("POST /api/v1/talents/{id}/favorite", s.require("", s.addFavorite))
	mux.HandleFunc("DELETE /api/v1/talents/{id}/favorite", s.require("", s.removeFavorite))
	mux.HandleFunc("GET /api/v1/sellers/{id}", s.publicSellerProfile)
	mux.HandleFunc("GET /api/v1/sellers/{id}/talents", s.listSellerTalents)
	mux.HandleFunc("GET /api/v1/sellers/{id}/reviews", s.listSellerReviews)
	mux.HandleFunc("GET /api/v1/sellers/{id}/portfolios", s.listSellerPortfolios)
	mux.HandleFunc("GET /api/v1/me/organizations", s.require("", s.requireFeature("enterprise", s.listMyOrganizations)))
	mux.HandleFunc("POST /api/v1/organizations", s.require("orders.buy", s.requireFeature("enterprise", s.createOrganization)))
	mux.HandleFunc("GET /api/v1/organizations/{id}", s.require("", s.requireFeature("enterprise", s.getOrganization)))
	mux.HandleFunc("GET /api/v1/organizations/{id}/orders", s.require("", s.requireFeature("enterprise", s.listOrganizationOrders)))
	mux.HandleFunc("POST /api/v1/organizations/{id}/members", s.require("", s.requireFeature("enterprise", s.addOrganizationMember)))
	mux.HandleFunc("PATCH /api/v1/organizations/{id}/members/{userId}", s.require("", s.requireFeature("enterprise", s.updateOrganizationMember)))
	mux.HandleFunc("DELETE /api/v1/organizations/{id}/members/{userId}", s.require("", s.requireFeature("enterprise", s.removeOrganizationMember)))
	mux.HandleFunc("POST /api/v1/organizations/{id}/budgets", s.require("", s.requireFeature("enterprise", s.createOrganizationBudget)))
	mux.HandleFunc("POST /api/v1/reports", s.require("", s.throttle("report_create", s.createReport)))
	mux.HandleFunc("GET /api/v1/me/reports", s.require("", s.listMyReports))
	mux.HandleFunc("POST /api/v1/coupons/preview", s.require("orders.buy", s.throttle("coupon_preview", s.previewCoupon)))
	mux.HandleFunc("GET /api/v1/recommendations", s.requireFeature("ai_matching", s.recommendTalents))
	mux.HandleFunc("GET /api/v1/rfqs", s.require("", s.requireFeature("smart_quote", s.listRFQs)))
	mux.HandleFunc("POST /api/v1/rfqs", s.require("orders.buy", s.requireFeature("smart_quote", s.createRFQ)))
	mux.HandleFunc("GET /api/v1/rfqs/{id}/quotes", s.require("", s.requireFeature("smart_quote", s.listQuotes)))
	mux.HandleFunc("POST /api/v1/quotes", s.require("orders.sell", s.requireFeature("smart_quote", s.createQuote)))
	mux.HandleFunc("POST /api/v1/quotes/{id}/accept", s.require("orders.buy", s.requireFeature("smart_quote", s.throttle("order_create", s.acceptQuote))))
	mux.HandleFunc("PUT /api/v1/talents/{id}", s.require("talents.write", s.updateTalent))
	mux.HandleFunc("POST /api/v1/talents/{id}/publish", s.require("talents.write", s.publishTalent))
	mux.HandleFunc("POST /api/v1/talents/{id}/status", s.require("talents.write", s.setMyTalentStatus))
	mux.HandleFunc("GET /api/v1/orders", s.require("", s.listOrders))
	mux.HandleFunc("POST /api/v1/orders", s.require("orders.buy", s.throttle("order_create", s.createOrder)))
	mux.HandleFunc("GET /api/v1/orders/{id}", s.require("", s.getOrder))
	mux.HandleFunc("POST /api/v1/orders/{id}/transition", s.require("", s.transitionOrder))
	mux.HandleFunc("POST /api/v1/orders/{id}/pay", s.require("orders.buy", s.payOrder))
	mux.HandleFunc("POST /api/v1/orders/{id}/deliveries", s.require("orders.sell", s.createDelivery))
	mux.HandleFunc("POST /api/v1/orders/{id}/revision", s.require("orders.buy", s.createRevision))
	mux.HandleFunc("POST /api/v1/orders/{id}/accept", s.require("orders.buy", s.acceptOrder))
	mux.HandleFunc("GET /api/v1/orders/{id}/messages", s.require("", s.listMessages))
	mux.HandleFunc("POST /api/v1/orders/{id}/messages", s.require("", s.throttle("message_create", s.createMessage)))
	mux.HandleFunc("GET /api/v1/orders/{id}/messages/ws", s.require("", s.messageWebSocket))
	mux.HandleFunc("POST /api/v1/orders/{id}/review", s.require("orders.buy", s.createReview))
	mux.HandleFunc("GET /api/v1/orders/{id}/disputes", s.require("", s.listOrderDisputes))
	mux.HandleFunc("POST /api/v1/orders/{id}/disputes", s.require("", s.throttle("dispute_open", s.openDispute)))
	mux.HandleFunc("POST /api/v1/orders/{id}/files", s.require("", s.uploadOrderFile))
	mux.HandleFunc("GET /api/v1/files/{id}", s.downloadFile)
	mux.HandleFunc("POST /api/v1/analytics/csp-report", s.throttle("csp_report", s.receiveCSPReport))
	mux.HandleFunc("GET /api/v1/admin/analytics/violations", s.require("settings.read", s.listAnalyticsViolations))
	mux.HandleFunc("DELETE /api/v1/admin/analytics/violations", s.require("settings.write", s.clearAnalyticsViolations))
	mux.HandleFunc("POST /api/v1/admin/analytics/violations/allow", s.require("settings.write", s.allowAnalyticsHost))
	mux.HandleFunc("GET /api/v1/admin/settings", s.require("settings.read", s.listSettings))
	mux.HandleFunc("GET /api/v1/admin/dashboard", s.require("audit.read", s.adminDashboard))
	mux.HandleFunc("GET /api/v1/admin/talents", s.require("talents.review", s.listAdminTalents))
	mux.HandleFunc("GET /api/v1/admin/orders", s.require("orders.manage", s.listAdminOrders))
	mux.HandleFunc("GET /api/v1/admin/orders/{id}", s.require("orders.manage", s.getAdminOrder))
	mux.HandleFunc("GET /api/v1/admin/ai/usage", s.require("settings.read", s.listAIUsage))
	mux.HandleFunc("GET /api/v1/admin/risk", s.require("risk.manage", s.listAdminRiskQueue))
	mux.HandleFunc("POST /api/v1/admin/risk/rescan", s.require("risk.manage", s.rescanRisk))
	mux.HandleFunc("GET /api/v1/admin/reports", s.require("risk.manage", s.listAdminReports))
	mux.HandleFunc("GET /api/v1/admin/reports/{id}", s.require("risk.manage", s.getAdminReport))
	mux.HandleFunc("POST /api/v1/admin/reports/{id}/resolve", s.require("risk.manage", s.resolveReport))
	mux.HandleFunc("POST /api/v1/admin/talents/{id}/status", s.require("talents.review", s.setTalentStatus))
	mux.HandleFunc("GET /api/v1/admin/coupons", s.require("coupons.manage", s.listCoupons))
	mux.HandleFunc("POST /api/v1/admin/coupons", s.require("coupons.manage", s.createCoupon))
	mux.HandleFunc("PUT /api/v1/admin/coupons/{id}", s.require("coupons.manage", s.updateCoupon))
	mux.HandleFunc("DELETE /api/v1/admin/coupons/{id}", s.require("coupons.manage", s.deleteCoupon))
	mux.HandleFunc("GET /api/v1/admin/disputes", s.require("risk.manage", s.listAdminDisputes))
	mux.HandleFunc("GET /api/v1/admin/disputes/{id}", s.require("risk.manage", s.getAdminDispute))
	mux.HandleFunc("POST /api/v1/admin/disputes/{id}/resolve", s.require("risk.manage", s.resolveDispute))
	mux.HandleFunc("GET /api/v1/admin/users", s.require("users.manage", s.listAdminUsers))
	mux.HandleFunc("GET /api/v1/admin/users/{id}", s.require("users.manage", s.getAdminUser))
	mux.HandleFunc("PATCH /api/v1/admin/users/{id}", s.require("users.manage", s.updateAdminUser))
	mux.HandleFunc("PUT /api/v1/admin/users/{id}/roles", s.require("users.manage", s.updateAdminUserRoles))
	mux.HandleFunc("DELETE /api/v1/admin/users/{id}/mfa", s.require("users.manage", s.resetAdminUserMFA))
	mux.HandleFunc("GET /api/v1/admin/users/{id}/api-keys", s.require("keys.manage.any", s.listUserAPIKeys))
	mux.HandleFunc("DELETE /api/v1/admin/api-keys/{id}", s.require("keys.manage.any", s.revokeUserAPIKey))
	mux.HandleFunc("PUT /api/v1/admin/settings/{key}", s.require("settings.write", s.putSetting))
	mux.HandleFunc("GET /api/v1/admin/auth-providers", s.require("settings.read", s.listAuthProviders))
	mux.HandleFunc("POST /api/v1/admin/auth-providers", s.require("settings.write", s.createAuthProvider))
	mux.HandleFunc("PUT /api/v1/admin/auth-providers/{id}", s.require("settings.write", s.updateAuthProvider))
	mux.HandleFunc("DELETE /api/v1/admin/auth-providers/{id}", s.require("settings.write", s.deleteAuthProvider))
	mux.HandleFunc("GET /api/v1/admin/feature-flags", s.require("settings.read", s.listFeatureFlags))
	mux.HandleFunc("PUT /api/v1/admin/feature-flags/{key}", s.require("settings.write", s.updateFeatureFlag))
	mux.HandleFunc("GET /api/v1/admin/roles", s.require("roles.manage", s.listRoles))
	mux.HandleFunc("PUT /api/v1/admin/roles/{code}/permissions", s.require("roles.manage", s.updateRolePermissions))
	mux.HandleFunc("GET /api/v1/admin/approvals/policies", s.require("approvals.manage", s.listApprovalPolicies))
	mux.HandleFunc("POST /api/v1/admin/approvals/policies", s.require("approvals.manage", s.createApprovalPolicy))
	mux.HandleFunc("PUT /api/v1/admin/approvals/policies/{id}", s.require("approvals.manage", s.updateApprovalPolicy))
	mux.HandleFunc("DELETE /api/v1/admin/approvals/policies/{id}", s.require("approvals.manage", s.deleteApprovalPolicy))
	mux.HandleFunc("GET /api/v1/admin/approvals/requests", s.require("approvals.manage", s.listApprovalRequests))
	mux.HandleFunc("GET /api/v1/admin/approvals/requests/{id}", s.require("approvals.manage", s.getAdminApprovalRequest))
	mux.HandleFunc("POST /api/v1/admin/approvals/requests/{id}/decision", s.require("approvals.manage", s.decideApproval))
	mux.HandleFunc("GET /api/v1/admin/events", s.require("events.manage", s.listDomainEvents))
	mux.HandleFunc("POST /api/v1/admin/events/{id}/retry", s.require("events.manage", s.retryDomainEvent))
	mux.HandleFunc("GET /api/v1/admin/events/deliveries", s.require("events.manage", s.listAdminDeliveries))
	mux.HandleFunc("POST /api/v1/admin/events/deliveries/{id}/retry", s.require("events.manage", s.retryAdminDelivery))
	mux.HandleFunc("GET /api/v1/admin/notifications/templates", s.require("settings.read", s.listNotificationTemplates))
	mux.HandleFunc("PUT /api/v1/admin/notifications/templates/{key}", s.require("settings.write", s.putNotificationTemplate))
	mux.HandleFunc("GET /api/v1/admin/notes", s.require("admin.access", s.listOperatorNotes))
	mux.HandleFunc("POST /api/v1/admin/notes", s.require("admin.access", s.createOperatorNote))
	mux.HandleFunc("DELETE /api/v1/admin/notes/{id}", s.require("admin.access", s.deleteOperatorNote))
	mux.HandleFunc("GET /api/v1/admin/audit", s.require("audit.read", s.listAuditLogs))
	mux.HandleFunc("GET /api/v1/admin/settlements", s.require("orders.manage", s.listSettlements))
	mux.HandleFunc("POST /api/v1/admin/settlements/{id}/action", s.require("orders.manage", s.settlementAction))
	mux.HandleFunc("POST /mcp", s.require("mcp.use", s.requireFeature("agent_marketplace", s.mcpPost)))
	mux.HandleFunc("GET /mcp", s.mcpGet)
	mux.HandleFunc("DELETE /mcp", s.mcpDelete)
	mux.Handle(analytics.MomentoProxyPath+"/", s.momentoProxy())
	mux.Handle("/", s.spaHandler())
	return s.middleware(mux)
}

func (s *Server) spaHandler() http.Handler {
	assets, err := fs.Sub(ui.Files, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(assets))
	// The shell is read once: it is the file the tracking snippet is written
	// into, per request, with that request's nonce.
	shell, shellErr := fs.ReadFile(assets, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isNonPagePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if file, err := assets.Open(path); err == nil {
				_ = file.Close()
				if strings.HasPrefix(path, "assets/") {
					// Every name under assets/ carries a content hash, so the
					// file behind it can never change. Without this the browser
					// revalidates the framework bundle on every navigation,
					// which is most of what splitting it out was for.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			// A missing asset is a missing asset. Falling through to the app
			// shell would answer a request for a script with HTML, and the
			// browser reports that as a parse error rather than as the 404 it
			// is. This happens for real: a tab left open across a deploy asks
			// for a chunk that no longer exists.
			if strings.HasPrefix(path, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		// The application shell itself must not be cached, or a deploy is
		// invisible to anyone whose browser still holds the old one.
		w.Header().Set("Cache-Control", "no-cache")
		if config := s.analyticsConfig(r.Context()); shellErr == nil && config.Active(r.URL.Path) {
			if snippet := config.Snippet(requestNonce(r)); snippet != "" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(injectSnippet(shell, snippet, config.Placement)))
				return
			}
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
