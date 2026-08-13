# Kkiit MCP

Kkiit는 MCP protocol `2025-11-25`의 Streamable HTTP endpoint를 `/mcp`에 제공합니다. 현재 구현은 서버 이벤트가 필요 없는 stateless JSON 응답 모드이며, `GET /mcp`는 명세에 따라 `405 Method Not Allowed`를 반환합니다.

개인화 페이지에서 API Key를 만들고 `mcp.use` 및 사용할 거래 scope를 선택합니다.

```bash
curl http://kkiit.internal:8080/mcp \
  -H 'Authorization: Bearer kkiit_발급키' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"example","version":"1.0"}}}'
```

도구 목록:

```bash
curl http://kkiit.internal:8080/mcp \
  -H 'Authorization: Bearer kkiit_발급키' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

변경 도구(`create_order`, `submit_delivery`, `accept_delivery` 등)는 Agent가 사용자 승인을 받은 뒤 호출해야 합니다. 서버는 API Key scope와 주문 buyer/seller 소유권을 다시 검증합니다.

| 도구 | 기능 | 주요 scope |
|---|---|---|
| `search_talents` | 공개 상품 검색 | `mcp.use` |
| `recommend_talents` | 요구사항·예산 기반 추천과 점수 | `mcp.use` |
| `get_talent` | 패키지·요구사항 포함 상품 조회 | `mcp.use` |
| `create_quote_request` | 구조화된 RFQ 등록 | `orders.buy` |
| `compare_quotes` | 접수 견적 비교 | `orders.buy` |
| `create_order` | 상품 주문 | `orders.buy` |
| `get_order_status` | 주문 Workspace와 Timeline 조회 | `mcp.use` + 소유권 |
| `send_message` | 주문 메시지 전송 | `mcp.use` + 소유권 |
| `submit_requirement` | 주문 요구사항 확정 | `orders.buy` + 소유권 |
| `submit_delivery` | 결과물 납품 | `orders.sell` + 소유권 |
| `request_revision` | 구조화된 수정 요청 | `orders.buy` + 소유권 |
| `accept_delivery` | 구매확정과 정산 예정 생성 | `orders.buy` + 소유권 |
