// sd.cpp HTTP 호환 layer.
//
// stock sd.cpp HTTP 서버는 OpenAI-compat 핸들러에서 top-level
// `steps` / `cfg_scale` / `seed` / `sample_method` / `negative_prompt` /
// `strength` 를 무시한다 (자체 default 로 덮어씀). 이 필드들을 적용하려면
// `prompt` 안에 `<sd_cpp_extra_args>` JSON 태그로 박아 넣어야 한다.
//
// iSANN broker 의 managed 경로에서는 engine-runner 가 이 wrapping 을
// 자동으로 해주지만 (pkg/engine/runner/proxy.go:injectExtraArgs), external
// 패턴 (type: "sd-external") 으로 컨테이너에 직접 proxy 할 때는 engine-runner
// 가 경로에 없어서 wrap 이 일어나지 않는다. 어느 mode 에서나 일관되게
// 동작시키려고 클라이언트 (broker UI Try It / Studio pipeline runner / 외부
// 호출자) 가 직접 wrap 한 body 를 broker 로 보낸다. managed mode 에서는
// 이미 wrap 된 prompt 가 도착하므로 engine-runner 의 injectExtraArgs 는
// top-level 에서 옮길 게 없어 no-op 으로 통과한다.

// engineField → request field 매핑.
// pkg/engine/manifest/sd.cpp.json 의 api.extra_args.fields 와 일치.
const EXTRA_ARG_FIELDS = {
  steps: "steps",
  cfg_scale: "cfg_scale",
  sampling_method: "sample_method", // request 의 `sampling_method` → sd-server 의 `sample_method`
  seed: "seed",
  negative_prompt: "negative_prompt",
  strength: "strength",
};

const TAG = "sd_cpp_extra_args";

// `<sd_cpp_extra_args>...</sd_cpp_extra_args>` 가 이미 prompt 안에 있으면
// 또 박지 않음 (engine-runner 가 이미 wrap 한 경우 / 사용자가 수동으로 박은
// 경우 대비).
const ALREADY_WRAPPED_RE = new RegExp(`<\\s*${TAG}\\s*>`, "i");

/**
 * sd.cpp HTTP 서버용 wrap. body 를 mutate 하지 않고 새 객체 반환.
 *
 *   wrapSdCppExtraArgs({ prompt: "X", steps: 5, cfg_scale: 8, seed: 21, size: "512x512" })
 *   → { prompt: "X<sd_cpp_extra_args>{\"steps\":5,\"cfg_scale\":8,\"seed\":21}</sd_cpp_extra_args>",
 *       size: "512x512" }
 *
 * - extra-args 필드 (steps/cfg_scale/seed/sample_method/negative_prompt/strength)
 *   는 top-level 에서 제거하고 prompt 안 태그로 이동.
 * - 그 외 필드 (prompt, size, n, response_format, image, mask, width, height, ...)
 *   는 top-level 에 남김.
 * - prompt 가 이미 wrap 돼 있으면 그대로 둠.
 * - 빈 / 0 / undefined 값은 무시 (sd.cpp 의 default 사용).
 */
export function wrapSdCppExtraArgs(body) {
  if (!body || typeof body !== "object") return body;
  const out = { ...body };
  const prompt = out.prompt || "";
  if (typeof prompt === "string" && ALREADY_WRAPPED_RE.test(prompt)) {
    // 이미 wrap 됨 — extra-args 필드는 top-level 에서 그냥 제거하고 나머지
    // 그대로. (도중에 wrap 된 prompt 와 top-level 값이 중복되면 sd.cpp 의
    // OpenAI-compat 핸들러가 헷갈려서 default 로 덮어쓰는 회귀가 있어
    // 중복 제거가 더 안전하다.)
    for (const k of Object.keys(EXTRA_ARG_FIELDS)) {
      delete out[k];
    }
    return out;
  }

  const extra = {};
  for (const [reqField, engineField] of Object.entries(EXTRA_ARG_FIELDS)) {
    const v = out[reqField];
    // false 와 0 도 의도된 값일 수 있어 == null 만 거름. 단 빈 문자열은
    // sd.cpp 에 보내봐야 의미 없으니 제외.
    if (v == null) continue;
    if (typeof v === "string" && v === "") continue;
    extra[engineField] = v;
    delete out[reqField];
  }

  if (Object.keys(extra).length === 0) return out;

  out.prompt = String(prompt) + `<${TAG}>${JSON.stringify(extra)}</${TAG}>`;
  return out;
}
