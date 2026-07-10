import React from "react";
import "./index.scss";

export default function NumberInput({
  value,
  onChange,
  step = 1,
  min,
  max,
  placeholder,
  disabled = false,
  className = "",
}) {
  const handleChange = (e) => {
    const raw = e.target.value;
    if (raw === "") { onChange(""); return; }
    const n = Number(raw);
    if (Number.isNaN(n)) return;
    onChange(n);
  };

  return (
    <input
      type="number"
      className={`number-input ${className}`}
      value={value ?? ""}
      step={step}
      min={min}
      max={max}
      placeholder={placeholder}
      disabled={disabled}
      onChange={handleChange}
    />
  );
}
