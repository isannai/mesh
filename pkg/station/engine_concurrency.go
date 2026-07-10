package station

// engine_concurrency.go — derive the queue's worker count from the engine's
// own concurrent-slot variable in its .env (llama PARALLEL, vLLM MAX_NUM_SEQS),
// as declared by manifest.queue.concurrency_env.
//
// Why: the engine .env is the single source of truth for parallelism — it's
// VRAM-bound and tuned by the operator alongside CTX_SIZE. The queue should
// never forward more requests than the engine can serve at once, so it follows
// the .env instead of being configured separately. The operator sets the
// number in ONE place (.env) and the queue auto-matches. Engines with no
// parallel concept (sd) omit concurrency_env and stay at the manifest default.
//
// Direction is one-way (.env → queue) on purpose: writing the queue's value
// back into .env would clobber the operator's slot/CTX/VRAM tuning.

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/daesob/http3proxy/pkg/setup"
)

// engineEnvConcurrency resolves a service's concurrent-slot count from its
// engine .env, keyed by the manifest's queue.concurrency_env. The .env lives at
// <root>/engines/<svc.Engine>/.env — the same file docker compose reads.
//
// Returns 0 ("no opinion — defer to the next resolution layer") when: the
// manifest declares no concurrency_env (engine has no parallel concept, e.g.
// sd), the service has no engine name, root is empty, the .env is missing, the
// variable is absent, or its value isn't a positive int.
func engineEnvConcurrency(root string, svc setup.ServiceEntry, m *manifest.Manifest) int {
	if m == nil || m.Queue.ConcurrencyEnv == "" || svc.Engine == "" || root == "" {
		return 0
	}
	envPath := filepath.Join(root, "engines", svc.Engine, ".env")
	return readEnvInt(envPath, m.Queue.ConcurrencyEnv)
}

// readEnvInt reads a KEY=VALUE .env file (the shell-sourced engine env) and
// returns the positive integer value of key, or 0 when the file/key is missing
// or the value doesn't parse to a positive int. Tolerates blank lines, '#'
// comment lines, surrounding whitespace, an optional "export " prefix, single/
// double quotes around the value, and an inline trailing comment on unquoted
// values. The first matching key wins.
func readEnvInt(path, key string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		if strings.TrimSpace(line[:eq]) != key {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		// Strip an inline trailing comment (VALUE # note) only when the value
		// isn't quoted — a '#' inside quotes is literal.
		if !strings.HasPrefix(val, `"`) && !strings.HasPrefix(val, "'") {
			if h := strings.IndexByte(val, '#'); h >= 0 {
				val = strings.TrimSpace(val[:h])
			}
		}
		val = strings.Trim(val, `"'`)
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			return n
		}
		return 0 // key found but unusable — don't keep scanning for a dupe
	}
	return 0
}
