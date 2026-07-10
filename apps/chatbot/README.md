# chatbot (mesh app)

Windows/Linux/Mac에서 동작하는 Go 텔레그램 챗봇. iSANN **app-mesh** 백엔드로,
isannd가 spawn하거나 단독 실행한다.

로컬에 설치된 wrapped AI API(에이전트)와 연동한다 — 하네스·툴 처리는 AI 서버 내부에서 수행되고,
봇은 프롬프트만 보내고 `answer` 만 받는다. `/start`, `/help` 명령과, 일반 텍스트는 **실제 AI 응답**으로
답한다(응답 대기 중에는 "생각 중" 스티커 표시). **인증(credential)은 아직 미연동.**

## 구조

이 앱은 mesh repo(`github.com/isannai/mesh`)의 단일 모듈 안에 station과 동일한 패턴으로 들어 있다.

```
cmd/chatbot/            # 엔트리포인트 (main)
pkg/chatbot/
├── config/             # 설정 로드 (JSON + env override)
├── ai/                 # AI 연동 seam (Client 인터페이스 + HTTP 클라)
└── telegram/           # 텔레그램 봇 (핸들러 등록 + long-polling)
apps/chatbot/
├── mesh.json           # app-mesh manifest (bin + args + config schema, inbound door 없음)
├── conf/chatbot.json   # 비-시크릿 기본값 (토큰은 여기 X — mesh config로)
├── public/image/       # "생각 중" 애니메이션 에셋 (선택)
└── docs/telegram-token.md
```

## 설정

이 앱은 iSANN **app-mesh** 로 돌아가므로 설정이 두 갈래다.

**① 시크릿(텔레그램 토큰 등) — `isann mesh config` 로 넣는다. 파일 편집이 아니다.**
노드 로컬 `secrets.json`(0600)에 저장되고 시작 시 env 로 주입된다 — **zip/git 에 안 들어가고 재배포에도 안 날아간다.**

```bash
isann mesh config chatbot --set TELEGRAM_BOT_TOKEN=<BotFather 토큰>
# (선택) 접근 자격증명:
isann mesh config chatbot --set AI_CREDENTIAL=<BASE64 서명>
```

토큰 미설정 시 isannd 가 **기동을 거부**한다(설정하라는 안내 출력). → 토큰 발급: [docs/telegram-token.md](docs/telegram-token.md)

**② 비-시크릿 기본값 — `conf/chatbot.json` 에서 조정** (endpoint / engine / preset / node_id / …).
`telegram.token` 은 **비워둔다** — 토큰은 ①로 들어온다:

```json
{
  "telegram": { "token": "", "thinking_animation": "", "thinking_sticker": "" },
  "ai": {
    "endpoint": "http://127.0.0.1:8443/internal/api/agent/run",
    "engine": "llama",
    "preset": "default",
    "node_id": "default",
    "max_turns": 4,
    "credential": "",
    "timeout": "30s"
  }
}
```

봇은 일반 텍스트를 받으면 `endpoint` 로 POST 한다. 요청 body:
`{prompt, engine, preset, nodes:[node_id], max_turns}` → 응답의 `answer` 를 돌려준다.

- `telegram.token` — @BotFather 에서 발급받은 봇 토큰. → 발급 방법: [docs/telegram-token.md](docs/telegram-token.md)
- `ai.endpoint` — 에이전트 실행 URL(isannd loopback). 클라이언트가 `?stream=0`(한번에) 을 붙인다.
- `ai.engine` — 추론 엔진. 예: `"llama"`.
- `ai.preset` — 서버측 추론 프리셋 이름. 백엔드가 이 프리셋에 묶인 추론 옵션(temperature 등)과
  **시스템 프롬프트**를 알아서 적용한다. 봇은 이름만 보낸다.
- `ai.node_id` — 추론 노드. body 의 `nodes:["<node_id>"]` 로 전송, backend 가 해석한다
  (`this` / `default` / 특정 노드 id).
- `ai.max_turns` — 에이전트 추론 턴 상한.
- `ai.credential` — 접근 자격증명(BASE64 EOA 서명). **인증은 아직 미연동 — 로드만 되고 전송 안 함.**

env 키(`TELEGRAM_BOT_TOKEN` / `AI_CREDENTIAL` / `AI_ENDPOINT` / `AI_NODE_ID`)는 `conf/chatbot.json` 값보다 우선한다 — ①의 `isann mesh config` 가 바로 이 env 주입이다(mesh.json `config.env` 스키마에 선언).

## 빌드

mesh 빌드 스크립트가 station과 함께 `chatbot-<os>-<arch>.zip` 을 산출한다(mesh.json + bin + conf.example + public):

```bat
:: Windows (repo 루트에서)
build\windows\mesh.bat 0.1.1
:: Linux (cross-compile)
build\linux\mesh.bat 0.1.1
```

> 단독 빌드: `go build -ldflags "-s -w" -o bin/chatbot ./cmd/chatbot`

## 실행

app-mesh 등록·기동:

```bash
isann mesh register chatbot
isann mesh config chatbot --set TELEGRAM_BOT_TOKEN=123:abc
isann mesh start chatbot        # isann mesh on chatbot = 부팅 자동시작
```

isannd 가 `bin/chatbot -config conf/chatbot.json` 으로 spawn 하고, 시작 시 토큰을 env 로 주입한다.
설정 확인: `isann mesh config chatbot` (토큰은 `********` 마스킹), 상태: `isann mesh status`.

단독 실행(디버그) 시엔 앱 루트에서(상대경로 `./conf` 해석):
```powershell
$env:TELEGRAM_BOT_TOKEN='123:abc'; .\bin\chatbot.exe --config .\conf\chatbot.json
```

텔레그램에서 봇과 대화:
- `/start` → 인사
- `/help` → 명령어 안내
- 그 외 텍스트 → **"🤔 생각 중..." 표시(+ 입력 중 애니메이션)** 후 응답으로 교체.

`Ctrl+C` 로 종료한다.

### "생각 중" GIF (선택)

`telegram.thinking_animation` 에 값을 넣으면 텍스트 placeholder 대신 GIF 를 띄운다
(비우면 텍스트 "🤔 생각 중..." 사용). 세 가지 형태를 자동 인식한다:

| 값 | 동작 | 전송량 |
| --- | --- | --- |
| `https://.../x.gif` (URL) | 텔레그램이 URL 에서 가져옴 | 파일 전송 0 |
| `./public/image/thinking.webp` (로컬 경로) | **첫 요청 1번만 업로드**, 이후 file_id 재사용 | 첫 1회만 |
| 텔레그램 `file_id` | 그대로 사용 | 파일 전송 0 |

로컬 파일은 봇 시작 후 첫 전송 시 업로드되고, 그때 텔레그램이 돌려준 `file_id` 를 메모리에
캐시해 이후에는 재업로드하지 않는다. GIF 일 때는 응답이 오면 GIF 를 지우고 답을 새 메시지로 보낸다.
(URL 은 인터넷에서 텔레그램 서버가 접근 가능한 공개 주소여야 하며, localhost/사내망은 불가.)

> 같은 토큰은 한 프로세스만 long-polling 할 수 있다(중복 실행 시 텔레그램이 409 Conflict 반환).
