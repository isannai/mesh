import React, { Suspense } from "react";
import GenericJsonForm from "./GenericJsonForm";
import { useTranslation } from "@i18n";

const KNOWN_FORMS = {
  "sd-api": React.lazy(() => import("./forms/SdApiForm")),
  "llm-api": React.lazy(() => import("./forms/LlmApiForm")),
};

export default function ServiceTab({ name, nodeId }) {
  const { t } = useTranslation();
  const Form = KNOWN_FORMS[name] || GenericJsonForm;

  return (
    <Suspense fallback={<p style={{ color: "var(--text-muted)", padding: 16 }}>{t("common.loading_short")}</p>}>
      <Form serviceName={name} nodeId={nodeId} />
    </Suspense>
  );
}
