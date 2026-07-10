import React from "react";

export default function Toggle({ value, onChange }) {
  return (
    <div className={`toggle-switch ${value ? "on" : ""}`} onClick={() => onChange(!value)}>
      <div className="toggle-knob" />
    </div>
  );
}

export function RestartBadge() {
  return <span style={{ color: "var(--color-warning)", fontSize: 11, marginLeft: 6 }}>(restart)</span>;
}
