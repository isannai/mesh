package setup

// tpm_challenge.go — proving a TPM is real by ANSWERING, not by showing papers.
//
// The old proof was "send me your EK certificate". Two things break it:
//
//	위조   인증서는 파일이다. 베끼면 그만이고, 받는 쪽은 x509 파싱만 한다
//	누락   EK 인증서가 아예 없는 정상 TPM 이 실재한다 — 보드 제조사가 넣어주지
//	       않으면 NV 인덱스 자체가 없다. 운영자 노드(i5-10500 / Intel PTT)가 그렇다
//
// 합치면 가짜는 통과하고 진짜는 떨어진다. 그래서 서류를 보는 대신 칩에게 직접 묻는다.
//
//	검증자   난수를 보낸다
//	노드     TPM 안의 키로 서명한다        ← 키는 칩 밖으로 안 나온다
//	검증자   등록해 둔 공개키로 검증한다
//
// 인증서가 등장하지 않으므로 인증서 없는 노드도 통과한다. 그리고 베낄 파일이 없다 —
// 하드웨어 값(메인보드 UUID·EK 지문)을 읽어 node id 를 복제한 쪽은 이 서명을 만들지
// 못한다. 개인키가 primary seed 에서 나오고 그 seed 는 칩 안에만 있기 때문이다.
// 그것이 이 교환이 실제로 막는 위협이다.
//
// 🔴 증명하지 '않는' 것: 이 키가 제조사가 만든 진짜 칩의 것이라는 보증. 그건 EK
// 인증서만 할 수 있고, 없는 장비가 있어서 자격 조건으로 못 쓴다. 소프트웨어 TPM
// 에뮬레이터는 이 교환을 통과한다. 한 대가 여러 대인 척하는 것은 검증자가 '공개키
// 하나에 노드 하나' 를 강제해서 막는다 — 서명이 아니라 그 유일성 규칙이 sybil 을
// 막는 부분이다.
//
// # 왜 표준 credential activation 이 아닌가
//
// 교과서적인 방법은 TPM2_MakeCredential / TPM2_ActivateCredential 이고, EK 에 직접
// 묶이므로 이것보다 강하다. 구현해서 운영자 노드(Windows 11 / Intel PTT)에 돌려봤고
// 앞 단계는 전부 통과했다 — EK 로드 · AK 생성 · StartAuthSession · PolicySecret(EH).
// 마지막 ActivateCredential 만 `0x80280400`(TPM_E_COMMAND_BLOCKED)으로 막힌다.
// Windows 가 표준 사용자에게 그 명령을 차단하고, isannd 는 S4U 사용자 태스크로 도니
// 해당된다. 관리자로 올리면 열릴 수 있으나 그러자고 데몬을 승격시킬 수는 없다.
//
// 같은 이유로 AK 를 영구 핸들에 박는 `client.AttestationKeyRSA` 도 못 쓴다 —
// EvictControl 이 owner auth 를 요구하고 Windows 가 그 소유권을 쥐고 있다.
// 그래서 매번 TRANSIENT 로 만든다. 안전한 이유는 CreatePrimary 가 결정적이기
// 때문이다: 같은 계층·같은 템플릿이면 primary seed 에서 같은 키가 나온다. 프로세스를
// 나눠 두 번 호출해도 공개키가 바이트까지 같은 것을 실측했다.
//
// 🔴 템플릿에 FlagRestricted 를 넣지 않는다. restricted signing key 는 TPM 이 만든
// 다이제스트(검증 티켓 동반)만 서명하므로, 검증자가 준 난수를 서명하려 하면
// `error code 0x20 : invalid ticket` 으로 떨어진다. 이것도 실측으로 걸렀다.

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"fmt"

	"github.com/google/go-tpm-tools/client"
	"github.com/google/go-tpm/legacy/tpm2"
)

