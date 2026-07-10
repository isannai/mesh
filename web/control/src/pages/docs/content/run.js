const ko = {
  manual: {
    p: "설치 디렉토리로 이동 후 Provider 실행:",
    note: "터미널이 닫히면 Provider 도 종료됩니다. 상시 운영하려면 서비스 등록 권장.",
  },
  win: {
    m1_h: "방법 1: Task Scheduler (간단)",
    m1_p: "작업 스케줄러 → 기본 작업 만들기 → \"시스템 시작 시\" 트리거 → ",
    m1_code: "C:\\IANN\\bin\\provider.exe",
    m1_p2: " 등록.",
    m2_h: "방법 2: Windows Service (권장)",
    note_a: "NSSM (",
    note_link: "nssm.cc",
    note_b: ") 을 사용하면 표준 .exe 를 service 로 쉽게 래핑할 수 있습니다.",
  },
  linux: {
    p: "/etc/systemd/system/iann-provider.service 생성:",
  },
  macos: {
    p: "launchd plist 예시 (~/Library/LaunchAgents/io.iann.provider.plist):",
  },
  logs: {
    p: "기본 로그 위치:",
    items: [
      ["Windows: ", "C:\\IANN\\logs\\provider.log"],
      ["Linux: ", "journalctl -u iann-provider -f", " (systemd) 또는 ", "/opt/iann/logs/"],
      ["macOS: ", "~/IANN/logs/"],
    ],
  },
  update: {
    p: "Gate 에 새 버전이 올라왔을 때 업데이트. Provider 를 잠시 중지 후 실행 권장.",
  },
  next: {
    items: [
      ["TPM 활성화", " — Verified Device 등급으로 Faucet 2배"],
      ["My Nodes 페이지에서 노드 상태 / 로그 확인", ""],
    ],
  },
};

const en = {
  manual: {
    p: "Go to the install directory, then run Provider:",
    note: "Provider exits when the terminal closes. For always-on operation, register a service.",
  },
  win: {
    m1_h: "Option 1: Task Scheduler (simple)",
    m1_p: "Task Scheduler → Create Basic Task → trigger \"When the computer starts\" → point at ",
    m1_code: "C:\\IANN\\bin\\provider.exe",
    m1_p2: ".",
    m2_h: "Option 2: Windows Service (recommended)",
    note_a: "NSSM (",
    note_link: "nssm.cc",
    note_b: ") wraps a plain .exe as a service easily.",
  },
  linux: {
    p: "Create /etc/systemd/system/iann-provider.service:",
  },
  macos: {
    p: "Example launchd plist (~/Library/LaunchAgents/io.iann.provider.plist):",
  },
  logs: {
    p: "Default log locations:",
    items: [
      ["Windows: ", "C:\\IANN\\logs\\provider.log"],
      ["Linux: ", "journalctl -u iann-provider -f", " (systemd) or ", "/opt/iann/logs/"],
      ["macOS: ", "~/IANN/logs/"],
    ],
  },
  update: {
    p: "When a new version is available in Gate. Stop Provider, run update, restart.",
  },
  next: {
    items: [
      ["TPM Activation", " — Verified Device tier unlocks 2× Faucet"],
      ["Check node status / logs in the My Nodes page", ""],
    ],
  },
};

export default { ko, en };
