# Kkiit

Kkiit는 사람·기업·AI Agent의 전문 서비스를 `재능 상품 → 요구사항 → 주문 → 작업 → 납품 → 구매확정 → 정산` 흐름으로 연결하는 오프라인 운영 가능 마켓플레이스입니다.

현재 버전은 운영 기반과 첫 거래 흐름을 제공합니다.

- Go 모듈러 모놀리스와 React + TypeScript + MUI 단일 웹 애플리케이션
- PostgreSQL 마이그레이션, FTS 검색, Transactional Outbox, 정산 원장
- Argon2id 로컬 로그인, TOTP MFA, OIDC/OAuth2 프리셋, 세션, RBAC, 불변 감사로그
- 개인 API 키 생성·회전·폐기, Scope·CIDR·Rate Limit 정책 모델
- 재능, 패키지, Smart Order Form, 주문·에스크로, 실시간 메시지, 파일, 납품, 수정, 구매확정·리뷰
- AI 상품 초안·요구 분석의 로컬 fallback, 검색·추천, RFQ와 견적 비교
- 관리자 KPI, 사용자·상품·주문·정산·위험, 인증 제공자, 변경 가능한 역할 권한, 조건부 승인 대기열
- REST API와 MCP Streamable HTTP의 stateless JSON 모드
- 단일 런타임 Docker 이미지와 오프라인 `.tar.gz` 릴리스

## UI 선택

UI 프레임워크는 **MUI**를 사용합니다. 재능 마켓의 소비자 화면과 데이터 밀도가 높은 관리자 콘솔을 같은 접근성 기준으로 유지하기 쉽고, 키보드 포커스·폼·Dialog·반응형 레이아웃의 기본 품질이 안정적이기 때문입니다. 기본 본문은 16px, 입력과 주요 버튼은 최소 44px 높이로 구성합니다. 관리자 탐색 메뉴는 서비스 색상에 맞춘 thin scrollbar를 별도로 적용했습니다.

## 실행 계약

Kkiit 프로세스가 읽는 환경변수는 다음 네 개뿐입니다.

| 이름 | 용도 |
|---|---|
| `POSTGRES_DSN` | PostgreSQL 연결 문자열 |
| `BOOTSTRAP_ADMIN` | 최초 Super Admin 아이디 또는 이메일 |
| `BOOTSTRAP_ADMIN_PASSWORD` | 최초 관리자 비밀번호, 최소 12자 |
| `ENCRYPTION_KEY` | DB 비밀값 암호화용 32바이트 키(Base64 또는 64자리 hex) |

수수료, 인증 제공자, AI Gateway, 승인, 알림, 기능 플래그, API/MCP 등 모든 가변 정책은 관리자 페이지와 PostgreSQL `system_settings`에서 관리합니다. 환경변수 별칭이나 숨은 기본 환경변수는 사용하지 않습니다.

관리자 MFA 강제 정책은 먼저 각 관리자 계정의 개인화 페이지에서 TOTP를 활성화한 뒤 `auth.security.mfa_admin_required`를 켜는 순서로 적용합니다. 활성화 전에 정책부터 켜면 미등록 관리자 로컬 로그인을 의도적으로 차단합니다.

암호화 키 생성 예시:

```bash
openssl rand -base64 32
```

## 개발

요구사항: Go 1.26+, Node.js 24+, PostgreSQL 16+.

```bash
cp .env.example .env
cd web && npm ci && npm run build && cd ..
set -a && . ./.env && set +a
go run -ldflags "-X main.version=$(cat VERSION)" ./cmd/kkiit
```

브라우저에서 `http://localhost:8080`을 엽니다. 앱 시작 시 마이그레이션을 advisory lock 안에서 자동 적용하고 부트스트랩 관리자를 보장합니다.

검증:

```bash
make check
```

## 오프라인 배포

인터넷에 연결된 빌드 환경에서 이미지와 압축 파일을 만듭니다.

```bash
./scripts/release.sh
```

산출 규칙:

