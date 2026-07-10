import React from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "@i18n";
import "./index.scss";

// IANN bootstrap landing.
//
// Provider 자체 설치는 CLI (`installer install --name=provider`) 로 수행. 자세한
// 가이드는 /welcome 페이지에 있음. provider 가 올라간 뒤 다른 software
// (sd-api, llm-api, model 등) 는 my-nodes 의 Settings → ServiceTab 에서
// `/node/{id}/installer/*` 경로로 설치.
//
// 이전엔 broker 가 `/v1/local/*` 로 자기 머신 software 를 직접 관리했지만
// 보안 (anonymous install 백도어, 임의 PID kill 백도어) 문제로 전부 제거됨.
export default function Setup() {
  const { t } = useTranslation();

  return (
    <div className="page">
      <div className="page-header">
        <h2>{t("setup.title")}</h2>
      </div>
      <div className="page-body">
        <div className="install-provider-card">
          <div className="ipc-title">
            <span>🚀 IANN Provider</span>
          </div>
          <div className="ipc-desc">
            자기 PC 를 GPU 노드로 만들어 매출과 크레딧을 창출하세요.
            Installer 다운로드 + 실행 가이드는 Welcome 페이지에서 제공.
          </div>
          <div className="ipc-actions">
            <Link to="/welcome" className="ipc-btn primary">
              📥 Provider 설치하러 가기 →
            </Link>
          </div>
        </div>

        <div className="install-provider-card mt-16">
          <div className="ipc-title">
            <span>📦 Service / Engine / Model</span>
          </div>
          <div className="ipc-desc">
            Provider 가 올라온 뒤 sd-api / llm-api / 엔진 / 모델 설치는
            <strong> My Nodes &rarr; Settings 탭</strong> 에서 노드별로 관리합니다.
          </div>
          <div className="ipc-actions">
            <Link to="/my-nodes" className="ipc-btn primary">
              내 노드 관리로 이동 →
            </Link>
          </div>
        </div>
      </div>
    </div>
  );
}
