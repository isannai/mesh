package station

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/setup"
)

// docker.go — provider 가 isannd 의 docker 컨트롤러를 호출해서 서비스 컨테이너
// lifecycle 을 제어한다. 이전 spawnService (engine-runner spawn) 폐기 후 그
// 자리를 대신하는 얇은 HTTP wrapper.
//
// 모든 호출은 provider 와 같은 호스트에 떠있는 isannd 의 internal listener
// (conf 의 outbound_gateway.node_bridge_addr — 보통 http://127.0.0.1:8443) 로
// POST. isannd 는 isLocalSourceIP 가드로 외부 접근을 거부하므로, 이 경로는
// 같은 호스트끼리만 의미가 있다.

// dockerStartRequest mirrors cmd/isannd/admin_docker.go's request type so the
// JSON body matches exactly. Duplicated rather than imported to avoid a
// circular package dependency (cmd/isannd can't be imported from pkg/).
type dockerStartRequest struct {
	Image      string                `json:"image"`
	Ports      []dockerPortBinding   `json:"ports,omitempty"`
	Volumes    []dockerVolumeBinding `json:"volumes,omitempty"`
	Env        map[string]string     `json:"env,omitempty"`
	Entrypoint []string              `json:"entrypoint,omitempty"`
	Command    []string              `json:"command,omitempty"`
	GPU        string                `json:"gpu,omitempty"`
	Restart    string                `json:"restart,omitempty"`
	LogMaxSize string                `json:"log_max_size,omitempty"`
	LogMaxFile int                   `json:"log_max_file,omitempty"`
	Probe      *dockerProbeSpec      `json:"probe,omitempty"`
}

type dockerPortBinding struct {
	Host      string `json:"host"`
	Container int    `json:"container"`
}

type dockerVolumeBinding struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	ReadOnly  bool   `json:"ro,omitempty"`
}

type dockerProbeSpec struct {
	URL            string `json:"url"`
	TimeoutMS      int    `json:"timeout_ms,omitempty"`
	ExpectedStatus int    `json:"expected_status,omitempty"`
	ModelPath      string `json:"model_path,omitempty"`
}

// containerNameFor 는 conf 의 서비스 엔트리 + 매니페스트 조합에서 docker
// 컨테이너의 실제 이름을 결정한다. 우선순위 = manifest.Container.Name →
// svc.Engine (예: "sd") → svc.Name (예: "sd-api"). svc.Name 그대로 쓰면
// /internal/api/docker/{stop,start,probe}/sd-api 로 가는데 실제 컨테이너는
// "sd" 라서 isannd 가 찾지 못한다. 매니페스트 파일 stem (= svc.Engine)
// 이 컨테이너 이름과 같다는 규약으로 안전하게 매칭.
func containerNameFor(svc setup.ServiceEntry, m *manifest.Manifest) string {
	if m != nil && m.Container != nil && m.Container.Name != "" {
		return m.Container.Name
	}
	if svc.Engine != "" {
		return svc.Engine
	}
	return svc.Name
}

// dockerStart asks the local isannd to run the engine container for svc.
// The container's name is resolved via containerNameFor (manifest > engine >
// service name). The ready_check spec is forwarded so isannd's probeRegistry
// can answer /internal/api/docker/probe/{name} without a re-supply on every probe.
func (p *Provider) dockerStart(svc setup.ServiceEntry) error {
	m := loadServiceManifest(svc, p.PackagesDir)
	if m == nil {
		return fmt.Errorf("docker start %q: manifest not found", svc.Name)
	}
	if m.Container == nil {
		return fmt.Errorf("docker start %q: manifest has no container block (launcher=%q)", svc.Name, m.Launcher)
	}
	body := buildDockerStartBody(svc, m)
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("docker start %q: marshal body: %w", svc.Name, err)
	}
	url := p.isanndInternalURL() + "/internal/api/docker/start/" + containerNameFor(svc, m)
	return callIsanndDocker(http.MethodPost, url, bytes.NewReader(raw))
}

// dockerStop asks the local isannd to stop the engine container for svc.
// Container metadata + log files are preserved (no rm) so operators can
// inspect post-mortem; explicit removal is out of scope.
func (p *Provider) dockerStop(svc setup.ServiceEntry) error {
	m := loadServiceManifest(svc, p.PackagesDir)
	url := p.isanndInternalURL() + "/internal/api/docker/stop/" + containerNameFor(svc, m)
	return callIsanndDocker(http.MethodPost, url, nil)
}

// isanndInternalURL returns the base URL provider uses to reach the same-
// host isannd's internal listener. Comes from conf/provider.json's
// outbound_gateway.node_bridge_addr (typically http://127.0.0.1:8443).
func (p *Provider) isanndInternalURL() string {
	if u := p.Cfg.OutboundGateway.URL(); u != "" {
		return u
	}
	return "http://127.0.0.1:8443"
}

// buildDockerStartBody maps manifest.ContainerSpec onto isannd's
// dockerStartRequest. Fields are 1:1 by design — manifest schema was
// shaped to match isannd's wire format so this is mostly a struct copy.
func buildDockerStartBody(svc setup.ServiceEntry, m *manifest.Manifest) dockerStartRequest {
	c := m.Container
	body := dockerStartRequest{
		Image:      c.Image,
		Env:        c.Env,
		Entrypoint: c.Entrypoint,
		Command:    c.Command,
		GPU:        c.GPU,
		Restart:    c.Restart,
	}
	for _, pm := range c.Ports {
		body.Ports = append(body.Ports, dockerPortBinding{Host: pm.Host, Container: pm.Container})
	}
	for _, vm := range c.Volumes {
		body.Volumes = append(body.Volumes, dockerVolumeBinding{
			Host:      vm.Host,
			Container: vm.Container,
			ReadOnly:  vm.ReadOnly,
		})
	}
	if c.LogMaxMB > 0 {
		body.LogMaxSize = strconv.Itoa(c.LogMaxMB) + "m"
	}
	// Probe spec lets isannd answer /internal/api/docker/probe/{name} without
	// the caller re-supplying the URL. Skip when no ready_check declared.
	if m.ReadyCheck.URL != "" {
		body.Probe = &dockerProbeSpec{
			URL:            m.ReadyCheck.URL,
			TimeoutMS:      m.ReadyCheck.PollTimeoutMS,
			ExpectedStatus: 200,
			ModelPath:      m.ReadyCheck.ModelPath,
		}
	}
	return body
}

// callIsanndDocker is a thin POST helper. 30s timeout — `docker run -d`
// returns immediately but `docker stop -t 10` can take up to ~10s. Errors
// from isannd come back as JSON {"error":"..."} bodies; we surface them
// verbatim so logs show the host-side reason directly.
var isanndDockerClient = &http.Client{Timeout: 30 * time.Second}

func callIsanndDocker(method, url string, body io.Reader) error {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := isanndDockerClient.Do(req)
	if err != nil {
		return fmt.Errorf("isannd docker call %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("isannd docker %s returned %d: %s", url, resp.StatusCode, string(respBody))
}
