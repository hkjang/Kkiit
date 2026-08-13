package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/hkjang/Kkiit/internal/ui"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /api/v1/version", s.version)
	mux.HandleFunc("GET /api/v1/auth/providers", s.listEnabledProviders)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/register", s.register)
	mux.HandleFunc("POST /api/v1/auth/logout", s.require("", s.logout))
	mux.HandleFunc("GET /api/v1/auth/oauth/{slug}/start", s.oauthStart)
	mux.HandleFunc("GET /api/v1/auth/oauth/{slug}/callback", s.oauthCallback)
	mux.HandleFunc("GET /api/v1/me", s.require("", s.me))
	mux.HandleFunc("PATCH /api/v1/me", s.require("", s.updateMe))
	mux.HandleFunc("GET /api/v1/me/mfa", s.require("", s.mfaStatus))
	mux.HandleFunc("POST /api/v1/me/mfa/totp/setup", s.require("", s.setupTOTP))
	mux.HandleFunc("POST /api/v1/me/mfa/totp/confirm", s.require("", s.confirmTOTP))
	mux.HandleFunc("DELETE /api/v1/me/mfa/totp", s.require("", s.disableTOTP))
	mux.HandleFunc("GET /api/v1/me/seller-profile", s.require("", s.getMySellerProfile))
	mux.HandleFunc("PUT /api/v1/me/seller-profile", s.require("", s.putMySellerProfile))
	mux.HandleFunc("GET /api/v1/me/talents", s.require("talents.write", s.listMyTalents))
	mux.HandleFunc("GET /api/v1/me/api-keys", s.require("keys.manage.self", s.listMyAPIKeys))
	mux.HandleFunc("POST /api/v1/me/api-keys", s.require("keys.manage.self", s.createMyAPIKey))
	mux.HandleFunc("POST /api/v1/me/api-keys/{id}/rotate", s.require("keys.manage.self", s.rotateMyAPIKey))
	mux.HandleFunc("DELETE /api/v1/me/api-keys/{id}", s.require("keys.manage.self", s.revokeMyAPIKey))
	mux.HandleFunc("GET /api/v1/categories", s.listCategories)
	mux.HandleFunc("POST /api/v1/ai/talents/draft", s.require("talents.write", s.aiTalentDraft))
	mux.HandleFunc("POST /api/v1/ai/requirements/analyze", s.require("orders.buy", s.aiRequirementAnalysis))
	mux.HandleFunc("GET /api/v1/talents", s.listTalents)
	mux.HandleFunc("POST /api/v1/talents", s.require("talents.write", s.createTalent))
	mux.HandleFunc("GET /api/v1/talents/{id}", s.getTalent)
	mux.HandleFunc("GET /api/v1/recommendations", s.recommendTalents)
	mux.HandleFunc("GET /api/v1/rfqs", s.require("", s.listRFQs))
	mux.HandleFunc("POST /api/v1/rfqs", s.require("orders.buy", s.createRFQ))
	mux.HandleFunc("GET /api/v1/rfqs/{id}/quotes", s.require("", s.listQuotes))
	mux.HandleFunc("POST /api/v1/quotes", s.require("orders.sell", s.createQuote))
	mux.HandleFunc("PUT /api/v1/talents/{id}", s.require("talents.write", s.updateTalent))
	mux.HandleFunc("POST /api/v1/talents/{id}/publish", s.require("talents.write", s.publishTalent))
	mux.HandleFunc("GET /api/v1/orders", s.require("", s.listOrders))
	mux.HandleFunc("POST /api/v1/orders", s.require("orders.buy", s.createOrder))
	mux.HandleFunc("GET /api/v1/orders/{id}", s.require("", s.getOrder))
	mux.HandleFunc("POST /api/v1/orders/{id}/transition", s.require("", s.transitionOrder))
	mux.HandleFunc("POST /api/v1/orders/{id}/pay", s.require("orders.buy", s.payOrder))
	mux.HandleFunc("POST /api/v1/orders/{id}/deliveries", s.require("orders.sell", s.createDelivery))
	mux.HandleFunc("POST /api/v1/orders/{id}/revision", s.require("orders.buy", s.createRevision))
	mux.HandleFunc("POST /api/v1/orders/{id}/accept", s.require("orders.buy", s.acceptOrder))
	mux.HandleFunc("GET /api/v1/orders/{id}/messages", s.require("", s.listMessages))
	mux.HandleFunc("POST /api/v1/orders/{id}/messages", s.require("", s.createMessage))
	mux.HandleFunc("GET /api/v1/orders/{id}/messages/ws", s.require("", s.messageWebSocket))
	mux.HandleFunc("POST /api/v1/orders/{id}/review", s.require("orders.buy", s.createReview))
	mux.HandleFunc("POST /api/v1/orders/{id}/files", s.require("", s.uploadOrderFile))
	mux.HandleFunc("GET /api/v1/files/{id}", s.require("", s.downloadFile))
	mux.HandleFunc("GET /api/v1/admin/settings", s.require("settings.read", s.listSettings))
	mux.HandleFunc("GET /api/v1/admin/dashboard", s.require("audit.read", s.adminDashboard))
	mux.HandleFunc("GET /api/v1/admin/talents", s.require("talents.review", s.listAdminTalents))
	mux.HandleFunc("GET /api/v1/admin/orders", s.require("orders.manage", s.listAdminOrders))
	mux.HandleFunc("GET /api/v1/admin/risk", s.require("risk.manage", s.listAdminRiskQueue))
	mux.HandleFunc("GET /api/v1/admin/users", s.require("users.manage", s.listAdminUsers))
	mux.HandleFunc("PATCH /api/v1/admin/users/{id}", s.require("users.manage", s.updateAdminUser))
	mux.HandleFunc("PUT /api/v1/admin/users/{id}/roles", s.require("users.manage", s.updateAdminUserRoles))
	mux.HandleFunc("PUT /api/v1/admin/settings/{key}", s.require("settings.write", s.putSetting))
	mux.HandleFunc("GET /api/v1/admin/auth-providers", s.require("settings.read", s.listAuthProviders))
	mux.HandleFunc("POST /api/v1/admin/auth-providers", s.require("settings.write", s.createAuthProvider))
	mux.HandleFunc("PUT /api/v1/admin/auth-providers/{id}", s.require("settings.write", s.updateAuthProvider))
	mux.HandleFunc("DELETE /api/v1/admin/auth-providers/{id}", s.require("settings.write", s.deleteAuthProvider))
	mux.HandleFunc("GET /api/v1/admin/roles", s.require("roles.manage", s.listRoles))
	mux.HandleFunc("PUT /api/v1/admin/roles/{code}/permissions", s.require("roles.manage", s.updateRolePermissions))
	mux.HandleFunc("GET /api/v1/admin/approvals/policies", s.require("approvals.manage", s.listApprovalPolicies))
	mux.HandleFunc("POST /api/v1/admin/approvals/policies", s.require("approvals.manage", s.createApprovalPolicy))
	mux.HandleFunc("PUT /api/v1/admin/approvals/policies/{id}", s.require("approvals.manage", s.updateApprovalPolicy))
	mux.HandleFunc("GET /api/v1/admin/approvals/requests", s.require("approvals.manage", s.listApprovalRequests))
	mux.HandleFunc("POST /api/v1/admin/approvals/requests/{id}/decision", s.require("approvals.manage", s.decideApproval))
	mux.HandleFunc("GET /api/v1/admin/audit", s.require("audit.read", s.listAuditLogs))
	mux.HandleFunc("GET /api/v1/admin/settlements", s.require("orders.manage", s.listSettlements))
	mux.HandleFunc("POST /api/v1/admin/settlements/{id}/action", s.require("orders.manage", s.settlementAction))
	mux.HandleFunc("POST /mcp", s.require("mcp.use", s.mcpPost))
	mux.HandleFunc("GET /mcp", s.mcpGet)
	mux.HandleFunc("DELETE /mcp", s.mcpDelete)
	mux.Handle("/", spaHandler())
	return s.middleware(mux)
}

func spaHandler() http.Handler {
	assets, err := fs.Sub(ui.Files, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/health/") || strings.HasPrefix(r.URL.Path, "/mcp") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if file, err := assets.Open(path); err == nil {
				_ = file.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
