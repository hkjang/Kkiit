package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

const mcpProtocolVersion = "2025-11-25"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) mcpGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "POST")
	writeError(w, http.StatusMethodNotAllowed, "stream_not_supported", "이 서버는 stateless JSON 응답 모드를 사용합니다.")
}
func (s *Server) mcpDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "POST")
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (s *Server) mcpPost(w http.ResponseWriter, r *http.Request) {
	if !s.validMCPOrigin(r) {
		writeJSON(w, http.StatusForbidden, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32000, Message: "invalid Origin"}})
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "application/json") && !strings.Contains(r.Header.Get("Accept"), "*/*") {
		writeJSON(w, http.StatusNotAcceptable, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32000, Message: "Accept must include application/json"}})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "Parse error"}})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeJSON(w, 400, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32600, Message: "Invalid Request"}})
		return
	}
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	response := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		response.Result = map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "Kkiit Marketplace", "version": s.Version}, "instructions": "Kkiit 재능 마켓 검색, 주문, 납품 및 구매확정 도구입니다. 변경 작업 전 사용자의 명시적 승인을 받으세요."}
	case "ping":
		response.Result = map[string]any{}
	case "tools/list":
		response.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		result, err := s.callMCPTool(r, req.Params)
		if err != nil {
			response.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}
		} else {
			response.Result = result
		}
	default:
		response.Error = &rpcError{Code: -32601, Message: "Method not found"}
	}
	w.Header().Set("MCP-Protocol-Version", mcpProtocolVersion)
	writeJSON(w, 200, response)
}

func (s *Server) validMCPOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func mcpTools() []map[string]any {
	return []map[string]any{
		{"name": "search_talents", "description": "조건에 맞는 공개 재능 상품을 검색합니다.", "inputSchema": schema(map[string]any{"query": stringProp("자연어 또는 키워드 검색어"), "limit": integerProp("결과 수, 최대 100")}, []string{})},
		{"name": "recommend_talents", "description": "요구사항과 예산을 바탕으로 추천 점수와 이유를 제공합니다.", "inputSchema": schema(map[string]any{"query": stringProp("구매 요구사항"), "budget_max": map[string]any{"type": "integer", "minimum": 0}}, []string{"query"})},
		{"name": "get_talent", "description": "재능 상품의 패키지와 주문 요구사항을 조회합니다.", "inputSchema": schema(map[string]any{"talent_id": stringProp("상품 UUID")}, []string{"talent_id"})},
		{"name": "create_quote_request", "description": "구조화된 프로젝트 견적 요청을 등록합니다.", "inputSchema": schema(map[string]any{"title": stringProp("프로젝트 제목"), "description": stringProp("상세 요구사항"), "requirements": map[string]any{"type": "object"}, "budget_min": map[string]any{"type": "integer", "minimum": 0}, "budget_max": map[string]any{"type": "integer", "minimum": 0}, "currency": stringProp("통화")}, []string{"title", "description"})},
		{"name": "compare_quotes", "description": "견적 요청에 접수된 견적을 적합도, 가격과 납기 순으로 비교합니다.", "inputSchema": schema(map[string]any{"rfq_id": stringProp("견적 요청 UUID")}, []string{"rfq_id"})},
		{"name": "create_order", "description": "선택한 공개 재능 상품을 주문합니다. 실제 호출 전에 사용자 승인이 필요합니다.", "inputSchema": schema(map[string]any{"talent_id": stringProp("상품 UUID"), "package_id": stringProp("패키지 UUID"), "requirements": map[string]any{"type": "object", "description": "구조화된 요구사항"}, "options": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}, []string{"talent_id", "requirements"})},
		{"name": "get_order_status", "description": "접근 가능한 주문의 상태와 타임라인을 조회합니다.", "inputSchema": schema(map[string]any{"order_id": stringProp("주문 UUID")}, []string{"order_id"})},
		{"name": "send_message", "description": "주문 Workspace에 메시지를 전송합니다.", "inputSchema": schema(map[string]any{"order_id": stringProp("주문 UUID"), "body": stringProp("메시지 내용")}, []string{"order_id", "body"})},
		{"name": "submit_requirement", "description": "구매자가 주문 요구사항을 갱신합니다.", "inputSchema": schema(map[string]any{"order_id": stringProp("주문 UUID"), "requirements": map[string]any{"type": "object"}}, []string{"order_id", "requirements"})},
		{"name": "submit_delivery", "description": "판매자가 주문 결과물을 납품합니다.", "inputSchema": schema(map[string]any{"order_id": stringProp("주문 UUID"), "delivery_type": stringProp("file, url 또는 text"), "content": map[string]any{"type": "object"}, "description": stringProp("납품 설명"), "content_hash": stringProp("무결성 해시")}, []string{"order_id", "delivery_type", "content"})},
		{"name": "request_revision", "description": "구매자가 구조화된 수정 요청을 등록합니다.", "inputSchema": schema(map[string]any{"order_id": stringProp("주문 UUID"), "details": stringProp("수정 내용"), "priority": stringProp("우선순위")}, []string{"order_id", "details"})},
		{"name": "accept_delivery", "description": "구매자가 납품을 구매확정하고 정산 예정 원장을 생성합니다.", "inputSchema": schema(map[string]any{"order_id": stringProp("주문 UUID")}, []string{"order_id"})},
	}
}
func schema(properties map[string]any, required []string) map[string]any {
	return map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func integerProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": 1, "maximum": 100}
}

