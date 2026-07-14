# Dead code tracking

`deadcode`(golang.org/x/tools) 로 mesh 모듈 전체(3 mains: station/control/chatbot + 테스트)에서
도달 불가능한 함수를 추적한다. 이 문서는 **남은 항목의 처분 상태**를 사람이 판단해 기록하는 곳이다.

## 재생성 방법

```bash
go install golang.org/x/tools/cmd/deadcode@latest
deadcode -test ./... | grep -v node_modules
```

> `node_modules` 하위(`web/control/.../flatted.go` 등)는 서드파티 vendored 코드라 항상 제외한다.

## 주의 (오탐 요인)

1. **플랫폼 한정 분석** — deadcode 는 실행 host 기준(현재 **windows/amd64**)으로만 분석한다.
   `_linux` 전용 함수나 `runtime.GOOS=="linux"` 분기는 리눅스 빌드에선 살아있을 수 있어 **오탐**이다.
   (예: `pkg/setup/detect.go` 의 `detectMemoryLinux` / `detectDiskLinux` / `detectCPUClockLinux`)
2. **미배선 WIP 패키지** — untracked(`??`) 상태로 새로 추가된 패키지는 아직 어느 main 에도 안 붙어서
   전부 "dead" 로 잡힌다. 이는 진짜 죽은 코드가 아니라 **배선 전 스캐폴딩**이다. 배선 시점에 재평가한다.
3. **의도된 스텁** — 실제 구현 배선 전까지 자리만 잡아둔 코드는 유지한다.

## 처분 범례

| 상태 | 의미 |
|---|---|
| 🔴 dead | 실제 죽은 코드. 삭제 대상 |
| ⏸ WIP | 미배선 신규 패키지. 배선 시 재평가, 지금은 유지 |
| 🟡 stub | 의도된 스텁. 유지 |
| ⚪ 오탐 | 플랫폼 등으로 인한 false positive |

---

## 해결 로그 (Resolved)

| 날짜 | 범위 | 내용 |
|---|---|---|
| 2026-07-14 | `pkg/station` | 죽은 함수 13개 제거 (session helpers, dockerPS/Probe, sync cleanup, HandleAuthVerifyHTTP, removeFileQuiet, queue effectiveStatus/UpdateJobProgress) |
| 2026-07-14 | `pkg/control` | 죽은 함수 15개 제거. `pkg/control/balancer` 패키지 통째 삭제(미사용) |
| 2026-07-14 | `pkg/pipeline` (GLink) | 죽은 함수 6개 제거 (Job.Done, JobStore.Get/Wait, Registry.Types, Runner.SetNodeCaller, pipelineError/ErrJobNotFound) |
| 2026-07-14 | `pkg/tunnel` | 통째 죽은 파일 2개 삭제: `mux.go`(MuxPacketConn 전체) · `webdav.go`(NewWebDAVHandler) — 5개 함수 |
| 2026-07-14 | `pkg/auth` | `VerifySignature`(RecoverAddress와 중복, 미사용) 제거 · `routes.go` 파일째 삭제(`ClassifyRoute` + `LevelNone/User/Admin` 상수, 미배선) |

---

## 남은 항목 (67개, 2026-07-14 기준)

### `pkg/setup` — 50개 · ⏸ WIP (설치/셋업 서브시스템, 미배선)

신규 untracked 패키지. 어느 main 에서도 호출되지 않음 = 아직 배선 전. 배선 시 재평가.