// tpmChallengeTemplate is the key this node signs challenges with.
//
// Deterministic: derived from the owner primary seed, so it is the same key on
// every call and across restarts. It changes only if the TPM is cleared, which
// is indistinguishable from a new machine and should re-register as one.
//
// No FlagRestricted — see the file comment.
func tpmChallengeTemplate() tpm2.Public {
	return tpm2.Public{
		Type:    tpm2.AlgRSA,
		NameAlg: tpm2.AlgSHA256,
		Attributes: tpm2.FlagFixedTPM | tpm2.FlagFixedParent |
			tpm2.FlagSensitiveDataOrigin | tpm2.FlagUserWithAuth | tpm2.FlagSign,
		RSAParameters: &tpm2.RSAParams{
			Sign:    &tpm2.SigScheme{Alg: tpm2.AlgRSASSA, Hash: tpm2.AlgSHA256},
			KeyBits: 2048,
		},
	}
}

// TPMChallengePublicKey returns the public half of this node's challenge key,
// PKIX DER.
//
// A public value: publishing it lets anyone verify an answer, and nobody but
// this chip can produce one. The verifier records it the first time and holds
// the node to it afterwards.
//
// Fails when there is no TPM. Callers must treat that as "does not take part",
// never as fatal — a node without a TPM has to keep working in every other way.
func TPMChallengePublicKey() ([]byte, error) {
	rwc, err := openTPM()
	if err != nil {
		return nil, fmt.Errorf("tpm: open: %w", err)
	}
	defer rwc.Close()

	key, err := client.NewKey(rwc, tpm2.HandleOwner, tpmChallengeTemplate())
	if err != nil {
		return nil, fmt.Errorf("tpm: challenge key: %w", err)
	}
	defer key.Close()

	der, err := x509.MarshalPKIXPublicKey(key.PublicKey())
	if err != nil {
		return nil, fmt.Errorf("tpm: marshal public key: %w", err)
	}
	return der, nil
}

// SignWithTPMKey signs a message inside the TPM.
//
// Used for two things that are the same operation: answering the rendezvous
// challenge (the message is a nonce) and signing a faucet ticket (the message
// is the ticket string). Keeping one function means the ticket cannot drift
// onto a different hash or padding than the challenge the key was verified
// with.
//
// The message is hashed HERE rather than by the caller so both ends agree
// without a second convention to keep in step: the wire carries the message,
// the signature covers SHA-256 of it.
func SignWithTPMKey(msg []byte) ([]byte, error) {
	if len(msg) == 0 {
		return nil, fmt.Errorf("tpm: nothing to sign")
	}

	rwc, err := openTPM()
	if err != nil {
		return nil, fmt.Errorf("tpm: open: %w", err)
	}
	defer rwc.Close()

	key, err := client.NewKey(rwc, tpm2.HandleOwner, tpmChallengeTemplate())
	if err != nil {
		return nil, fmt.Errorf("tpm: challenge key: %w", err)
	}
	defer key.Close()

	digest := sha256.Sum256(msg)
	sig, err := tpm2.Sign(rwc, key.Handle(), "", digest[:], nil,
		&tpm2.SigScheme{Alg: tpm2.AlgRSASSA, Hash: tpm2.AlgSHA256})
	if err != nil {
		return nil, fmt.Errorf("tpm: sign: %w", err)
	}
	if sig.RSA == nil {
		return nil, fmt.Errorf("tpm: signature carries no RSA part")
	}
	return sig.RSA.Signature, nil
}

// VerifyTPMChallenge checks an answer against the public key the verifier holds.
//
// Lives here, beside the signer, so the two cannot drift on hash or padding.
// The verifier is a different program in a different repository and must not
// re-derive this.
func VerifyTPMChallenge(pubDER, nonce, sig []byte) error {
	if len(pubDER) == 0 || len(nonce) == 0 || len(sig) == 0 {
		return fmt.Errorf("tpm: incomplete challenge answer")
	}
	pub, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		return fmt.Errorf("tpm: parse public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("tpm: public key is %T, want RSA", pub)
	}
	digest := sha256.Sum256(nonce)
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest[:], sig); err != nil {
		return fmt.Errorf("tpm: signature does not verify: %w", err)
	}
	return nil
}
