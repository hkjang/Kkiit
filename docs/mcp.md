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
| `search_talents` | 공개 상품 검색(카테고리·가격·납기·정렬) | `mcp.use` |
| `list_categories` | 카테고리 트리 조회 | `mcp.use` |
| `list_reviews` | 상품 후기 본문과 점수 분포 | `mcp.use` |
| `ask_seller` | 주문 전 판매자 문의 | `mcp.use` |
| `list_inquiries` | 주고받은 문의와 답변 조회 | `mcp.use` |
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
| `list_orders` | 내가 당사자인 주문 목록 | `mcp.use` |
| `preview_coupon` | 쿠폰 적용 시 할인액과 결제 금액 | `orders.buy` |
| `pay_order` | 에스크로 결제, `idempotency_key` 필수 | `orders.buy` + 소유권 |
| `list_notifications` | 알림함 조회로 상태 변화 감지 | `mcp.use` |
| `open_dispute` | 분쟁 접수와 정산 보류 | `mcp.use` + 소유권 |
| `submit_report` | 정책 위반 신고 | `mcp.use` |
| `list_settlements` | 판매자 정산 내역과 지급 예정 | `orders.sell` |

## Agent 거래 루프

주문 하나를 끝까지 진행하려면 다음 순서면 충분합니다.

1. `search_talents`(예산이 있다면 `price_max`, 급하면 `max_delivery_days`) 또는 `recommend_talents`로 후보를 찾습니다. 분류를 먼저 좁히려면 `list_categories`를 봅니다.
2. **주문 전에 조사합니다.** `list_reviews`로 평점 숫자가 아니라 실제 구매자가 쓴 내용을 읽고, 확실하지 않은 점은 `ask_seller`로 물어 `list_inquiries`에서 답을 확인합니다. 사람이 주문 전에 하는 일을 Agent도 할 수 있어야 사용자를 대신해 고를 수 있습니다.
3. `preview_coupon`으로 실제 결제 금액을 확인하고 사용자 승인을 받습니다.
4. `create_order`에 `coupon_code`를 함께 보내 주문을 만듭니다.
5. `pay_order`를 호출합니다. **`idempotency_key`는 Agent가 만들어 보관해야 하며, 재시도할 때 같은 값을 다시 보내야 중복 결제되지 않습니다.** 같은 키로 다시 호출하면 `idempotent_replay: true`가 돌아옵니다.
6. `list_notifications`를 주기적으로 호출해 상태 변화를 감지합니다. 주문마다 폴링할 필요 없이 알림함 하나만 보면 됩니다.
7. 납품이 오면 `accept_delivery`로 구매확정하거나, `request_revision`으로 수정을 요청하거나, 해결되지 않으면 `open_dispute`로 분쟁을 접수합니다.

`tools/list`는 항상 전체 목록을 반환하지만, 키 scope에 없는 도구를 호출하면 필요한 권한 이름과 함께 오류가 돌아옵니다.
