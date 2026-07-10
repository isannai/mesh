import React from "react";
import "./index.scss";

export default function StatCard({ label, value, color }) {
  return (
    <div className="stat-card" style={{ borderTopColor: color || "var(--color-primary)" }}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  );
}
