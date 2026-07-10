import React from "react";
import ReactDOM from "react-dom/client";
import "@styles/index.scss";
import { LanguageProvider } from "@i18n";
import { ThemeProvider } from "@theme";
import { ToastProvider } from "@components/Toast/ToastContext";
import "@components/Toast/index.scss";
import { AuthProvider } from "./context/AuthContext";
import { PipelineLayoutProvider } from "./context/PipelineLayoutContext";
import Canvas from "@layout/canvas";

const root = ReactDOM.createRoot(document.getElementById("root"));
root.render(
  <ThemeProvider>
    <LanguageProvider>
      <ToastProvider>
        <AuthProvider>
          <PipelineLayoutProvider>
            <Canvas />
          </PipelineLayoutProvider>
        </AuthProvider>
      </ToastProvider>
    </LanguageProvider>
  </ThemeProvider>
);
