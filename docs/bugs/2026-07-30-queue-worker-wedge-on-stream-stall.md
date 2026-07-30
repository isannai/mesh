# 큐 워커 마비: station↔engine 무한 대기 (타임아웃 부재)

- **상태:** 원인 확정(재현 완료) / 수정 예정
- **작성:** 2026-07-30
- **영향:** station의 추론 큐 전체 마비 — 재시작 전까지 복구 불가
- **관련 코드:** `pkg/station/queue/dispatcher.go`, `pkg/station/queue/queue.go`, `pkg/station/jobs_handler.go`

---

## 1. 증상

- 서비스 `Concurrency=1`인데 `running_count`가 2로 관측됨.
- 그 상태에서 **어떤 추론 요청도 처리되지 않음.** 새 요청을 보내면 **큐(pending)만 하나씩 늘어남.**
- **station을 재시작하면** 즉시 정상화되어 추론이 다시 들어감. (엔진은 재시작하지 않음)
- 트리거: agent가 오래 걸리는 추론을 요청한 뒤 **중간에 agent를 끊거나 타임아웃**이 발생한 경우.

---

## 2. 근본 원인 (코드 + 재현으로 확정)

### 2-1. 진짜로 잠기는 것은 "워커 슬롯"이지 뮤텍스가 아님

- 실제 엔진 호출 `process()`는 `q.mu.Lock()` **이전**에 실행됨 → 추론 중 `q.mu`는 잡히지 않음.
- "큐가 하나씩 늘어남" = 접수(Submit)는 정상, **dispatch만 멈춤** → 뮤텍스 데드락이 아니라 **워커가 이전 작업에 물려 안 움직이는 것**.
- `Concurrency=1`이라 유일한 워커 슬롯(세마포어 permit)이 물리면 서비스 전체가 정지.

### 2-2. station↔engine 읽기에 타임아웃이 전혀 없음

두 경로 모두 무한 대기 가능:

| 경로 | 읽기 코드 | 타임아웃 |
|---|---|---|
| **스트림** | `reader.ReadString('\n')` 루프 (`dispatcher.go:196`, `streamSentenceChunks`) | ❌ 없음 |
| **버퍼** | `io.ReadAll(resp.Body)` (`dispatcher.go:113·122`) | ❌ 없음 |

- 엔진 HTTP 클라이언트는 타임아웃 없는 기본 `&http.Client{}` (`dispatcher.go:65`).
- 잡의 컨텍스트는 **station 생명주기(`Manager.ctx`)** 라서, 오직 station 종료(재시작) 시에만 취소됨.
- **agent 끊김은 신뢰성 있게 감지할 수 없음:** 추론 요청 경로는
  `agent → (control측 isannd) → [QUIC/UDP 터널, 상시 연결] → (station측 isannd) → station 로컬 HTTP`
  이고, station은 isannd가 프론트하는 **로컬 평문 HTTP**로 요청을 받는다 (`station.go:24,225` — "isannd fronts it"). 따라서 핸들러의 `r.Context()`는 **"로컬 isannd → station" 요청**에 묶여 있고, 원격 agent가 아니다. isannd↔isannd 터널은 상시 물려 있어 **원격 agent disconnect가 station까지 전파된다는 보장이 없다.**
- `streamSentenceChunks`는 **ctx를 아예 전달받지도 않음** (`dispatcher.go:188`).
- **결론:** 잡 생존 신호(agent liveness)를 감지할 방법이 없으므로, **disconnect 감지가 아니라 station 자체의 타임아웃**만이 견고한 해법이다.
- 결과: 엔진이 SSE 청크를 흘리다 **멈추면**(연결은 열린 채) 읽기가 영원히 블록 → 워커 슬롯 영구 점유 → 큐 마비. **유일한 탈출구는 `Manager.ctx` 취소 = station 재시작** → "재시작하면 풀림"의 정체.

### 2-3. 재현 테스트

`pkg/station/queue/stream_stall_test.go` — SSE로 청크 1개 뒤 멈추는(=`[DONE]`/close 없음) 가짜 엔진에 대해:
1. processor가 700ms 동안 리턴 안 함 → **유휴/read 타임아웃 없음 확정**
2. ctx 취소해야만 리턴 → **재시작만이 유일한 복구**임을 확정

→ "엔진 무응답"이 아니라 **"스트림 중간 정지 + station 무한 대기"**. 엔진은 정상.

### 2-4. 부수 버그(이미 수정): running_count 오버카운트

- 워커가 세마포어 슬롯 획득 **전에** `dequeue()`로 잡을 running에 넣어, 슬롯 대기 중인 잡까지 running으로 집계 → `Concurrency=1`인데 `running_count=2`.
- **수정 완료**: 슬롯을 dequeue 전에 획득하도록 순서 교정 (`queue.go` `Worker`). 이제 `running ≤ Concurrency` 보장.

---

## 3. 해결 방안

### 3-1. station → engine: 요청 타임아웃 + 세션 종료로 추론 중지