| 파일:라인 | 함수 | 비고 |
|---|---|---|
| detect.go:69 | `DetectSystem` | |
| detect.go:158 | `detectMemoryLinux` | ⚪ linux 전용 — 리눅스 빌드에선 오탐 가능 |
| detect.go:179 | `detectDiskLinux` | ⚪ linux 전용 |
| detect.go:195 | `detectCPUClockLinux` | ⚪ linux 전용 |
| detect.go:260 | `HashHardware` | |
| detect.go:439 | `detectGPUsDynamic` | |
| detect.go:494 | `DetectHardwareDynamic` | |
| detect.go:504 | `StableHardware` | |
| detect.go:531 | `VolatileHardware` | |
| detect.go:557 | `MergeHardware` | |
| detect.go:579 | `LogSystemInfo` | |
| detect.go:598 | `FormatSystemSummary` | |
| detect_windows.go:63 | `FreeDiskBytes` | |
| detect_windows.go:79 | `hasCUDA12Runtime` | |
| detect_windows.go:85 | `fetchCPUProcessorID` | |
| detect_windows.go:149 | `detectRAMFreeGB` | |
| detect_windows.go:171 | `detectCPUsDynamic` | |
| filehash.go:40 | `FileHashShort` | |
| github.go:25 | `LatestRelease` | |
| github.go:59 | `Release.AssetNames` | |
| github.go:68 | `Release.FindAsset` | |
| prereq.go:69 | `DetectPrereqs` | |
| prereq.go:125 | `driverComponent` | |
| prereq_windows.go:47 | `hideWSLWindow` | |
| prereq_windows.go:57 | `wslOutput` | |
| prereq_windows.go:67 | `detectPrereqs` | |
| prereq_windows.go:190 | `wslFeaturePresent` | |
| prereq_windows.go:211 | `wslDistros` | |
| prereq_windows.go:247 | `isDockerDesktopDistro` | |
| prereq_windows.go:260 | `DockerDesktopRunning` | |
| prereq_windows.go:273 | `wslNativeDockerArgs` | |
| prereq_windows.go:287 | `dockerDaemonInWSL` | |
| prereq_windows.go:313 | `dockerDesktopInstalledExe` | |
| prereq_windows.go:327 | `toolkitInWSL` | |
| prereq_windows.go:341 | `stripBOM` | |
| project_llama.go:11 | `NewLlamaProject` | |
| project_llama.go:27 | `selectLlamaAsset` | |
| project_llama.go:70 | `matchesLlamaPlatform` | |
| project_sd.go:11 | `NewSDProject` | |
| project_sd.go:27 | `selectSDAsset` | |
| project_sd.go:78 | `matchesPlatform` | |
| project_sd.go:108 | `matchesVariant` | |
| setup.go:28 | `Ensure` | |
| setup.go:109 | `download` | |
| setup.go:143 | `progressDisplay.Write` | |
| setup.go:224 | `ExtractZipFiltered` | |
| setup.go:506 | `formatBytes` | |
| tpm.go:53 | `SignWithTPM` | |
| tpm_windows.go:35 | `decryptWithEK` | |
| tpm_windows.go:70 | `ReadAKPublicHash` | |

### `pkg/tunnel` — 9개 · 개별 미사용 (패키지 자체는 핵심, 유지)

tunnel 패키지는 station/control 16개 파일이 import 하는 핵심 패키지다. 아래는 **살아있는 타입의 개별 미사용 메서드/헬퍼**. 배선 의도 확인 후 개별 제거 가능.

| 파일:라인 | 함수 | 비고 |
|---|---|---|
| isannd_client.go:166 | `IsanndClient.Connect` | 타입은 사용 중(NewIsanndClient) — 메서드만 미사용 |
| isannd_client.go:367 | `IsanndClient.SendMetrics` | 〃 |
| isannd_client.go:423 | `IsanndClient.Close` | 〃 |
| isannd_client.go:442 | `IsanndClient.GetTPMFingerprint` | 〃 |
| session.go:71 | `Session.NeedsRenew` | Session 타입은 광범위 사용 — 메서드만 미사용 |
| session.go:191 | `SessionCache.LookupByNodeID` | SessionCache 사용 중(Put/LookupByToken) — 메서드만 미사용 |
| session.go:203 | `SessionCache.Prune` | 〃 |
| session.go:229 | `UUIDString` | 독립 헬퍼 |
| rendezvous.go:177 | `MustResolveUDP` | 독립 헬퍼 |

### 기타 WIP 신규 패키지 — 7개 · ⏸ WIP

| 파일:라인 | 함수 | 패키지 |
|---|---|---|
| pkg/engine/manifest/root.go:97 | `ManifestCandidates` | engine/manifest |
| pkg/engine/manifest/root.go:104 | `ResolveManifestPath` | engine/manifest |
| pkg/engine/manifest/template.go:12 | `Substitute` | engine/manifest (소문자 `substitute` 래퍼, 미배선) |
| pkg/installclient/process_windows.go:7 | `isProcessAlive` | installclient |
| pkg/installclient/process_windows.go:14 | `IsProcessAlive` | installclient |
| pkg/glog/events.go:54 | `NewEventWriter` | glog |
| pkg/profile/profile.go:141 | `SetPathFor` | profile |

### `pkg/chatbot/ai` — 1개 · 🟡 stub (유지)

| 파일:라인 | 함수 | 비고 |
|---|---|---|
| client.go:39 | `NopClient.Chat` | "real AI client 배선 전까지"의 M1 스텁 — 유지 |
