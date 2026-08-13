package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

func TestMCPToolContractsHaveSchemas(t *testing.T) {
	tools := mcpTools()
	if len(tools) != 12 {
		t.Fatalf("tool count=%d", len(tools))
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
