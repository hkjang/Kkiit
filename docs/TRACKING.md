# 방문 추적 스크립트 설정

관리자 화면 **시스템 → 방문 추적** (`/admin/tracking`) 에서 어떤 화면이 실제로 쓰이는지 재는
스크립트를 붙일 수 있습니다. 새로 설치한 곳에서는 **꺼져 있고**, 켜기 전까지 페이지와 보안
정책은 아무것도 달라지지 않습니다.

이 문서는 관리자 가이드의 한 절입니다. 설정 방법과, 스니펫이 조용히 막히는 이유인 콘텐츠
보안 정책(CSP)을 설명합니다.

## 설정 항목

설정은 `analytics.tracking` 한 줄에 저장되며 전체 설정 화면에서도 편집할 수 있습니다.
`방문 추적` 화면은 같은 값을 제공자별로 필요한 칸만 보여 줍니다.

| 항목 | 뜻 | 기본값 |
|---|---|---|
| `enabled` | 켜야 붙습니다 | 꺼짐 |
| `provider` | `momento` · `ga4` · `gtm` · `matomo` · `custom` · `none` | `momento` |
| `momento_url` · `momento_site_id` | Momento 수집기 주소와 사이트 id | 비어 있음 |
| `momento_proxy` | 브라우저 대신 서버가 `/momento/*` 를 수집기로 넘깁니다 | 켜짐 |
| `measurement_id` | GA4 측정 id(`G-…`) 또는 GTM 컨테이너 id(`GTM-…`) | 비어 있음 |
| `matomo_url` · `matomo_site_id` | Matomo 주소와 사이트 id | 비어 있음 |
| `custom_snippet` | 붙여넣은 추적 코드. **8KB 를 넘으면 저장되지 않습니다** | 비어 있음 |
| `allowed_hosts` | 스니펫에서 자동으로 읽지 못한 출처를 더하는 자리. 한 줄에 하나 | 비어 있음 |
| `include_admin` | `/admin` 화면에서도 추적할지 | 아니오 |
| `placement` | `head` 끝 또는 `body` 끝 | `head` |

켜기 전에는 빈 채로 저장할 수 있습니다. 켜는 순간 제공자가 필요로 하는 값(수집기 주소와
사이트 id, 측정 id, 붙여넣은 코드)이 비어 있으면 저장이 거부되고 이유가 표시됩니다.
값의 변경은 감사 로그에 `settings.update` 로 남으며, 다음 페이지 요청부터(늦어도 10초 안에)
적용됩니다.

## Momento 를 먼저 씁니다

Momento 는 사내에서 직접 운영하는 수집기입니다. 방문 데이터가 밖으로 나가지 않는 유일한
선택지이므로 제공자 목록의 첫 자리에 있고 기본값입니다.

1. 수집기 주소(예: `https://momento.corp.example`)와 사이트 id 를 넣습니다.
2. **같은 오리진 프록시**를 켜 둡니다(기본). 페이지에는 다음 태그가 붙습니다.

   ```html
   <script nonce="…" async src="/momento/tracker.js"
           data-site-id="<사이트 id>" data-environment="prd"
           data-contract-version="1" data-endpoint="/momento"></script>
   ```

   브라우저는 Kkiit 에만 요청하고, Kkiit 가 `/momento/*` 를 수집기 주소로 넘깁니다.
   이때 방문자의 세션 쿠키와 인증 헤더는 떼어 냅니다. 보안 정책에 외부 출처가 아예
   등장하지 않으므로 정책을 바꿀 수 없는 설치에서도 동작합니다. 프록시는 Momento 가 켜진
   동안에만 열리고, 그 외에는 `/momento/*` 가 404 입니다.
3. 프록시를 끄면 브라우저가 수집기 주소를 직접 부르고, 그 출처가 정책에 자동으로 더해집니다.

## CSP — 스니펫이 조용히 막히는 이유

Kkiit 의 페이지는 `script-src 'self'` 로 잠겨 있습니다. 자기 오리진의 스크립트만 실행되고,
인라인 스크립트와 외부 스크립트는 브라우저가 **아무 말 없이** 버립니다. 추적 코드를 그냥
붙이면 화면은 멀쩡한데 수집기에는 아무것도 들어오지 않고, 이유는 브라우저 콘솔에만 나옵니다.

