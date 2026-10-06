package station

// m0_inference_auth_test.go — M0(추론 인증 통일) 검증.
//
// M0 는 잡 소유권을 "broker 가 넘겨주던 X-Caller-Address 헤더" 에서
// "받는 door 가 IANN 서명을 직접 recover" 로 옮긴다 (docs/TODO/isann-cli-phase3.md).
// 0-c 까지 적용된 최종 상태 — X-Caller-Address 는 어디서도 신뢰하지 않는다.
//
// 이 파일이 덮는 두 가지:
//   - recoverCaller : 서명에서만 신원 복구 (헤더 완전 무시 = 사칭 불가)
//   - authorizeJob  : 실제 핸들러를 관통하는 per-job 소유권 (end-to-end)

import (
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/isannai/mesh/pkg/auth"
)

// newWallet 은 테스트용 진짜 지갑을 하나 만든다 — secp256k1 키쌍을 생성해
// (개인키, 체크섬 주소) 를 돌려준다. 실제 지갑과 동일한 키/주소라 서명·복구가
// 실제 경로 그대로 돌아간다 (crypto 를 mock 하지 않는다).
func newWallet(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	pk, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pk, crypto.PubkeyToAddress(pk.PublicKey).Hex()
}

// signedHeaders 는 CLI 클라이언트가 message 에 pk 로 서명했을 때 실제로 붙여
// 보내는 두 헤더를 만든다: "Authorization: ISANN <sig>" + "X-ISANN-Message".
// auth.SignMessage 를 그대로 써서 provider 의 RecoverAddress 와 round-trip 한다.
func signedHeaders(t *testing.T, pk *ecdsa.PrivateKey, message string) map[string]string {
	t.Helper()
	sig, err := auth.SignMessage(message, pk)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return map[string]string{
		"Authorization":   "ISANN " + sig,
		"X-ISANN-Message": message,
	}
}