- 이미지: `kkiit:v0.1.1`
- 파일: `release/kkiit-v0.1.1.tar.gz`

단절망으로 파일을 반입한 뒤 다음처럼 설치합니다. 조직의 반입 절차에서 생성한 checksum이 있다면 먼저 검증합니다.

```bash
./scripts/load-offline.sh kkiit-v0.1.1.tar.gz
docker compose up -d
```

런타임 이미지는 애플리케이션 바이너리, React 정적 파일, CA 인증서와 시간대 데이터만 포함합니다. 외부 CDN, 폰트, JavaScript, 빌드 도구를 호출하지 않습니다. PostgreSQL은 운영 조직이 제공하는 외부 인스턴스를 사용하며 `pgvector`가 없는 환경에서도 핵심 기능이 동작합니다.

완전 단절망에서는 로컬 로그인을 사용합니다. Google·Naver·Apple·Kakao는 해당 도메인에 접근 가능한 제한망에서만 사용할 수 있고, 내부 Keycloak은 Issuer URL과 Client 정보를 관리자 페이지에서 설정하면 됩니다.

Keycloak 앞에서 `Invalid parameter: redirect_uri` 오류가 발생하면 관리자 `인증 연동`에서 브라우저가 접속하는 **외부 서비스 주소**(예: `https://market.example.com`)를 저장합니다. 각 제공자 카드에 표시되는 전체 콜백 주소를 Keycloak Client의 **Valid redirect URIs**에 그대로 등록해야 합니다. Kkiit는 설정값을 우선 사용하고, 비어 있으면 표준 `Forwarded` 또는 `X-Forwarded-Proto`/`X-Forwarded-Host` 헤더를 반영합니다.

## GitHub Release

[`VERSION`](./VERSION)과 같은 `v버전` 태그를 push하면 `.github/workflows/release.yml`이 이미지를 빌드하고, 저장/삭제/재로드로 archive를 검증한 뒤 다음 파일만 GitHub Release에 게시합니다.

- `kkiit-v버전.tar.gz`

수동으로 GitHub에 push하거나 Release를 생성하지 않습니다. 배포 권한이 있는 사용자가 태그를 push할 때 자동화가 실행됩니다.

## API와 MCP

- REST: `/api/v1`
- OpenAPI: 저장소의 [`docs/openapi.yaml`](./docs/openapi.yaml)
- MCP: `POST /mcp`, protocol `2025-11-25`, Bearer API Key 필요
- 상태: `/health/live`, `/health/ready`

MCP 요청은 `Authorization: Bearer kkiit_...`와 `Accept: application/json, text/event-stream`을 포함해야 합니다. 현재 서버는 세션이 필요 없는 stateless JSON 응답 모드를 제공하며 GET 스트림에는 `405`를 반환합니다. 제공 도구는 `search_talents`, `recommend_talents`, `get_talent`, `create_quote_request`, `compare_quotes`, `create_order`, `get_order_status`, `send_message`, `submit_requirement`, `submit_delivery`, `request_revision`, `accept_delivery`입니다.

## 승인 동작

관리자가 `talent_publish` 승인 정책을 활성화하고 조건이 일치하면 상품 공개 요청은 `review_pending`과 승인 대기열로 이동합니다. 조건에는 `min_amount`, `max_amount`, `service_types`, `seller_levels`, `quality_score_below`를 조합할 수 있습니다. 활성 정책이 없거나 어느 정책의 조건에도 맞지 않으면 승인 요청 자체를 만들지 않고 즉시 공개합니다. 따라서 불필요한 승인·반려 상태가 거래 흐름에 남지 않습니다.

## 디렉터리

```text
cmd/kkiit                 실행 진입점
internal/database         마이그레이션과 부트스트랩
internal/httpapi          REST, 인증, 관리자 API, MCP
internal/cryptox          AES-256-GCM과 토큰 다이제스트
internal/password         Argon2id
internal/ui               빌드된 React UI embed
web                       React + TypeScript + MUI
docs                      아키텍처와 API 계약
scripts                   오프라인 이미지 생성·로딩
```
