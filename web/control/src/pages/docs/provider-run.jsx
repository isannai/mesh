import React from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "@i18n";
import content from "./content/run";
import "./docs-guide.scss";

export default function ProviderRunGuide() {
  const { t, lang } = useTranslation();
  const c = content[lang] || content.en;

  return (
    <div className="page docs-guide">
      <div className="page-header">
        <div className="docs-breadcrumb">
          <Link to="/docs">Docs</Link> <span>/</span> <span>{t("docs.run.title")}</span>
        </div>
        <h2>{t("docs.run.title")}</h2>
        <p className="docs-lede">{t("docs.run.lede")}</p>
      </div>

      <div className="page-body docs-body">
        <Section title={t("docs.run.manual_start")}>
          <p>{c.manual.p}</p>
          <pre>{`# Windows
cd C:\\IANN
.\\bin\\provider.exe

# Linux / macOS
cd /opt/iann
./bin/provider`}</pre>
          <p className="note">{c.manual.note}</p>
        </Section>

        <Section title={t("docs.run.service_windows")}>
          <h4>{c.win.m1_h}</h4>
          <p>{c.win.m1_p}<code>{c.win.m1_code}</code>{c.win.m1_p2}</p>

          <h4>{c.win.m2_h}</h4>
          <pre>{`# Administrator PowerShell
sc create IANNProvider binPath= "C:\\IANN\\bin\\provider.exe" start= auto
sc description IANNProvider "IANN decentralized GPU node"
sc start IANNProvider

# Stop / remove
sc stop IANNProvider
sc delete IANNProvider`}</pre>
          <p className="note">
            {c.win.note_a}
            <a href="https://nssm.cc/" target="_blank" rel="noopener noreferrer">{c.win.note_link}</a>
            {c.win.note_b}
          </p>
        </Section>

        <Section title={t("docs.run.service_linux")}>
          <p>{c.linux.p}</p>
          <pre>{`[Unit]
Description=IANN Provider
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=iann
WorkingDirectory=/opt/iann
ExecStart=/opt/iann/bin/provider
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target`}</pre>
          <pre>{`sudo systemctl daemon-reload
sudo systemctl enable iann-provider
sudo systemctl start iann-provider
sudo systemctl status iann-provider`}</pre>
        </Section>

        <Section title={t("docs.run.service_macos")}>
          <p>{c.macos.p}</p>
          <pre>{`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>io.iann.provider</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/YOU/IANN/bin/provider</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>WorkingDirectory</key><string>/Users/YOU/IANN</string>
</dict>
</plist>`}</pre>
          <pre>{`launchctl load ~/Library/LaunchAgents/io.iann.provider.plist
launchctl start io.iann.provider`}</pre>
        </Section>

        <Section title={t("docs.run.logs")}>
          <p>{c.logs.p}</p>
          <ul>
            {c.logs.items.map((parts, i) => (
              <li key={i}>
                {parts.map((p, j) =>
                  j % 2 === 0 ? <React.Fragment key={j}>{p}</React.Fragment> : <code key={j}>{p}</code>
                )}
              </li>
            ))}
          </ul>
        </Section>

        <Section title={t("docs.run.update")}>
          <p>{c.update.p}</p>
          <pre>{`# Stop service
sudo systemctl stop iann-provider    # Linux
sc stop IANNProvider                 # Windows

# Update self
cd /opt/iann && ./installer update --self

# Restart
sudo systemctl start iann-provider`}</pre>
        </Section>

        <Section title={t("docs.run.next")}>
          <ul>
            <li><Link to="/docs/tpm-setup">{c.next.items[0][0]}</Link>{c.next.items[0][1]}</li>
            <li><Link to="/my-nodes">{c.next.items[1][0]}</Link>{c.next.items[1][1]}</li>
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
