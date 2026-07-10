// Local transform functions (run in browser, no API call)

/**
 * Extract a value from an object using dot-path notation.
 * e.g., extract({a:{b:[{c:1}]}}, "a.b.0.c") → 1
 */
export function extract(input, path) {
  if (!path) return input;
  // Normalize array brackets: "choices[0].message" → "choices.0.message"
  const normalized = path.replace(/\[(\d+)\]/g, ".$1");
  const parts = normalized.split(".");
  let val = input;
  for (const p of parts) {
    if (val == null) return undefined;
    val = val[p];
  }
  return val;
}

/**
 * Replace {{prev}} and {{in.field}} placeholders in a template string.
 */
export function template(input, tmpl, stepResults) {
  if (!tmpl) return input;
  let result = tmpl;
  // {{prev}} → previous step's output (input to this node)
  result = result.replace(/\{\{prev\}\}/g, typeof input === "string" ? input : JSON.stringify(input));
  // {{stepId}} → output of a specific step
  if (stepResults) {
    result = result.replace(/\{\{(\w+)\}\}/g, (_, id) => {
      const val = stepResults[id];
      return val != null ? (typeof val === "string" ? val : JSON.stringify(val)) : `{{${id}}}`;
    });
    // {{stepId.path}} → dot-path into a step's output
    result = result.replace(/\{\{(\w+)\.([^}]+)\}\}/g, (_, id, path) => {
      const val = extract(stepResults[id], path);
      return val != null ? (typeof val === "string" ? val : JSON.stringify(val)) : "";
    });
  }
  return result;
}

/**
 * Regex extract/replace.
 */
export function regex(input, pattern, replace) {
  if (!pattern) return input;
  const str = typeof input === "string" ? input : JSON.stringify(input);
  const re = new RegExp(pattern, "g");
  if (replace != null) {
    return str.replace(re, replace);
  }
  const match = str.match(re);
  return match ? match[0] : str;
}

/**
 * Run a transform node locally.
 */
export function runTransform(transform, input, params, stepResults) {
  switch (transform) {
    case "extract":
      return extract(input, params.path);
    case "template":
      return template(input, params.template, stepResults);
    case "regex":
      return regex(input, params.pattern, params.replace);
    case "json_merge":
      if (typeof input === "object" && typeof params === "object") {
        return { ...input, ...params };
      }
      return input;
    default:
      return input;
  }
}
