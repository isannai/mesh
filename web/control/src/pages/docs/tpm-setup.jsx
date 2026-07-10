import React from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "@i18n";
import content from "./content/tpm";
import "./docs-guide.scss";

export default function TPMSetupGuide() {
  const { t, lang } = useTranslation();
  const c = content[lang] || content.en;

  return (
    <div className="page docs-guide">
      <div className="page-header">
        <div className="docs-breadcrumb">
          <Link to="/docs">Docs</Link> <span>/</span> <span>{t("docs.tpm.title")}</span>
        </div>
        <h2>{t("docs.tpm.title")}</h2>
        <p className="docs-lede">{t("docs.tpm.lede")}</p>
      </div>

      <div className="page-body docs-body">
        <div className="docs-callout verified">
          💎 {c.callout_a}<strong>{c.callout_bold}</strong>{c.callout_b}
        </div>

        <Section title={t("docs.tpm.what_is")}>
          <p>{c.what.p}<strong>{c.what.bold}</strong>{c.what.p2}</p>
          <ul>
            {c.what.items.map((it, i) => (
              <li key={i}>
                {it[0]}{it[1] && <strong>{it[1]}</strong>}{it[2]}
              </li>
            ))}
          </ul>
        </Section>

        <Section title={t("docs.tpm.check_status")}>
          <h4>{c.check_windows}</h4>
          <pre>{`# tpm.msc
Win + R → tpm.msc

# PowerShell
Get-Tpm

# Device Manager → Security devices → "Trusted Platform Module 2.0"`}</pre>

          <h4>{c.check_linux}</h4>
          <pre>{`sudo apt install tpm2-tools
sudo tpm2_getcap properties-fixed | grep TPM2_PT_MANUFACTURER
ls /dev/tpm*`}</pre>
        </Section>

        <Section title={t("docs.tpm.bios_enable")}>
          <p>{c.bios.intro_a}<code>{c.bios.keys}</code>{c.bios.intro_b}</p>

          <CPUBlock block={c.cpu.intel} />
          <CPUBlock block={c.cpu.amd} />
        </Section>

        <Section title={t("docs.tpm.verify")}>
          <p>{c.verify.p1_a}<code>{c.verify.p1_code}</code>{c.verify.p1_b}</p>
          <p>{c.verify.p2}<code>{c.verify.p2_code}</code>{c.verify.p2_b}</p>
        </Section>

        {c.flow && (
          <Section title={c.flow.title}>
            <ol style={{ paddingLeft: 20, lineHeight: 1.8 }}>
              {c.flow.steps.map((s, i) => <li key={i}>{s.replace(/^\d+\.\s*/, "")}</li>)}
            </ol>
            {c.flow.note && <div className="docs-callout">{c.flow.note}</div>}
          </Section>
        )}

        <Section title={t("docs.tpm.faq")}>
          {c.faq.map((f, i) => (
            <div key={i}>
              <h4>{f.q}</h4>
              <p>{f.a}</p>
            </div>
          ))}
        </Section>
      </div>
    </div>
  );
}

function Section({ title, children }) {
  return (
    <section className="docs-section">
      <h3>{title}</h3>
      {children}
    </section>
  );
}

function CPUBlock({ block }) {
  return (
    <div className="cpu-block">
      <h4>{block.label}</h4>
      <p>{block.desc}</p>
      <ul>
        {block.paths.map((p, i) => <li key={i}><code>{p}</code></li>)}
      </ul>
      {block.note && <div className="note">{block.note}</div>}
    </div>
  );
}
