package station

// m0_inference_auth_test.go — M0(추론 인증 통일) 검증.
//
// M0 는 잡 소유권을 "broker 가 넘겨주던 X-Caller-Address 헤더" 에서
// "받는 door 가 IANN 서명을 직접 recover" 로 옮긴다 (docs/TODO/isann-cli-phase3.md).
// 0-c 까지 적용된 최종 상태 — X-Caller-Address 는 어디서도 신뢰하지 않는다.
//
// 이 파일이 덮는 4(+1)가지:
//   - recoverCaller       : 서명에서만 신원 복구 (헤더 완전 무시 = 사칭 불가)
//   - providerRole        : owner/admin/user/issuer 4-role 분류
//   - verifyInferenceAuth : 보호모드 추론 게이트 (서명 필수, 4-role)
//   - isInferencePath     : 어떤 경로가 4-role 게이트를 타는가
//   - authorizeJob        : 실제 핸들러를 관통하는 per-job 소유권 (end-to-end)

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/auth"
	"github.com/isannai/mesh/pkg/tunnel"
	"github.com/ethereum/go-ethereum/crypto"
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
			r := httptest.NewRequest(http.MethodPost, "/v1/jobs", nil)
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
// providerRole — 4-role 분류 (walletIsOperator-class 핫스팟)
// ----------------------------------------------------------------------------

// TestProviderRole 은 auth.json 에 owner/admin/user/issuer 가 등록된 Provider 를
// 만들고, providerRole 이 각 주소를 올바른 role 로 분류하는지 + 미등록은 ""
// + 대소문자 무시까지 확인한다. CLAUDE.md 가 경고한 4-role 허용 버그 영역이라
// 의도적으로 못 박는다.
func TestProviderRole(t *testing.T) {
	_, owner := newWallet(t)
	_, admin := newWallet(t)
	_, user := newWallet(t)
	_, issuer := newWallet(t)
	_, stranger := newWallet(t)

	p := &Provider{Base: &tunnel.Base{Auth: tunnel.AuthConfig{
		Mode:   "protected",
		Owner:  owner,
		Admins: []string{admin},
		Users:  []string{user},
		Issuer: issuer,
	}}}

	cases := []struct {
		addr string
		want string
	}{
		{owner, "owner"},
		{admin, "admin"},
		{user, "user"},
		{issuer, "issuer"},
		{stranger, ""},                    // 어느 목록에도 없으면 role 없음
		{strings.ToLower(owner), "owner"}, // 주소 비교는 대소문자 무시
	}
	for _, c := range cases {
		if got := p.providerRole(c.addr); got != c.want {
			t.Errorf("providerRole(%s) = %q, want %q", c.addr, got, c.want)
		}
	}
}

// ----------------------------------------------------------------------------
// verifyInferenceAuth — 보호모드 추론 게이트 (서명 필수)
// ----------------------------------------------------------------------------

// TestVerifyInferenceAuth 는 4-role 추론 게이트를 검증한다. bufStream(메모리
// quic.Stream 어댑터)을 써서 네트워크 없이 게이트를 직접 호출한다.
// 0-c: 통과하려면 유효한 4-role *서명* 이 필요하다 — X-Caller-Address 헤더로는
// 통과할 수 없다.
func TestVerifyInferenceAuth(t *testing.T) {
	userPK, user := newWallet(t)
	strangerPK, _ := newWallet(t)

	// user 한 명만 등록된 보호모드 노드.
	p := &Provider{Base: &tunnel.Base{Auth: tunnel.AuthConfig{Mode: "protected", Users: []string{user}}}}

	// 서명 메시지의 만료 필드({...}:{...}:{...}:{nonce}:{expiresAt}:{nodes})로
	// 만료 검사까지 탄다. 미래/과거 unix 타임스탬프를 박는다.
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	past := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)

	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		// 등록된 user 의 유효 서명 → 통과 (4-role 이라 owner/admin 아니어도 OK).
		{
			"valid user signature passes (4-role)",
			signedHeaders(t, userPK, "user:provider:sd:n1:"+future+":"),
			true,
		},
		// auth.json 어디에도 없는 지갑 서명 → 403.
		{
			"signed by unknown wallet rejected",
			signedHeaders(t, strangerPK, "user:provider:sd:n1:"+future+":"),
			false,
		},
		// 아무 인증 헤더도 없으면 → 401.
		{
			"no auth headers rejected",
			nil,
			false,
		},
		// 만료된 서명 → 401.
		{
			"expired signature rejected",
			signedHeaders(t, userPK, "user:provider:sd:n1:"+past+":"),
			false,
		},
		// 0-c 핵심: X-Caller-Address 만 있고 서명이 없으면 더 이상 통과 못 한다
		// (예전 broker-vouched 분기 제거). 헤더로 게이트를 못 연다.
		{
			"X-Caller-Address alone no longer passes the gate",
			map[string]string{"X-Caller-Address": "0xabc"},
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/provider/v1/jobs", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			bs := &bufStream{Buffer: new(bytes.Buffer)}
			if got := p.verifyInferenceAuth(bs, r); got != tc.want {
				t.Errorf("verifyInferenceAuth = %v, want %v (resp: %s)", got, tc.want, bs.String())
			}
		})
	}
}

// TestIsInferencePath 는 어떤 orchestrator 경로가 4-role 추론 게이트
// (verifyInferenceAuth)를 타고, 어떤 경로가 owner/admin 운영 게이트로 남는지를
// 경계 검증한다. 운영 경로가 실수로 추론 게이트로 새지 않는지 확인.
func TestIsInferencePath(t *testing.T) {
	// 추론(4-role 게이트) — 잡 제출/조회/결과, 큐 통계, 산출물 다운로드.
	yes := []struct{ m, p string }{
		{http.MethodPost, "/provider/v1/jobs"},
		{http.MethodGet, "/provider/v1/jobs/abc123"},
		{http.MethodGet, "/provider/v1/jobs/abc123/result"},
		{http.MethodGet, "/provider/outputs/sd_abc.png"},
		{http.MethodGet, "/provider/v1/queue/stats"},
	}
	for _, c := range yes {
		if !isInferencePath(c.p, c.m) {
			t.Errorf("isInferencePath(%s %s) = false, want true", c.m, c.p)
		}
	}
	// 추론 아님(owner/admin 운영 게이트 유지) — 설정/프로필/서비스 기동 +
	// 잡 제출도 결과조회도 아닌 맨 GET.
	no := []struct{ m, p string }{
		{http.MethodPost, "/provider/config"},
		{http.MethodGet, "/provider/profiles"},
		{http.MethodPost, "/service/sd/start"},
		{http.MethodGet, "/provider/v1/jobs"}, // 맨 GET 은 제출도 id조회도 아님
	}
	for _, c := range no {
		if isInferencePath(c.p, c.m) {
			t.Errorf("isInferencePath(%s %s) = true, want false", c.m, c.p)
		}
	}
}

// ----------------------------------------------------------------------------
// authorizeJob — 실제 핸들러를 관통하는 소유권 (end-to-end)
// ----------------------------------------------------------------------------

// submitFor 는 주어진 헤더로 job 을 제출하고 job id 를 돌려준다. 실제
// JobsHandler 의 POST /v1/jobs 를 타므로 recoverCaller → SubmitterAddress
// 경로가 그대로 돈다.
func submitFor(t *testing.T, srvURL string, headers map[string]string) string {
	t.Helper()
	body, _ := json.Marshal(submitRequest{Service: "sd-api", Params: json.RawMessage(`{"prompt":"x"}`)})
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/v1/jobs", strings.NewReader(string(body)))
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
