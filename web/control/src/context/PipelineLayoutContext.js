import React, { createContext, useContext, useState, useEffect } from "react";

const PipelineLayoutContext = createContext();

export function PipelineLayoutProvider({ children }) {
  const [layout, setLayout] = useState(
    localStorage.getItem("iann_pipeline_layout") || "fullwidth"
  );

  useEffect(() => {
    localStorage.setItem("iann_pipeline_layout", layout);
  }, [layout]);

  return (
    <PipelineLayoutContext.Provider value={{ layout, setLayout }}>
      {children}
    </PipelineLayoutContext.Provider>
  );
}

export function usePipelineLayout() {
  return useContext(PipelineLayoutContext);
}
