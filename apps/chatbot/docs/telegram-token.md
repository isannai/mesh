# 텔레그램 봇 토큰 발급 방법

챗봇을 실행하려면 텔레그램 봇 토큰이 필요합니다. 토큰은 텔레그램 공식 봇인
**@BotFather** 에서 무료로 발급받습니다.

## 1. BotFather 열기

텔레그램 앱 검색창에 `@BotFather` 를 입력하고, 파란 체크(공식 인증)가 달린
BotFather 를 선택합니다.

- 링크로 바로 열기: https://t.me/BotFather

## 2. 봇 생성

1. `/start` 입력 — BotFather 와 대화를 시작합니다.
2. `/newbot` 입력 — 새 봇 생성을 시작합니다.
3. BotFather 가 두 가지를 차례로 물어봅니다:
   - **봇 이름(name)** — 화면에 표시될 이름. 자유롭게 입력 (예: `isann 챗봇`)
   - **봇 사용자명(username)** — 반드시 **`bot` 으로 끝나야** 하고, 전체에서 고유해야 함
     (예: `isann_chat_bot`)

## 3. 토큰 받기

생성에 성공하면 BotFather 가 토큰을 알려줍니다.

```
Done! Congratulations on your new bot.
...
Use this token to access the HTTP API:
123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

위에서 `123456789:AAEx...` 부분이 **봇 토큰**입니다.

## 4. 토큰 넣기

파일에 직접 쓰지 말고 **app-mesh 시크릿으로 넣습니다** — 재배포(업데이트)에도 안 날아갑니다:

```bash
isann mesh config chatbot --set TELEGRAM_BOT_TOKEN=123456789:AAExxxxxxxxxxxxxxxxxxxx
```

isannd 가 노드 로컬 `secrets.json`(0600)에 저장하고, chatbot 시작 시 환경변수 `TELEGRAM_BOT_TOKEN`
으로 주입합니다. `conf/chatbot.json` 의 `telegram.token` 은 비워둡니다.

> 확인: `isann mesh config chatbot` — 토큰은 `********` 로 마스킹돼 표시됩니다.

## 5. 실행 및 확인

```bash
isann mesh register chatbot
isann mesh config chatbot --set TELEGRAM_BOT_TOKEN=123456789:AAEx...
isann mesh start chatbot
```

`isann mesh status` 에서 chatbot 이 running 이면 정상입니다.
텔레그램에서 방금 만든 봇(사용자명으로 검색)과의 대화창을 열고:

- `/start` → 인사 응답
- `/help` → 명령어 안내
- 그 외 텍스트 → 동일 텍스트 에코

## 주의사항

- 🔒 **토큰은 비밀번호와 같습니다.** 노출되면 누구나 봇을 조종할 수 있습니다.
  토큰은 `isann mesh config` 로 넣어 노드 로컬 `secrets.json`(0600)에만 저장되고 git/zip 에는 안 들어갑니다.
- 토큰이 노출됐거나 분실했으면 BotFather 에서 `/revoke` 로 재발급할 수 있습니다.
- ⚠️ **같은 토큰으로 봇을 두 곳에서 동시에 실행하면** 텔레그램이 `409 Conflict` 를
  반환합니다. 한 번에 한 프로세스만 실행하세요.
- 토큰이 틀리면 실행 즉시 `Unauthorized` 로 종료되므로 바로 알 수 있습니다.

## 참고: 유용한 BotFather 명령어

| 명령어 | 설명 |
| --- | --- |
| `/mybots` | 내가 만든 봇 목록 보기 / 관리 |
| `/token` | 기존 봇의 토큰 다시 보기 |
| `/revoke` | 토큰 재발급(기존 토큰 무효화) |
| `/setname` | 봇 이름 변경 |
| `/setdescription` | 봇 설명 변경 |
| `/setcommands` | 명령어 목록 등록(입력창 자동완성에 표시) |
| `/deletebot` | 봇 삭제 |