func (s *Server) callMCPTool(r *http.Request, raw json.RawMessage) (any, error) {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil || call.Name == "" {
		return nil, fmt.Errorf("도구 호출 형식이 올바르지 않습니다")
	}
	p, _ := principalFrom(r.Context())
	required := map[string]string{"create_quote_request": "orders.buy", "compare_quotes": "orders.buy", "create_order": "orders.buy", "submit_requirement": "orders.buy", "send_message": "mcp.use", "submit_delivery": "orders.sell", "request_revision": "orders.buy", "accept_delivery": "orders.buy"}
	if permission := required[call.Name]; permission != "" && !hasPermission(p, permission) {
		return nil, fmt.Errorf("필요한 키 권한이 없습니다: %s", permission)
	}
	var method, path, pathName, pathValue string
	payload := call.Arguments
	switch call.Name {
	case "search_talents":
		method = http.MethodGet
		path = "/api/v1/talents"
		query, _ := call.Arguments["query"].(string)
		limit := fmt.Sprint(call.Arguments["limit"])
		if limit == "<nil>" {
			limit = "24"
		}
		path += "?q=" + url.QueryEscape(query) + "&limit=" + url.QueryEscape(limit)
		payload = nil
	case "recommend_talents":
		method = http.MethodGet
		path = "/api/v1/recommendations"
		query, _ := call.Arguments["query"].(string)
		budget := fmt.Sprint(call.Arguments["budget_max"])
		if budget == "<nil>" {
			budget = "0"
		}
		path += "?q=" + url.QueryEscape(query) + "&budget_max=" + url.QueryEscape(budget)
		payload = nil
	case "get_talent":
		method = http.MethodGet
		path = "/api/v1/talents/x"
		pathName = "id"
		pathValue = fmt.Sprint(call.Arguments["talent_id"])
		payload = nil
	case "create_quote_request":
		method = http.MethodPost
		path = "/api/v1/rfqs"
	case "compare_quotes":
		method = http.MethodGet
		path = "/api/v1/rfqs/x/quotes"
		pathName = "id"
		pathValue = fmt.Sprint(call.Arguments["rfq_id"])
		payload = nil
	case "create_order":
		method = http.MethodPost
		path = "/api/v1/orders"
	case "get_order_status":
		method = http.MethodGet
		path = "/api/v1/orders/x"
		pathName = "id"
		pathValue = fmt.Sprint(call.Arguments["order_id"])
		payload = nil
	case "send_message":
		method = http.MethodPost
		path = "/api/v1/orders/x/messages"
		pathName = "id"
		pathValue = fmt.Sprint(call.Arguments["order_id"])
		delete(payload, "order_id")
		payload["attachments"] = []any{}
	case "submit_requirement":
		return s.mcpSubmitRequirement(r, call.Arguments)
	case "submit_delivery":
		method = http.MethodPost
		path = "/api/v1/orders/x/deliveries"
		pathName = "id"
		pathValue = fmt.Sprint(call.Arguments["order_id"])
		delete(payload, "order_id")
	case "request_revision":
		method = http.MethodPost
		path = "/api/v1/orders/x/revision"
		pathName = "id"
		pathValue = fmt.Sprint(call.Arguments["order_id"])
		delete(payload, "order_id")
	case "accept_delivery":
		method = http.MethodPost
		path = "/api/v1/orders/x/accept"
		pathName = "id"
		pathValue = fmt.Sprint(call.Arguments["order_id"])
		payload = map[string]any{}
	default:
		return nil, fmt.Errorf("알 수 없는 도구입니다: %s", call.Name)
	}
	body := io.Reader(nil)
	if payload != nil {
		encoded, _ := json.Marshal(payload)
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, body).WithContext(r.Context())
	if pathName != "" {
		request.SetPathValue(pathName, pathValue)
	}
	recorder := httptest.NewRecorder()
	switch call.Name {
	case "search_talents":
		s.listTalents(recorder, request)
	case "recommend_talents":
		s.recommendTalents(recorder, request)
	case "get_talent":
		s.getTalent(recorder, request)
	case "create_quote_request":
		s.createRFQ(recorder, request)
	case "compare_quotes":
		s.listQuotes(recorder, request)
	case "create_order":
		s.createOrder(recorder, request)
	case "get_order_status":
		s.getOrder(recorder, request)
	case "send_message":
		s.createMessage(recorder, request)
	case "submit_delivery":
		s.createDelivery(recorder, request)
	case "request_revision":
		s.createRevision(recorder, request)
	case "accept_delivery":
		s.acceptOrder(recorder, request)
	}
	if recorder.Code >= 400 {
		return nil, fmt.Errorf("Kkiit API 오류(%d): %s", recorder.Code, strings.TrimSpace(recorder.Body.String()))
	}
	var result any
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			result = recorder.Body.String()
		}
	}
	text, _ := json.MarshalIndent(result, "", "  ")
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "structuredContent": result}, nil
}

func (s *Server) mcpSubmitRequirement(r *http.Request, args map[string]any) (any, error) {
	p, _ := principalFrom(r.Context())
	orderID := fmt.Sprint(args["order_id"])
	requirements, ok := args["requirements"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("requirements 객체가 필요합니다")
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE orders SET requirements=$3,state=CASE WHEN state='REQUIREMENT_PENDING' THEN 'READY' ELSE state END,updated_at=now() WHERE id=$1::uuid AND buyer_id=$2 AND state IN ('CREATED','PAID','REQUIREMENT_PENDING')`, orderID, p.UserID, requirements)
	if err != nil || tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("요구사항을 갱신할 수 있는 주문이 아닙니다")
	}
	s.audit(r, "order.requirements_update", "order", orderID, nil, requirements, "success")
	result := map[string]any{"order_id": orderID, "updated": true}
	text, _ := json.Marshal(result)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "structuredContent": result}, nil
}