정책을 `'unsafe-inline'` 으로 푸는 방법은 **쓰지 않습니다.** 한 번 풀면 그 앱의 모든 인라인
스크립트가 함께 허용되고, 추적을 끈 뒤에도 정책은 느슨한 채로 남습니다. 대신 다음 세 가지를
합니다.

- **요청마다 nonce.** 페이지 요청마다 무작위 값을 만들어 스니펫의 **모든** `<script>` 태그에
  `nonce="…"` 로 붙이고, 같은 값을 `script-src 'nonce-…'` 로 넣습니다. 붙여넣은 코드에
  이미 nonce 가 있는 태그는 건드리지 않습니다.
- **출처는 스니펫에서 읽습니다.** 추적 도구는 자기 주소를 로더 안에 적어 둡니다. 붙여넣은
  코드의 `http(s)://…` 주소를 긁어 `script-src` · `connect-src` · `img-src` 에 더합니다.
  GA4·GTM 은 Google 출처를, Matomo 와 직접 호출하는 Momento 는 그 주소를 더합니다.
- **막힌 것을 기록합니다.** 추적이 켜진 동안에만 정책에 `report-uri` 를 넣어, 브라우저가
  막은 출처와 지시어(`script-src` 등)를 받아 둡니다. 같은 차단이 페이지마다 반복되므로
  횟수가 아니라 서로 다른 출처가 중요하고, 최근 100건만 메모리에 남습니다(재시작하면
  비워집니다).

추적을 끄면 정책은 원래 문자열로 돌아갑니다. nonce 도, 외부 출처도, `report-uri` 도 남지
않습니다.

### 붙이지 않는 곳

- `/api/*` · `/health/*` · `/mcp` · `/momento/*` 같은 비화면 경로에는 스니펫이 없고, 정책은
  오히려 더 좁은 `default-src 'none'` 입니다.
- `/admin` 아래 화면은 `include_admin` 을 켰을 때만 붙습니다. 운영자의 화면 이동은 대개
  누구도 원하는 방문 데이터가 아닙니다.
- 로그인 화면에도 다른 화면과 같은 스니펫이 붙습니다. 추적 도구가 입력값을 보내지 않도록
  수집기 쪽에서 개인 식별 값을 걸러야 합니다.

## 막힌 출처를 허용하기

1. 추적을 켠 뒤 마켓 화면을 한 번 엽니다.
2. `방문 추적` 화면 아래 **막힌 출처** 에 브라우저가 신고한 주소가 지시어와 함께 보입니다.
3. **허용 목록에 추가** 를 누르면 `allowed_hosts` 에 그 출처가 들어가고 감사 로그에 남습니다.
   지금 설정이 이미 허용하는 출처는 `지금 설정이 허용함` 으로 표시됩니다.
4. **기록 비우기** 를 누르고 페이지를 다시 열어 아직 막히는 것이 있는지 확인합니다.

같은 출처가 허용 뒤에도 다시 신고되면 브라우저가 옛 페이지를 붙들고 있는 것입니다.
새로고침한 뒤 다시 확인하세요.

## 확인 방법

```bash
# 꺼져 있을 때: 스니펫 없음, 정책 원래대로
curl -sD - -o /dev/null https://kkiit.example/ | grep -i content-security-policy

# 켠 뒤: script-src 에 'nonce-…' 와 출처, 끝에 report-uri
curl -sD - -o page.html https://kkiit.example/talents/1 | grep -i content-security-policy
grep -o '<script nonce[^>]*>' page.html

# 비화면 경로: 좁은 정책
curl -sD - -o /dev/null https://kkiit.example/api/v1/version | grep -i content-security-policy
```

## API

| 메서드·경로 | 권한 | 뜻 |
|---|---|---|
| `PUT /api/v1/admin/settings/analytics.tracking` | `settings.write` | 설정 저장. 8KB 초과·잘못된 제공자·주소 없이 켜기는 400 |
| `GET /api/v1/admin/analytics/violations` | `settings.read` | 막힌 출처 목록 |
| `DELETE /api/v1/admin/analytics/violations` | `settings.write` | 기록 비우기 |
| `POST /api/v1/admin/analytics/violations/allow` | `settings.write` | `{"origin":"https://…"}` 를 허용 목록에 추가 |
| `POST /api/v1/analytics/csp-report` | 없음 | 브라우저의 위반 신고. 본문 8KB, IP 당 분당 60회, 항상 204 |