// mergeHeaders 는 두 헤더 맵을 합친다 (b 가 a 를 덮어쓴다). "유효 서명 + 위조
// X-Caller-Address" 처럼 한 요청에 여러 헤더를 동시에 실어야 하는 케이스용.
func mergeHeaders(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// ----------------------------------------------------------------------------
// recoverCaller — 신원의 출처는 오직 서명 (헤더는 무시)
// ----------------------------------------------------------------------------

// TestRecoverCaller 는 recoverCaller 가 신원을 어디서 뽑는지 표로 검증한다.
// 0-c 핵심: 서명이 있으면 그 서명자를, 없으면 "" 를 돌려준다. X-Caller-Address
// 헤더는 *절대* 보지 않는다 → 헤더만으로는 누구도 사칭할 수 없다.
func TestRecoverCaller(t *testing.T) {
	pk, addr := newWallet(t)
	hdr := signedHeaders(t, pk, "infer:msg:1")

	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		// 헤더가 전혀 없으면 익명 — open 모드/free tier.
		{"anonymous — no headers", nil, ""},
		// 유효 서명 → 서명자 주소를 복구.
		{"signature recovers signer", hdr, addr},
		{
			// 서명이 있으면 같이 실린 (위조) X-Caller-Address 는 완전히 무시되고
			// 서명자가 이긴다. 서명된 요청은 헤더로 탈취 불가 (anti-spoof).
			name:    "signature wins, forged X-Caller-Address ignored",
			headers: mergeHeaders(hdr, map[string]string{"X-Caller-Address": "0xdeadbeef00000000000000000000000000000000"}),
			want:    addr,
		},
		{
			// 0-c 핵심: 서명 없이 X-Caller-Address 만 있으면 더 이상 폴백하지
			// 않고 익명("") 이 된다 → 헤더만으로는 소유권을 주장할 수 없다.
			name:    "X-Caller-Address alone no longer confers identity",
			headers: map[string]string{"X-Caller-Address": addr},
			want:    "",
		},
		{
			// 서명이 깨졌으면(복구 실패) 익명. 헤더로 폴백하지 않는다.
			name:    "invalid signature → anonymous (no header fallback)",
			headers: map[string]string{"Authorization": "ISANN deadbeef", "X-ISANN-Message": "m", "X-Caller-Address": addr},
			want:    "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/svc/sd-api/v1/jobs", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := recoverCaller(r); !strings.EqualFold(got, tc.want) {
				t.Errorf("recoverCaller = %q, want %q", got, tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// authorizeJob — 실제 핸들러를 관통하는 소유권 (end-to-end)
// ----------------------------------------------------------------------------

// submitFor 는 주어진 헤더로 job 을 제출하고 job id 를 돌려준다. station 과
// 같은 /svc/sd-api/v1/jobs 제출 경로를 타므로 recoverCaller → SubmitterAddress
// 경로가 그대로 돈다.
func submitFor(t *testing.T, srvURL string, headers map[string]string) string {
	t.Helper()
	body, _ := json.Marshal(submitRequest{Service: "sd-api", Params: json.RawMessage(`{"prompt":"x"}`)})
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/svc/sd-api/v1/jobs", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d", resp.StatusCode)
	}
	var sr submitResponse
	json.NewDecoder(resp.Body).Decode(&sr)
	return sr.JobID
}

// statusCodeFor 는 주어진 헤더로 job 상태를 조회하고 HTTP 상태코드를 돌려준다.
// authorizeJob 이 GET /v1/jobs/{id} 진입에서 소유권을 검사하므로 200/403 으로
// 소유권 판정을 관찰할 수 있다 (잡 완료를 기다릴 필요 없음).
func statusCodeFor(t *testing.T, srvURL, jobID string, headers map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srvURL+"/v1/jobs/"+jobID, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestAuthorizeJob_Ownership 는 mock 엔진 + 진짜 JobsHandler 로 httptest 서버를
// 띄워, 제출→저장→조회 전체 흐름에서 소유권이 맞물리는지 통합 검증한다.
// 단위 함수가 아니라 실제 HTTP 핸들러를 관통한다.
func TestAuthorizeJob_Ownership(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("done"))
	}))
	defer engine.Close()
	_, srv := newTestHandler(t, engine)

	ownerPK, _ := newWallet(t)
	otherPK, _ := newWallet(t)

	// 익명 제출(서명 없음) → SubmitterAddress="" → 누구나 읽을 수 있는 공개 잡.
	t.Run("anonymous job is public", func(t *testing.T) {
		id := submitFor(t, srv.URL, nil)
		if code := statusCodeFor(t, srv.URL, id, nil); code != http.StatusOK {
			t.Errorf("anon fetch = %d, want 200", code)
		}
		// 서명한 타인조차 익명 잡은 읽을 수 있다.
		if code := statusCodeFor(t, srv.URL, id, signedHeaders(t, otherPK, "m")); code != http.StatusOK {
			t.Errorf("stranger fetch of anon job = %d, want 200", code)
		}
	})

	// 서명 제출 → 그 지갑만 조회 가능.
	t.Run("signed job is owner-only", func(t *testing.T) {
		id := submitFor(t, srv.URL, signedHeaders(t, ownerPK, "submit:1"))
		// 같은 지갑이면 메시지가 달라도 같은 주소로 복구 → 200.
		if code := statusCodeFor(t, srv.URL, id, signedHeaders(t, ownerPK, "fetch:2")); code != http.StatusOK {
			t.Errorf("owner fetch = %d, want 200", code)
		}
		// 다른 지갑 → 403.
		if code := statusCodeFor(t, srv.URL, id, signedHeaders(t, otherPK, "fetch:3")); code != http.StatusForbidden {
			t.Errorf("other-wallet fetch = %d, want 403", code)
		}
		// 익명(서명 없음)으로 소유된 잡 조회 → 403.
		if code := statusCodeFor(t, srv.URL, id, nil); code != http.StatusForbidden {
			t.Errorf("anon fetch of owned job = %d, want 403", code)
		}
	})

	// 0-c 핵심: X-Caller-Address 만으로 제출하면 신원이 안 잡혀(recoverCaller="")
	// 잡이 익명=공개가 된다 → 헤더는 더 이상 소유권을 주지 못한다.
	t.Run("X-Caller-Address on submit grants no ownership", func(t *testing.T) {
		_, addr := newWallet(t)
		id := submitFor(t, srv.URL, map[string]string{"X-Caller-Address": addr})
		// 같은 주소 헤더로 조회해도 — 애초에 소유자로 기록되지 않았으니 — 공개(200).
		if code := statusCodeFor(t, srv.URL, id, map[string]string{"X-Caller-Address": addr}); code != http.StatusOK {
			t.Errorf("fetch = %d, want 200 (job is anonymous; header confers nothing)", code)
		}
		// 완전 익명 조회도 당연히 200 (공개 잡).
		if code := statusCodeFor(t, srv.URL, id, nil); code != http.StatusOK {
			t.Errorf("anon fetch = %d, want 200", code)
		}
	})
}

