import React, { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "@i18n";
import { useAuth } from "../../context/AuthContext";
import { useToast } from "@components/Toast/ToastContext";
import { fetchGatePackage } from "../../api/software";
import "./index.scss";

// Welcome page — first-time onboarding hub.
// See docs/mockup/broker-welcome.html for the reference layout.

const BROKER_URL = (typeof window !== "undefined" && window.location.origin) || "";
const INSTALL_TOKEN = ""; // reserved for future token-based register flow

const OS_CONFIG = {
  windows: {
    label: "Windows x64",
    defaultDir: "C:\\IANN",
    installerCmd: "installer.exe",
    scriptName: "install-iann.ps1",
    scriptType: "PowerShell",
  },
  linux: {
    label: "Linux x64",
    defaultDir: "/opt/iann",
    installerCmd: "./installer",
    scriptName: "install-iann.sh",
    scriptType: "bash",
  },
  macos: {
    label: "macOS ARM",
    defaultDir: "~/IANN",
    installerCmd: "./installer",
    scriptName: "install-iann.sh",
    scriptType: "bash",
  },
};

function detectOS() {
  const ua = (typeof navigator !== "undefined" ? navigator.userAgent : "").toLowerCase();
  if (ua.includes("win")) return "windows";
  if (ua.includes("mac")) return "macos";
  return "linux";
}

function isValidAddress(addr) {
  return /^0x[a-fA-F0-9]{40}$/.test(addr);
}

function fmt(tpl, vars) {
  return tpl.replace(/\{(\w+)\}/g, (_, k) => (vars[k] != null ? vars[k] : `{${k}}`));
}

export default function Welcome() {
  const { t } = useTranslation();
  const { auth } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();

  const [os, setOs] = useState(detectOS);
  const [installDir, setInstallDir] = useState(() => OS_CONFIG[detectOS()].defaultDir);
  const [receiverMode, setReceiverMode] = useState("own");
  const [receiverAddr, setReceiverAddr] = useState(() => (auth?.address || ""));
  const [downloadMethod, setDownloadMethod] = useState("script");

  const [pkg, setPkg] = useState(null);
  const [pkgError, setPkgError] = useState(null);

  useEffect(() => {
    setInstallDir(OS_CONFIG[os].defaultDir);
  }, [os]);

  // Gate: one descriptor per installer. fetchGatePackage applies the shared
  // ETag cache (api/software.js) so reloading the Welcome page, or navigating
  // back to it, reuses the last response when the Gate content is unchanged.
  useEffect(() => {
    fetchGatePackage("core", "installer")
      .then(data => {
        if (!data) {
          setPkgError("not found");
          return;
        }
        setPkg(data);
      })
      .catch(err => setPkgError(err.message || "error"));
  }, []);

  const receiver = receiverMode === "own" ? receiverAddr.trim() : "";
  const receiverValid = receiverMode !== "own" || isValidAddress(receiver);

  // Filter Gate downloads by current OS.
  // Matches filename against the selected OS marker; keeps files without any
  // OS marker (shared conf, docs, etc.).
  const osFiles = useMemo(() => {
    if (!pkg || !pkg.downloads) return [];
    const markers = { windows: ["windows", "win"], linux: ["linux"], macos: ["darwin", "macos"] };
    const otherMarkers = Object.entries(markers).filter(([k]) => k !== os).flatMap(([, v]) => v);
    return pkg.downloads.filter(f => {
      const name = (f.file_name || "").toLowerCase();
      const hasOther = otherMarkers.some(m => name.includes(`-${m}-`) || name.includes(`_${m}_`) || name.endsWith(`-${m}`));
      return !hasOther;
    });
  }, [pkg, os]);

  const script = useMemo(() => {
    if (osFiles.length === 0) return "";
    if (downloadMethod === "script") {
      return os === "windows" ? buildPowerShell(osFiles, installDir, receiver) : buildBash(osFiles, installDir, receiver);
    }
    if (downloadMethod === "oneliner") {
      return buildOneLiner(os, installDir, receiver);
    }
    return buildManual(osFiles, installDir, receiver, os);
  }, [osFiles, installDir, receiver, downloadMethod, os]);

  const downloadDisabled = osFiles.length === 0 || !receiverValid;

  function handleDownload() {
    if (downloadDisabled) return;
    if (downloadMethod === "oneliner") {
      navigator.clipboard.writeText(script).then(() => toast.success(t("welcome.copied_cmd")));
      return;
    }
    if (downloadMethod === "binary") {
      // Prefer the .zip entry (Installer is now distributed as a single zip
      // per OS). Fall back to downloading every file if no zip found.
      const zip = osFiles.find(f => (f.file_name || "").toLowerCase().endsWith(".zip"));
      const target = zip ? [zip] : osFiles;
      target.forEach(f => {
        const u = f.url || f.download_url;
        if (!u) return;
        const a = document.createElement("a");
        a.href = u;
        a.download = f.file_name || "";
        a.rel = "noopener";
        a.click();
      });
      return;
    }
    const blob = new Blob([script], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = OS_CONFIG[os].scriptName;
    a.click();
    URL.revokeObjectURL(url);
  }

  async function handlePasteReceiver() {
    try {
      const text = await navigator.clipboard.readText();
      setReceiverAddr(text.trim());
    } catch {
      toast.error(t("welcome.clipboard_fail"));
    }
  }

  function handleCopyScript() {
    navigator.clipboard.writeText(script).then(() => toast.success(t("welcome.copied")));
  }

  const downloadBtnLabel = downloadMethod === "oneliner"
    ? t("welcome.copy_cmd")
    : downloadMethod === "binary"
      ? fmt(t("welcome.download_binary"), { n: osFiles.length })
      : fmt(t("welcome.download_script"), { name: OS_CONFIG[os].scriptName });

  return (
    <div className="welcome-wrap">
      <div className="welcome-header">
        <h1>{t("welcome.page_title")}</h1>
        <p>{t("welcome.page_subtitle")}</p>
        {auth?.address && (
          <div className="wallet-info">
            <span>🔐 {t("welcome.logged_in")}</span>
            <code>{auth.address.slice(0, 10)}...{auth.address.slice(-6)}</code>
          </div>
        )}
      </div>

      <div className="install-hero">
        <h2>🚀 {t("welcome.install_title")}</h2>
        <p className="desc">{t("welcome.install_desc")}</p>

        <div className="install-prompt-hint verified">
          🔑 {t("welcome.install_token_hint")}
        </div>

        <FormSection label={t("welcome.step_os")}>
          <div className="os-picker">
            {Object.entries(OS_CONFIG).map(([k, v]) => (
              <button
                key={k}
                type="button"
                className={`os-btn ${os === k ? "active" : ""}`}
                onClick={() => setOs(k)}
              >
                {k === "windows" ? "🪟" : k === "linux" ? "🐧" : "🍎"} {v.label}
              </button>
            ))}
          </div>
        </FormSection>

        <FormSection label={t("welcome.step_dir")} helper={t("welcome.step_dir_helper")}>
          <div className="input-row">
            <input
              className="input-field"
              type="text"
              value={installDir}
              onChange={e => setInstallDir(e.target.value)}
            />
            <button className="btn-icon" onClick={() => setInstallDir(OS_CONFIG[os].defaultDir)}>
              {t("welcome.default_btn")}
            </button>
          </div>
        </FormSection>

        <FormSection label={t("welcome.step_receiver")}>
          <div className="radio-group">
            <div
              className={`radio-item ${receiverMode === "auto" ? "selected" : ""}`}
              onClick={() => setReceiverMode("auto")}
            >
              <div className="title">
                <span className="radio-dot" />
                <span>{t("welcome.receiver_auto_title")}</span>
              </div>
              <div className="sub">{t("welcome.receiver_auto_desc")}</div>
            </div>

            <div
              className={`radio-item ${receiverMode === "own" ? "selected" : ""}`}
              onClick={() => setReceiverMode("own")}
            >
              <div className="title">
                <span className="radio-dot" />
                <span>{t("welcome.receiver_own_title")}</span>
              </div>
              <div className="sub">{t("welcome.receiver_own_desc")}</div>
              {receiverMode === "own" && (
                <>
                  <div className="input-row">
                    <input
                      className={`input-field ${receiverAddr && !receiverValid ? "invalid" : ""}`}
                      type="text"
                      placeholder={t("welcome.addr_placeholder")}
                      value={receiverAddr}
                      onChange={e => setReceiverAddr(e.target.value)}
                      onClick={e => e.stopPropagation()}
                    />
                    <button className="btn-icon" onClick={e => { e.stopPropagation(); handlePasteReceiver(); }}>
                      📋 {t("welcome.paste")}
                    </button>
                  </div>
                  <div className="helper">
                    {receiverAddr === "" && t("welcome.addr_required")}
                    {receiverAddr !== "" && (receiverValid ? t("welcome.addr_valid") : t("welcome.addr_invalid"))}
                  </div>
                </>
              )}
            </div>
          </div>
        </FormSection>

        <FormSection label={t("welcome.step_method")}>
          <div className="download-options">
            {[
              { id: "script",   icon: "📜", title: t("welcome.method_script_title"),   sub: t("welcome.method_script_sub") },
              { id: "oneliner", icon: "⚡", title: t("welcome.method_oneliner_title"), sub: t("welcome.method_oneliner_sub") },
              { id: "binary",   icon: "📦", title: t("welcome.method_binary_title"),   sub: t("welcome.method_binary_sub") },
            ].map(m => (
              <div
                key={m.id}
                className={`download-card ${downloadMethod === m.id ? "selected" : ""}`}
                onClick={() => setDownloadMethod(m.id)}
              >
                <div className="ico">{m.icon}</div>
                <div className="title">{m.title}</div>
                <div className="sub">{m.sub}</div>
              </div>
            ))}
          </div>

          <div className="download-action">
            <button className="btn-primary" onClick={handleDownload} disabled={downloadDisabled}>
              {downloadBtnLabel}
            </button>
            <span className="download-meta">
              {pkgError && <span className="error">{t("welcome.gate_error")}: {pkgError}</span>}
              {!pkgError && !pkg && t("welcome.gate_loading")}
              {pkg && fmt(t("welcome.files_count"), { n: osFiles.length, type: OS_CONFIG[os].scriptType })}
            </span>
          </div>
        </FormSection>

        {pkg && (
          <FormSection label={t("welcome.step_preview")} hint={t("welcome.preview_hint")}>
            <div className="code-block">
              <button className="copy-btn" onClick={handleCopyScript}>{t("welcome.copy")}</button>
              <pre dangerouslySetInnerHTML={{ __html: highlight(script) }} />
            </div>
          </FormSection>
        )}
      </div>

      <div className="side-grid single">
        <SideCardVerified t={t} />
      </div>

      <div className="footer-actions">
        <a href="#/docs/provider-install" onClick={e => { e.preventDefault(); navigate("/docs/provider-install"); }}>{t("welcome.guide_link")}</a>
        <div className="flex-row gap-8">
          <button className="btn-secondary" onClick={() => navigate("/")}>
            {t("welcome.later")}
          </button>
        </div>
      </div>
    </div>
  );
}

function FormSection({ label, helper, hint, children }) {
  return (
    <div className="form-section">
      <div className="label">
        {label}
        {hint && <span className="hint">{hint}</span>}
      </div>
      {children}
      {helper && <div className="helper">{helper}</div>}
    </div>
  );
}

function SideCardVerified({ t }) {
  return (
    <div className="side-card verified-card">
      <h3>{t("welcome.verified_title")}</h3>
      <ul className="mini-checklist">
        <li><span className="mini-check done verified">✓</span><span>{t("welcome.verified_faucet")}</span></li>
        <li><span className="mini-check done verified">✓</span><span>{t("welcome.verified_accum")}</span></li>
        <li><span className="mini-check done verified">✓</span><span>{t("welcome.verified_badge")}</span></li>
        <li className="muted"><span className="mini-check" /><span>{t("welcome.verified_market")}</span></li>
        <li className="muted"><span className="mini-check" /><span>{t("welcome.verified_fee")}</span></li>
      </ul>
      <div className="verified-box">
        {t("welcome.verified_note_line1")}<br />
        <strong>{t("welcome.verified_note_line2")}</strong><br />
        <span className="small">{t("welcome.verified_note_line3")}</span>
      </div>
    </div>
  );
}

// ── syntax highlight ────────────────────────────────────────────────

function escapeHtml(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function highlight(text) {
  if (!text) return "";
  let out = escapeHtml(text);
  out = out.replace(/^(#.*)$/gm, '<span class="c">$1</span>');
  out = out.replace(/(\$[A-Za-z_][\w]*|"\$[A-Za-z_][\w]*")/g, '<span class="v">$1</span>');
  out = out.replace(/(&quot;[^&"]*&quot;|"[^"]*")/g, m => `<span class="s">${m}</span>`);
  out = out.replace(/\b(install|update|uninstall|--install-dir|--receiver|--broker|installer\.exe|\.\/installer)\b/g, '<span class="cmd">$1</span>');
  return out;
}

// ── script builders ────────────────────────────────────────────────

// pickZip returns the first .zip entry from the Gate file list, or null.
function pickZip(files) {
  return files.find(f => (f.file_name || "").toLowerCase().endsWith(".zip")) || null;
}

function buildPowerShell(files, installDir, receiver) {
  const zip = pickZip(files);
  const recLine = receiver ? ` \`\n    --receiver ${receiver}` : "";
  if (!zip) {
    return `# No installer .zip found in Gate package. Check Gate registration.`;
  }
  const zipUrl = zip.url || zip.download_url || "";
  const zipName = zip.file_name;

  return `# IANN Provider Installer (PowerShell)
# 흐름: zip 다운로드 → 압축 해제 → installer install
$ErrorActionPreference = "Stop"

$installDir = "${installDir}"
$broker     = "${BROKER_URL}"
$receiver   = "${receiver || '(auto)'}"
$zipUrl     = "${zipUrl}"
$zipName    = "${zipName}"

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$zipPath = Join-Path $installDir $zipName

Write-Host "[1/3] Downloading $zipName ..."
Invoke-WebRequest -Uri $zipUrl -OutFile $zipPath

Write-Host "[2/3] Extracting to $installDir ..."
# 압축 해제 결과:
#   ${installDir}\\installer.exe
#   ${installDir}\\conf\\installer.json
Expand-Archive -Path $zipPath -DestinationPath $installDir -Force
Remove-Item $zipPath

Write-Host "[3/3] Running installer ..."
& "$installDir\\installer.exe" install \`
    --install-dir $installDir \`
    --broker $broker${recLine}

Write-Host "Done. Refresh your broker dashboard."
`;
}

function buildBash(files, installDir, receiver) {
  const zip = pickZip(files);
  const sudo = installDir.startsWith("/") ? "sudo " : "";
  const recLine = receiver ? ` \\\n    --receiver "${receiver}"` : "";
  if (!zip) {
    return `# No installer .zip found in Gate package. Check Gate registration.`;
  }
  const zipUrl = zip.url || zip.download_url || "";
  const zipName = zip.file_name;

  return `#!/usr/bin/env bash
# 흐름: zip 다운로드 → 압축 해제 → installer install
set -e

INSTALL_DIR="${installDir}"
BROKER="${BROKER_URL}"
RECEIVER="${receiver || '(auto)'}"
ZIP_URL="${zipUrl}"
ZIP_NAME="${zipName}"

${sudo}mkdir -p "$INSTALL_DIR"
ZIP_PATH="$INSTALL_DIR/$ZIP_NAME"

echo "[1/3] Downloading $ZIP_NAME ..."
${sudo}curl -fsSL "$ZIP_URL" -o "$ZIP_PATH"

echo "[2/3] Extracting to $INSTALL_DIR ..."
# 압축 해제 결과:
#   $INSTALL_DIR/installer
#   $INSTALL_DIR/conf/installer.json
${sudo}unzip -o -q "$ZIP_PATH" -d "$INSTALL_DIR"
${sudo}rm "$ZIP_PATH"
${sudo}chmod +x "$INSTALL_DIR/installer"

echo "[3/3] Running installer ..."
${sudo}"$INSTALL_DIR/installer" install \\
    --install-dir "$INSTALL_DIR" \\
    --broker "$BROKER"${recLine}

echo "Done. Refresh your broker dashboard."
`;
}

function buildOneLiner(os, installDir, receiver) {
  const recParam = receiver ? `&receiver=${encodeURIComponent(receiver)}` : "";
  const dirParam = `&dir=${encodeURIComponent(installDir)}`;
  if (os === "windows") {
    return `iwr "${BROKER_URL}/install.ps1?os=windows${dirParam}${recParam}" | iex`;
  }
  const osName = os === "macos" ? "darwin" : os;
  return `curl -sSf "${BROKER_URL}/install.sh?os=${osName}${dirParam}${recParam}" | sh`;
}

function buildManual(files, installDir, receiver, os) {
  const cmd = OS_CONFIG[os].installerCmd;
  const recArg = receiver ? ` --receiver ${receiver}` : "";
  const brokerArg = BROKER_URL ? ` --broker ${BROKER_URL}` : "";
  const zip = pickZip(files);
  const zipName = zip ? zip.file_name : "(installer zip)";
  const extractCmd = os === "windows"
    ? `Expand-Archive -Path ${zipName} -DestinationPath ${installDir} -Force`
    : `unzip -o ${zipName} -d ${installDir}`;
  const sep = os === "windows" ? "\\" : "/";
  const binaryName = os === "windows" ? "installer.exe" : "installer";

  return `# 1) ${zipName} 를 ${installDir} 로 이동 후 압축 해제
${extractCmd}

# 압축 해제 결과 (최초 상태):
#   ${installDir}${sep}${binaryName}
#   ${installDir}${sep}conf${sep}installer.json

# 2) ${installDir} 로 이동 후 install 실행
${cmd} install --install-dir ${installDir}${brokerArg}${recArg}

# install 완료 후 디렉토리:
#   ${installDir}${sep}${binaryName}         (재실행용)
#   ${installDir}${sep}conf${sep}auth.json        (Owner 주소)
#   ${installDir}${sep}conf${sep}keystore${sep}*    (지갑 — 백업 필수!)
#   ${installDir}${sep}bin${sep}                 (Provider 실행 파일)
#   ${installDir}${sep}packages${sep}            (서비스 / 엔진)`;
}
