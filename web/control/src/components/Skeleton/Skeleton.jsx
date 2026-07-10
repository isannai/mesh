import React from "react";
import "./index.scss";

function Skeleton({ width, height, borderRadius, style, className = "" }) {
  return (
    <div
      className={`skeleton ${className}`}
      style={{ width, height, borderRadius, ...style }}
    />
  );
}

function Line({ width = "100%", height = 14, style, className = "" }) {
  return <div className={`skeleton skeleton--line ${className}`} style={{ width, height, ...style }} />;
}

function Circle({ size = 32, style, className = "" }) {
  return <div className={`skeleton skeleton--circle ${className}`} style={{ width: size, height: size, ...style }} />;
}

function Block({ width = "100%", height = 40, borderRadius = 6, style, className = "" }) {
  return <div className={`skeleton skeleton--block ${className}`} style={{ width, height, borderRadius, ...style }} />;
}

Skeleton.Line = Line;
Skeleton.Circle = Circle;
Skeleton.Block = Block;

export default Skeleton;
