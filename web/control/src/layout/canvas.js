import React, { Suspense } from "react";
import { BrowserRouter, Routes, Route, Navigate, useLocation } from "react-router-dom";
import { useAuth } from "../context/AuthContext";
import { usePipelineLayout } from "../context/PipelineLayoutContext";
import Header from "@layout/header";
import Footer from "@layout/footer";
import Sidebar from "@layout/sidebar";
import Workspace from "@pages/workspace";
import Welcome from "@pages/welcome";
import Nodes from "@pages/nodes";
import NodeDetailPage from "@pages/node-detail";
import SearchPage from "@pages/search";
import MyNodes from "@pages/my-nodes";
import Settings from "@pages/settings";
import SystemLogs from "@pages/system/logs";
import Deploy from "@pages/models";
import Docs from "@pages/docs";
import ApiReference from "@pages/docs/api";
import ProviderInstallGuide from "@pages/docs/provider-install";
import ProviderRunGuide from "@pages/docs/provider-run";
import TPMSetupGuide from "@pages/docs/tpm-setup";
import VllmSetupGuide from "@pages/docs/vllm-setup";
import AboutPage from "@pages/docs/about";
import RoadmapPage from "@pages/docs/roadmap";

// Studios — lazy loaded (무거운 의존성 분리)
const PipelineStudio = React.lazy(() => import("@studios/pipeline/PipelinePage"));

function AdminOnly({ children }) {
  const { role } = useAuth();
  if (role !== "owner" && role !== "admin") return <Navigate to="/" replace />;
  return children;
}

function Loading() {
  return (
    <div className="canvas-empty-center">
      Loading...
    </div>
  );
}

// API Reference 는 좌측 카테고리 사이드바 필요
const SIDEBAR_ROUTES = ["/docs/api"];
// Marketing-tier docs pages — hub + About + Roadmap. The viewport-wide
// gradient backdrop is rendered when the user is on one of these so the
// flashy orbs cover the full canvas (not just the 1200px content cage).
// Content itself stays inside the cage so reading width is preserved.
const DOCS_MARKETING_ROUTES = ["/docs", "/docs/about", "/docs/roadmap"];

function AppLayout() {
  const location = useLocation();
  const { layout: pipelineLayout } = usePipelineLayout();
  const showSidebar = SIDEBAR_ROUTES.some(r => location.pathname.startsWith(r));
  const isPipeline = location.pathname.startsWith("/studios/pipeline");
  const isDocsMarketing = DOCS_MARKETING_ROUTES.includes(location.pathname);
  const fullwidth = isPipeline && pipelineLayout === "fullwidth";

  const mainClass = fullwidth ? "main-content main-content-fullwidth" : "main-content";
  const innerClass = `app-body-inner${fullwidth ? " full" : ""}`;

  return (
    <div className="app">
      {/* Marketing backdrop — fixed full-viewport orbs + grid pattern.
          Rendered only on docs hub / About / Roadmap. Sits behind every
          other layer (z-index: 0 on a fixed element, content above has
          its own stacking). The fade-out on route change is handled by
          React unmount — no animation needed. */}
      {isDocsMarketing && <div className="docs-marketing-bg" aria-hidden />}
      <Header />
      <div className="app-body">
        <div className={innerClass}>
          {showSidebar && <Sidebar />}
          <div className={mainClass}>
            <Routes>
              <Route path="/" element={<Workspace />} />
              <Route path="/welcome" element={<Welcome />} />
              <Route path="/search" element={<SearchPage />} />
              <Route path="/nodes" element={<Nodes />} />
              <Route path="/nodes/:nodeId" element={<NodeDetailPage />} />
              <Route path="/my-nodes" element={<MyNodes />} />
              <Route path="/deploy" element={<Deploy />} />
              <Route path="/docs" element={<Docs />} />
              <Route path="/docs/about" element={<AboutPage />} />
              <Route path="/docs/roadmap" element={<RoadmapPage />} />
              <Route path="/docs/provider-install" element={<ProviderInstallGuide />} />
              <Route path="/docs/provider-run" element={<ProviderRunGuide />} />
              <Route path="/docs/tpm-setup" element={<TPMSetupGuide />} />
              <Route path="/docs/vllm-setup" element={<VllmSetupGuide />} />
              <Route path="/docs/api" element={<ApiReference />} />
              <Route path="/settings" element={<Settings />} />
              <Route path="/system/logs" element={<SystemLogs />} />
              <Route path="/studios/pipeline" element={
                <Suspense fallback={<Loading />}><PipelineStudio /></Suspense>
              } />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </div>
        </div>
      </div>
      <Footer />
    </div>
  );
}

// detectBase derives the router basename from the <base href> the isannd
// remote-UI tunnel injects. Served at the root the base is "/" → basename "/"
// (no-op). Served through the tunnel it's "/node/<id>/" → basename "/node/<id>"
// so internal navigation and deep-route refresh keep the prefix.
function detectBase() {
  const href = document.querySelector("base")?.getAttribute("href");
  if (href && href !== "/") return href.replace(/\/$/, "");
  return "/";
}

export default function Canvas() {
  return (
    <BrowserRouter basename={detectBase()}>
      <AppLayout />
    </BrowserRouter>
  );
}
