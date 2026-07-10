const ko = {
  callout_a: "",
  callout_bold: "Verified Device 혜택",
  callout_b: ": Faucet 1회 충전량 100 → 200, Free Credit 누적 가능, Verified 뱃지.",
  what: {
    p: "TPM (Trusted Platform Module) 은 하드웨어 기반 신뢰 루트 칩입니다. IANN 은 CPU 에 내장된 ",
    bold: "펌웨어 TPM (fTPM)",
    p2: " 만 Verified Device 기준으로 인정합니다 — 바이오스 설정만으로 활성화 가능.",
    items: [
      ["Intel: ", "PTT", " (Platform Trust Technology, 6세대 Skylake 이상)"],
      ["AMD: ", "fTPM", " (AMD CPU fTPM, Ryzen 1000 이상 / Athlon 2000 이상)"],
      ["외장 dTPM 모듈은 Phase 1 대상이 아님 (Phase 2 에서 데이터센터용으로 검토)", "", ""],
    ],
  },
  check_windows: "Windows",
  check_linux: "Linux",
  bios: {
    intro_a: "재부팅 → BIOS 진입 키 연타 (일반적으로 ",
    keys: "Del, F2, F10, Esc",
    intro_b: " 중 하나). Phase 1 은 CPU 내장 fTPM 만 인정하므로 아래 둘 중 CPU 에 맞는 쪽만 활성화하면 됩니다.",
  },
  cpu: {
    intel: {
      label: "Intel — PTT (Platform Trust Technology)",
      desc: "Skylake (6세대) 이상 Core 프로세서에서 지원. 메뉴 이름은 메인보드 제조사마다 조금씩 다르지만, 경로 패턴은 비슷합니다.",
      paths: [
        "Advanced → PCH-FW Configuration → PTT → Enable",
        "Advanced → Trusted Computing → Security Device Support → Enable + TPM Device Selection → PTT",
        "Peripherals → Intel Platform Trust Technology (PTT) → Enable",
      ],
      note: "노트북 (HP / Dell / Lenovo / 삼성 등) 은 보통 Security → TPM Device (또는 Security Chip) → Enable 위치에 있습니다.",
    },
    amd: {
      label: "AMD — fTPM (AMD CPU fTPM)",
      desc: "Ryzen 1000 / Athlon 2000 이상 프로세서에서 지원. \"AMD CPU fTPM\" 으로 표기된 옵션을 Enable 하면 됩니다.",
      paths: [
        "Advanced → AMD fTPM Configuration → TPM Device Selection → Firmware TPM",
        "Settings → Miscellaneous → AMD CPU fTPM → Enable",
        "Advanced → CPU Configuration → AMD fTPM switch → AMD CPU fTPM",
      ],
      note: "fTPM 활성 후 일부 AMD 메인보드에서 stutter 리포트 있음 → BIOS 최신 버전 업데이트 권장.",
    },
  },
  verify: {
    p1_a: "BIOS 저장 후 부팅. Windows 에서 ",
    p1_code: "tpm.msc",
    p1_b: " 가 \"준비 완료\" 로 바뀌면 성공.",
    p2: "IANN 측 검증은 Provider 시작 시 fTPM EK Certificate 를 읽고, 랑데뷰 서버에 등록할 때 전송합니다. 랑데뷰는 EK 인증서 구조를 검증(Intel CSME/PTT 또는 AMD fTPM 발급 여부)하고, nonce challenge 로 TPM 소유를 증명합니다. 검증 완료 시 노드에 ",
    p2_code: "tpm_verified=true",
    p2_b: " 플래그가 붙고 인증 뱃지가 표시됩니다.",
  },
  flow: {
    title: "인증 절차",
    steps: [
      "1. Provider 시작 → fTPM에서 EK(Endorsement Key) 인증서 읽기",
      "2. 랑데뷰 서버에 등록 시 EK 인증서 포함하여 전송",
      "3. 랑데뷰: EK 인증서 구조 검증 (Issuer가 Intel CSME/PTT 또는 AMD fTPM인지)",
      "4. 랑데뷰: 랜덤 nonce 생성 → Provider에게 전송",
      "5. Provider: TPM으로 nonce 서명 (TPM 내부의 키로만 가능)",
      "6. 랑데뷰: 서명 검증 → 통과 시 tpm_verified = true",
      "7. 노드 목록에 인증 뱃지 표시 + TPM Issuer 정보 표시",
    ],
    note: "랑데뷰 서버 재시작 시 자동으로 재인증됩니다 (~30초 소요).",
  },
  faq: [
    {
      q: "BIOS 에 해당 메뉴가 없어요",
      a: "Intel Skylake (6세대) 이전 또는 AMD Ryzen 1000 이전 CPU 는 fTPM 미지원. IANN Phase 1 은 fTPM 만 인정하므로 해당 PC 는 Verified Device 대상에서 제외됩니다 (기본 Faucet 100 은 그대로 사용 가능). CPU 모델 검색으로 지원 여부 확인.",
    },
    {
      q: "왜 외장 dTPM 은 안 되나요?",
      a: "Phase 1 은 root CA 관리 범위를 좁히기 위해 Intel / AMD 두 곳만 검증합니다. dTPM 은 Infineon, ST, Nuvoton 등 제조사별 체인이 분산되어 있어 중고·복제품 유통 위험도 있습니다. Phase 2 에서 데이터센터 운영자 대상으로 확장 예정.",
    },
    {
      q: "fTPM 활성 후 부팅 느려짐 / 스터터 이슈",
      a: "일부 AMD 메인보드에서 fTPM 활성 후 stutter 발생 기록 있음. BIOS 최신 버전으로 업데이트 권장. Windows 11 도 동일 이슈 리포트 있었고 MS 가 패치로 대응.",
    },
    {
      q: "TPM 없어도 Provider 운영 가능?",
      a: "가능합니다. 단 Faucet 1회 충전량이 100 에서 제한되며 (누적 불가), 마켓 노출 우선순위에서 밀립니다. fTPM 활성화만 하면 보너스 받음.",
    },
  ],
};

