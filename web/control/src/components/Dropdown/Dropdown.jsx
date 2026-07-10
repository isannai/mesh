import React, { useState, useRef, useEffect, useCallback } from "react";
import { createPortal } from "react-dom";
import "./index.scss";

export default function Dropdown({ value, options = [], onChange, placeholder = "-- Select --", disabled = false, required = false, searchable = false }) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [pos, setPos] = useState(null);
  const ref = useRef(null);
  const searchRef = useRef(null);
  const triggerRef = useRef(null);
  const listRef = useRef(null);

  // Compute pos from the trigger's current viewport rect. Used both at click
  // time (synchronous, before first paint of the open list) and on
  // scroll/resize while open.
  const recomputePos = useCallback(() => {
    if (!triggerRef.current) return;
    const r = triggerRef.current.getBoundingClientRect();
    const listH = 280;
    const spaceBelow = window.innerHeight - r.bottom;
    const above = spaceBelow < listH && r.top > spaceBelow;
    setPos({
      left: r.left,
      width: r.width,
      top: above ? undefined : r.bottom + 4,
      bottom: above ? window.innerHeight - r.top + 4 : undefined,
    });
  }, []);

  useEffect(() => {
    if (!open) return;
    // Re-pin to viewport while the list is open. Click-time recomputePos
    // already handled the first paint; this catches subsequent scroll/resize.
    window.addEventListener("scroll", recomputePos, true);
    window.addEventListener("resize", recomputePos);
    return () => {
      window.removeEventListener("scroll", recomputePos, true);
      window.removeEventListener("resize", recomputePos);
    };
  }, [open, recomputePos]);

  const selected = options.find(o => (typeof o === "object" ? o.value : o) === value);
  const displayText = selected ? (typeof selected === "object" ? selected.label : selected) : placeholder;

  const filteredOptions = searchable && search
    ? options.filter(o => {
        const label = (typeof o === "object" ? o.label : o).toLowerCase();
        const val = (typeof o === "object" ? o.value : o).toLowerCase();
        const q = search.toLowerCase();
        return label.includes(q) || val.includes(q);
      })
    : options;

  useEffect(() => {
    const handler = (e) => {
      // The list is portaled to document.body, so it's outside ref.current.
      // Treat clicks inside either the trigger wrapper or the portaled list as inside.
      const insideTrigger = ref.current && ref.current.contains(e.target);
      const insideList = listRef.current && listRef.current.contains(e.target);
      if (!insideTrigger && !insideList) {
        setOpen(false);
        setSearch("");
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  useEffect(() => {
    if (open && searchable && searchRef.current) searchRef.current.focus();
  }, [open, searchable]);

  const handleSelect = (val) => {
    onChange(val);
    setOpen(false);
    setSearch("");
  };

  return (
    <div ref={ref} className={`dropdown ${open ? "open" : ""} ${disabled ? "disabled" : ""}`}>
      <button
        ref={triggerRef}
        type="button"
        className="dropdown-trigger"
        onClick={() => {
          if (disabled) return;
          if (!open) {
            // Compute pos synchronously so the portaled list renders at the
            // correct viewport coords on the very first paint instead of
            // flashing in at top:0/left:0 until useEffect catches up.
            recomputePos();
          }
          setOpen(!open);
        }}
        disabled={disabled}
      >
        <span className={`dropdown-text ${!selected ? "placeholder" : ""}`}>{displayText}</span>
        <span className="dropdown-arrow">
          <svg width="10" height="6" viewBox="0 0 10 6"><path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></svg>
        </span>
      </button>
      {open && createPortal(
        // Render to document.body so the list is unaffected by ancestor overflow:auto
        // (e.g. scrollable modal bodies) or stacking-context isolation. The mousedown
        // handler walks the click target to detect "outside" via .dropdown-list
        // ancestor lookup, since the portaled list is no longer inside `ref.current`.
        <div ref={listRef} className="dropdown-list" style={pos ? {
          position: "fixed",
          left: pos.left, right: "auto",
          width: pos.width,
          // Explicit "auto" defeats the SCSS `top: 100%` rule when
          // opening upward — otherwise top:100% + bottom:<x> collapses
          // the element to zero height and the list disappears.
          top: pos.top ?? "auto",
          bottom: pos.bottom ?? "auto",
          margin: 0,
          zIndex: 9999,
        } : { visibility: "hidden", position: "fixed", right: "auto", zIndex: 9999 }}>
          {searchable && (
            <div className="dropdown-search">
              <input
                ref={searchRef}
                type="text"
                value={search}
                onChange={e => setSearch(e.target.value)}
                placeholder="Search..."
                className="dropdown-search-input"
                onClick={e => e.stopPropagation()}
              />
            </div>
          )}
          {!searchable && placeholder && (
            <div
              className={`dropdown-item ${!value ? "active" : ""}`}
              onClick={() => handleSelect("")}
            >
              <span className="dropdown-item-text">{placeholder}</span>
            </div>
          )}
          {filteredOptions.length === 0 && (
            <div className="dropdown-item" style={{ color: "var(--text-muted)", cursor: "default" }}>
              <span className="dropdown-item-text">No results</span>
            </div>
          )}
          {filteredOptions.map((o, i) => {
            const val = typeof o === "object" ? o.value : o;
            const label = typeof o === "object" ? o.label : o;
            const isActive = val === value;
            return (
              <div
                key={val || i}
                className={`dropdown-item ${isActive ? "active" : ""}`}
                onClick={() => handleSelect(val)}
              >
                <span className="dropdown-item-text">{label}</span>
                {isActive && <span className="dropdown-check">&#10003;</span>}
              </div>
            );
          })}
        </div>,
        document.body
      )}
    </div>
  );
}