- station이 엔진에 추론 요청할 때 **타임아웃을 설정**한다.
- 타임아웃이 발생하면 **해당 요청 세션(연결)을 끊어** 엔진의 추론을 중지시킨다.
  - Go 기본 동작상 요청 context를 취소하면 그 TCP 연결이 close되며, 대부분의 엔진(llama-server/vLLM)은 클라이언트 disconnect를 감지해 생성을 중단한다.

### 3-2. isannd ↔ station: 추론 요청 시 timeout 값 입력받아 적용

- isannd가 station에 추론을 요청할 때 **timeout 값을 함께 전달**받아 적용한다.
- **기본값(요청에 timeout이 없을 때):**
  - **llm / vllm → 60초**
  - **sd → 600초**

### 3-3. llm / vllm: station↔engine 구간은 항상 stream으로 수신

- **llm, vllm** 계열은 클라이언트 요청과 무관하게 **station이 엔진으로부터 항상 stream으로 받는다.**
- 클라이언트(isannd)로의 출력은 요청 방식에 따라:
  - **버퍼로 요청** → station이 stream을 **버퍼링해서 한 번에** 전달.
  - **stream으로 요청** → **구문 단위(청크)로 버퍼링해서** 전달. (문장 세그먼터: `. ! ? ;` 등, `chunk_mode`로 조정)

> 참고: 출력 측(청크 폴링 `/v1/jobs/{id}/chunk`, 버퍼 결과 `/v1/jobs/{id}/result`, `assembleStreamResult`)과 세그먼터는 **이미 구현되어 있음.** 신규 작업은 ① "llm/vllm 내부 항상 stream화" ② "요청 타임아웃(세션 종료 포함)" 두 가지.

---

## 4. 구현 체크리스트

**1단계 — 타임아웃(마비 해결): 완료 (2026-07-30)**

- [x] station→engine 요청에 실행 타임아웃 적용 + 만료 시 연결 close
      (`dispatcher.go` `MakeManagedProcess`: `reqCtx` + `time.AfterFunc(timeout, cancel)`)
- [x] `streamSentenceChunks`에 context 전달 → **유휴(idle) 타임아웃** (청크마다 `watchdog.Reset`, 무청크 시 cancel → 읽기 에러 → 실패 리턴)
- [x] 버퍼 경로(`io.ReadAll`)에 **총(total) 타임아웃** 적용 (watchdog 미리셋)
- [x] isannd↔station: `?timeout=` 수용(`jobs_handler.go` → `job.Timeout`), 서비스별 기본값 배선
      (`queue_init.go`: text=llm/vllm **60s idle**, image=sd **600s total**)
- [x] 재현 테스트 전환 (`stream_stall_test.go`): 멈춘 스트림 → ctx 취소 없이 자가 복구(에러) / 진행 중 스트림 → 유휴 타임아웃에 안 죽음

**2단계 — llm/vllm 내부 항상 stream화: 완료 (2026-07-30)**

- [x] streamable 서비스(`opts.StreamPath != ""` = 매니페스트에 stream_path 있음, 즉 llm/vllm)는
      **클라 요청과 무관하게 엔진에 `stream:true` 강제** (`dispatcher.go` `forceStreamFlag`, 내부 stream 게이트를 `job.Stream`→`opts.StreamPath`로 변경). sd(이미지)는 stream_path 없어 버퍼 유지.
- [x] 출력: 버퍼 클라이언트(`/result`·`wait=true`)는 재조립된 full JSON, 스트림 클라이언트는 `/chunk` 폴링 — 둘 다 기존 경로로 서빙.
- [x] **버퍼 클라이언트용 응답 재조립 정확성**: content + **tool_calls 델타 병합**(index별 id/type/name·arguments 연결) + usage/finish_reason 보존 (`streamSentenceChunks`의 `mergeToolCallDeltas`, `assembleStreamResult` 확장).
- [x] 테스트(`internal_stream_test.go`): 버퍼 클라(`Stream=false`)가 내부 stream으로 받아 tool_calls·usage 온전히 재조립됨을 검증.

> **효과:** 이제 llm/vllm은 **버퍼 요청이어도 내부는 stream 수신** → 유휴 타임아웃이 균일 적용되어, 긴 정상 생성이 총 타임아웃에 잘리지 않고 진짜 멈춤만 컷됨.

### 실환경 검증 필요 (배포 전)

- [ ] 실제 llama-server / vLLM 엔진 대상으로 **버퍼 요청의 tool_calls 응답**이 네이티브(non-stream) 응답과 동등한지 확인 (logprobs 등 content/tool_calls 외 필드 사용 시 재조립 범위 점검).

---

## 5. 논의 포인트 (구현 시 결정 필요)

- **타임아웃 성격:** llm/vllm을 내부 stream으로 받으므로, 60초를 **총 실행 시간**으로 볼지 **무청크(idle) 시간**으로 볼지 결정 필요.
  - *총 60초*: 정상적으로 60초 넘게 걸리는 긴 생성도 잘림.
  - *유휴 60초(권장)*: 청크가 오면 타이머 리셋 → 진행 중이면 안 죽고 **진짜 멈춘 것만** 컷. 스트림 수신 구조와 잘 맞음.
- sd(버퍼)는 중간 진행이 안 보이므로 **총 타임아웃 600초**로 적용.
