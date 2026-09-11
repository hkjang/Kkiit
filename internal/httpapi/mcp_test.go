package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

func TestMCPToolContractsHaveSchemas(t *testing.T) {
	tools := mcpTools()
	// A bare count breaks on every legitimate addition while saying nothing
	// about what was lost. What matters is that the tools an agent needs to get
	// from "find me something" to "it is paid for" are all still advertised.
	required := []string{"search_talents", "get_talent", "list_reviews", "list_categories",
		"ask_seller", "create_order", "pay_order", "get_order_status", "accept_delivery"}
	present := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if name, _ := tool["name"].(string); name != "" {
			present[name] = true
		}
	}
	for _, name := range required {
		if !present[name] {
			t.Fatalf("필수 도구 %s가 목록에서 사라졌습니다", name)
		}
	}
	seen := make(map[string]bool)
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "" || seen[name] {
			t.Fatalf("invalid or duplicate tool %q", name)
		}
		seen[name] = true
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Fatalf("tool %s has invalid schema", name)
		}
	}
}

func TestAPIKeyRateWindow(t *testing.T) {
	server := &Server{}
	id := uuid.UUID{1}
	if !server.allowAPIKey(id, 2) || !server.allowAPIKey(id, 2) {
		t.Fatal("requests inside limit rejected")
	}
	if server.allowAPIKey(id, 2) {
		t.Fatal("request over limit accepted")
	}
}

// A tool an agent can call without a matching key scope would let an MCP key
// act beyond its role, so every mutating tool must declare its permission.
func TestMCPMutatingToolsDeclareAPermission(t *testing.T) {
	mutating := map[string]bool{
		"create_quote_request": true, "create_order": true, "submit_requirement": true, "send_message": true,
		"submit_delivery": true, "request_revision": true, "accept_delivery": true, "pay_order": true,
		"open_dispute": true, "submit_report": true, "preview_coupon": true,
	}
	for _, tool := range mcpTools() {
		name, _ := tool["name"].(string)
		if mutating[name] && mcpToolPermissions[name] == "" {
			t.Fatalf("도구 %s에 필요한 권한이 선언되지 않았습니다", name)
		}
	}
	for name := range mcpToolPermissions {
		found := false
		for _, tool := range mcpTools() {
			if tool["name"] == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("권한만 선언되고 존재하지 않는 도구입니다: %s", name)
		}
	}
}

func TestMCPToolNamesMatchTheDispatchTable(t *testing.T) {
	// Every advertised tool must be reachable; an unrouted name would surface as
	// "알 수 없는 도구" only after an agent tried to use it.
	for _, tool := range mcpTools() {
		name, _ := tool["name"].(string)
		if !mcpRoutable(name) {
			t.Fatalf("도구 %s를 처리하는 경로가 없습니다", name)
		}
	}
}