// submitStatusFor 는 submitFor 처럼 제출하되, 거절도 관찰하도록 상태코드와 본문을
// 돌려준다. wait 이면 동기 제출(wait=true)이다.
func submitStatusFor(t *testing.T, srvURL string, headers map[string]string, wait bool) (int, string) {
	t.Helper()
	body, _ := json.Marshal(submitRequest{Service: "sd-api", Params: json.RawMessage(`{"prompt":"another"}`), Wait: wait})
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/svc/sd-api/v1/jobs", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestSubmitWithTakenJobIDRefused: 남의 job id 를 X-ISANN-Request-Id 로 실어
// 제출하면 그 job 에 붙지 않고 409 로 거절된다. 예전에는 그 job 을 돌려주고
// 소유자를 제출자로 덮어써서, id 만 알면 서명된 job 의 답을 가져가고 주인을
// 내쫓을 수 있었다. 거절된 뒤에도 주인은 그대로다.
func TestSubmitWithTakenJobIDRefused(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("done"))
	}))
	defer engine.Close()
	_, srv := newTestHandler(t, engine)

	ownerPK, _ := newWallet(t)
	otherPK, _ := newWallet(t)
	id := submitFor(t, srv.URL, signedHeaders(t, ownerPK, "submit:1"))
	taken := map[string]string{"X-ISANN-Request-Id": id}

	for _, tc := range []struct {
		name    string
		headers map[string]string
		wait    bool
	}{
		{"anonymous", taken, false},
		{"anonymous, wait", taken, true},
		{"another wallet", mergeHeaders(signedHeaders(t, otherPK, "submit:2"), taken), false},
		{"the owner itself", mergeHeaders(signedHeaders(t, ownerPK, "submit:3"), taken), false},
	} {
		code, body := submitStatusFor(t, srv.URL, tc.headers, tc.wait)
		if code != http.StatusConflict || !strings.Contains(body, "request_id_in_use") {
			t.Errorf("%s: submit with a taken id = %d %s, want 409 request_id_in_use", tc.name, code, body)
		}
		if strings.Contains(body, "done") {
			t.Errorf("%s: the refusal carries the job's answer: %s", tc.name, body)
		}
	}

	// The job still belongs to its owner.
	if code := statusCodeFor(t, srv.URL, id, signedHeaders(t, ownerPK, "fetch:1")); code != http.StatusOK {
		t.Errorf("owner fetch = %d, want 200", code)
	}
	if code := statusCodeFor(t, srv.URL, id, signedHeaders(t, otherPK, "fetch:2")); code != http.StatusForbidden {
		t.Errorf("other-wallet fetch = %d, want 403", code)
	}
	if code := statusCodeFor(t, srv.URL, id, nil); code != http.StatusForbidden {
		t.Errorf("anonymous fetch = %d, want 403", code)
	}
}