const en = {
  callout_a: "",
  callout_bold: "Verified Device benefits",
  callout_b: ": Faucet top-up 100 → 200, Free Credit accumulation, Verified badge.",
  what: {
    p: "TPM (Trusted Platform Module) is a hardware root of trust. IANN counts only ",
    bold: "firmware TPM (fTPM)",
    p2: " (built into the CPU) for Verified Device status — just a BIOS setting.",
    items: [
      ["Intel: ", "PTT", " (Platform Trust Technology, Skylake 6th-gen or newer)"],
      ["AMD: ", "fTPM", " (AMD CPU fTPM, Ryzen 1000 or newer / Athlon 2000 or newer)"],
      ["External dTPM modules are out of scope in Phase 1 (Phase 2 will evaluate for datacenters)", "", ""],
    ],
  },
  check_windows: "Windows",
  check_linux: "Linux",
  bios: {
    intro_a: "Reboot → spam the BIOS entry key (usually one of ",
    keys: "Del, F2, F10, Esc",
    intro_b: "). Phase 1 only recognises CPU-based fTPM, so enable whichever of the two matches your CPU.",
  },
  cpu: {
    intel: {
      label: "Intel — PTT (Platform Trust Technology)",
      desc: "Supported on Skylake (6th-gen) or newer Core CPUs. Menu wording varies slightly by motherboard vendor, but the path pattern is consistent.",
      paths: [
        "Advanced → PCH-FW Configuration → PTT → Enable",
        "Advanced → Trusted Computing → Security Device Support → Enable + TPM Device Selection → PTT",
        "Peripherals → Intel Platform Trust Technology (PTT) → Enable",
      ],
      note: "On laptops (HP / Dell / Lenovo / Samsung …), the option usually lives under Security → TPM Device (or Security Chip) → Enable.",
    },
    amd: {
      label: "AMD — fTPM (AMD CPU fTPM)",
      desc: "Supported on Ryzen 1000 / Athlon 2000 or newer. Enable the option labelled \"AMD CPU fTPM\".",
      paths: [
        "Advanced → AMD fTPM Configuration → TPM Device Selection → Firmware TPM",
        "Settings → Miscellaneous → AMD CPU fTPM → Enable",
        "Advanced → CPU Configuration → AMD fTPM switch → AMD CPU fTPM",
      ],
      note: "Some AMD boards report stutter after fTPM is enabled — update to the latest BIOS.",
    },
  },
  verify: {
    p1_a: "Save and reboot. On Windows, ",
    p1_code: "tpm.msc",
    p1_b: " should report \"Ready for use\".",
    p2: "On the IANN side, Provider reads the fTPM EK Certificate at startup and sends it to the Rendezvous server during registration. Rendezvous verifies the EK cert structure (Intel CSME/PTT or AMD fTPM issuer) and issues a nonce challenge to prove TPM ownership. On success, the node is flagged ",
    p2_code: "tpm_verified=true",
    p2_b: " and a certification badge is displayed.",
  },
  flow: {
    title: "Verification Flow",
    steps: [
      "1. Provider starts → reads EK (Endorsement Key) certificate from fTPM",
      "2. Sends EK certificate to Rendezvous server during registration",
      "3. Rendezvous: validates EK cert structure (Issuer must be Intel CSME/PTT or AMD fTPM)",
      "4. Rendezvous: generates random nonce → sends to Provider",
      "5. Provider: signs nonce with TPM-resident key (only possible with real TPM)",
      "6. Rendezvous: verifies signature → sets tpm_verified = true",
      "7. Certification badge + TPM Issuer info displayed on node listing",
    ],
    note: "Re-verification happens automatically after Rendezvous restart (~30 seconds).",
  },
  faq: [
    {
      q: "My BIOS does not have this menu",
      a: "CPUs older than Intel Skylake (6th-gen) or AMD Ryzen 1000 don't support fTPM. Phase 1 only counts fTPM, so these PCs cannot qualify as Verified Device (basic Faucet 100 still works). Search your CPU model to confirm fTPM support.",
    },
    {
      q: "Why are external dTPM modules not supported?",
      a: "Phase 1 narrows the root CA scope to Intel and AMD only. dTPM chains are spread across multiple vendors (Infineon, ST, Nuvoton) with a higher risk of counterfeit / second-hand units. Phase 2 will evaluate dTPM for datacenter operators.",
    },
    {
      q: "Stutter issues after fTPM enable",
      a: "Some AMD boards reported stutter after fTPM enable. Update to the latest BIOS. Windows 11 had the same report; MS patched it.",
    },
    {
      q: "Can I run Provider without TPM?",
      a: "Yes. But Faucet top-up is capped at 100 (no accumulation) and market ranking is lower. Enabling fTPM unlocks the bonuses.",
    },
  ],
};

export default { ko, en };
