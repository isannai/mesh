// Unified fetch wrapper with client-side timeout and idempotent retry.
//
// Why: broker 가 어떤 이유로든 응답을 못 보내거나 네트워크가 일시적으로
// 불안정할 때 브라우저가 영원히 pending 상태로 멈추는 걸 방지한다.
// GET/HEAD 는 멱등이라 자동 재시도, POST/PUT/DELETE 는 재시도하지 않는다.
//
// HTTP 응답 (4xx/5xx) 은 서버가 "실제로 응답한" 경우이므로 재시도하지
// 않고 그대로 호출자에게 돌려준다. timeout (AbortError) 과 네트워크
// 에러 (fetch 자체 실패) 만 재시도 대상.

const DEFAULT_TIMEOUT_MS = 10000;
const DEFAULT_RETRIES = 2;
const DEFAULT_BACKOFF_MS = 300;

export async function fetchWithTimeout(url, opts = {}, {
  timeoutMs = DEFAULT_TIMEOUT_MS,
  retries = DEFAULT_RETRIES,
  backoffMs = DEFAULT_BACKOFF_MS,
} = {}) {
  const method = (opts.method || "GET").toUpperCase();
  const canRetry = method === "GET" || method === "HEAD";
  const maxAttempts = canRetry ? retries : 0;

  for (let attempt = 0; attempt <= maxAttempts; attempt++) {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), timeoutMs);

    try {
      // If caller passed their own signal, respect it too
      const signal = opts.signal
        ? anyAbortSignal([opts.signal, ctl.signal])
        : ctl.signal;
      const res = await fetch(url, { ...opts, signal });
      clearTimeout(timer);
      return res; // HTTP 응답은 상태코드 무관하게 그대로 반환
    } catch (err) {
      clearTimeout(timer);

      const isTimeout = err && err.name === "AbortError";
      const isNetError = err && err.name === "TypeError"; // fetch failed / network down

      // 마지막 시도거나, 재시도 대상 에러가 아니면 호출자에게 던짐
      if (attempt === maxAttempts || !(isTimeout || isNetError)) {
        throw err;
      }

      // 백오프 후 재시도
      await new Promise((r) => setTimeout(r, backoffMs * (attempt + 1)));
    }
  }
}

// 복수 AbortSignal 중 하나라도 abort 되면 파생 signal 을 abort
function anyAbortSignal(signals) {
  const ctl = new AbortController();
  for (const s of signals) {
    if (s.aborted) {
      ctl.abort();
      return ctl.signal;
    }
    s.addEventListener("abort", () => ctl.abort(), { once: true });
  }
  return ctl.signal;
}
