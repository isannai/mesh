import React from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "@i18n";
import content from "./content/install";
import "./docs-guide.scss";

export default function ProviderInstallGuide() {
  const { t, lang } = useTranslation();
  const c = content[lang] || content.en;

  return (
    <div className="page docs-guide">
      <div className="page-header">
        <div className="docs-breadcrumb">
          <Link to="/docs">Docs</Link> <span>/</span> <span>{t("docs.install.title")}</span>
        </div>
        <h2>{t("docs.install.title")}</h2>
        <p className="docs-lede">{t("docs.install.lede")}</p>
      </div>

      <div className="page-body docs-body">
        <Section title={t("docs.install.prereq")}>
          <ul>
            {c.prereq.items.map((it, i) => <li key={i}>{it}</li>)}
          </ul>
          <p className="note">{c.prereq.note}</p>
        </Section>

        <Section title={t("docs.install.download_methods")}>
          <p>
            {c.methods.intro_before}<Link to="/welcome">Welcome</Link>{c.methods.intro_after}
          </p>
          <table className="docs-table">
            <thead>
              <tr>{c.methods.header.map((h, i) => <th key={i}>{h}</th>)}</tr>
            </thead>
            <tbody>
              {c.methods.rows.map((row, i) => (
                <tr key={i}>{row.map((cell, j) => <td key={j}>{cell}</td>)}</tr>
              ))}
            </tbody>
          </table>
        </Section>

        <Section title={t("docs.install.walkthrough_powershell")}>
          <h4>{c.walk.step1_h}</h4>
          <p>{c.walk.step1_p}</p>

          <h4>{c.walk.step2_h}</h4>
          <pre>{`Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
.\\install-iann.ps1`}</pre>

          <h4>{c.walk.step3_h}</h4>
          <p>{c.walk.step3_p_a}<code>C:\IANN</code>{c.walk.step3_p_b}<code>$installDir</code>{c.walk.step3_p_c}</p>

          <h4>{c.walk.step4_h}</h4>
          <p>{c.walk.step4_p}</p>
          <ul>
            {c.walk.step4_items.map((it, i) => (
              <li key={i}><strong>{it[0]}</strong>{it[1]}</li>
            ))}
          </ul>

          <h4>{c.walk.step5_h}</h4>
          <p>{c.walk.step5_p_a}{c.walk.step5_p_b}</p>
        </Section>

        <Section title={t("docs.install.prompts")}>
          <p>
            {c.prompts.intro_a}<strong>{c.prompts.intro_bold}</strong>{c.prompts.intro_b}
            <code>{c.prompts.intro_code}</code>{c.prompts.intro_c}
          </p>

          {c.prompts.list.map(p => (
            <Prompt key={p.n} recommendLabel={c.prompts.recommend} {...p} />
          ))}
        </Section>

        <Section title={t("docs.install.troubleshoot")}>
          <h4>{c.trouble.h_smartscreen}</h4>
          <p>{c.trouble.p_smartscreen}</p>

          <h4>{c.trouble.h_firewall}</h4>
          <p>{c.trouble.p_firewall}</p>

          <h4>{c.trouble.h_perm}</h4>
          <p>
            {c.trouble.p_perm_a}<code>{c.trouble.p_perm_code}</code>{c.trouble.p_perm_b}
            <code>{c.trouble.p_perm_code2}</code>{c.trouble.p_perm_c}
          </p>
        </Section>

        <Section title={t("docs.install.next")}>
          <p>{c.next.intro}</p>
          <ul>
            <li><Link to="/docs/provider-run">{c.next.run_title}</Link>{c.next.run_desc}</li>
            <li><Link to="/docs/tpm-setup">{c.next.tpm_title}</Link>{c.next.tpm_desc}</li>
          </ul>
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

function Prompt({ n, title, q, recommend, recommend_multi, warn, recommendLabel }) {
  return (
    <div className="installer-prompt">
      <div className="prompt-head">
        <span className="prompt-num">{n}</span>
        <strong>{title}</strong>
      </div>
      <pre className="prompt-q">{q}</pre>
      <div className="prompt-rec">
        <strong>{recommendLabel}</strong>
        {recommend && <span>{recommend}</span>}
        {recommend_multi && (
          <div>
            {recommend_multi.map((line, i) => (
              <div key={i}><strong>{line[0]}</strong>{line[1]}</div>
            ))}
          </div>
        )}
      </div>
      {warn && <div className="prompt-warn">⚠ {warn}</div>}
    </div>
  );
}
