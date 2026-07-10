import React, { useState } from "react";
import { useTranslation } from "../../i18n";
import Skeleton from "@components/Skeleton/Skeleton";
import "./index.scss";

export default function DataTable({
  columns,
  data,
  actions,
  onEdit,
  onDelete,
  onRowClick,
  selectedKey,
  selectedValue,
  renderActions,    // (row) => React.ReactNode — 커스텀 액션 버튼
  expandable,       // (row) => React.ReactNode | null — 펼침 콘텐츠
  rowClassName,     // (row) => string — 행 추가 클래스
  emptyMessage,     // 빈 데이터 메시지
  loading,          // boolean — skeleton rows 표시
  loadingRows = 5,  // skeleton row 개수
}) {
  const { t } = useTranslation();
  const [expandedKey, setExpandedKey] = useState(null);

  const hasActions = actions || renderActions;

  if (loading) {
    return (
      <table className="data-table">
        <thead>
          <tr>
            {columns.map((col) => (
              <th key={col.key} style={{ textAlign: col.align || "left", width: col.width }}>{col.label}</th>
            ))}
            {hasActions && <th style={{ textAlign: "center" }}>{t("common.actions")}</th>}
          </tr>
        </thead>
        <tbody>
          {Array.from({ length: loadingRows }).map((_, i) => (
            <tr key={`sk-${i}`} className="dt-row-static">
              {columns.map((col) => (
                <td key={col.key} style={{ textAlign: col.align || "left", width: col.width }}>
                  <Skeleton.Line width={col.skeletonWidth || "70%"} />
                </td>
              ))}
              {hasActions && <td><Skeleton.Line width="60px" /></td>}
            </tr>
          ))}
        </tbody>
      </table>
    );
  }

  if (!data || data.length === 0) {
    return <p className="no-data">{emptyMessage || t("common.no_data")}</p>;
  }

  const isExpandable = !!expandable;

  return (
    <table className="data-table">
      <thead>
        <tr>
          {columns.map((col) => (
            <th key={col.key} style={{ textAlign: col.align || "left", width: col.width }}>{col.label}</th>
          ))}
          {hasActions && <th style={{ textAlign: "center" }}>{t("common.actions")}</th>}
        </tr>
      </thead>
      <tbody>
        {data.map((row, i) => {
          const rowKey = row.id || row.name || row.node_id || i;
          const isSelected = selectedKey && row[selectedKey] === selectedValue;
          const expanded = isExpandable && expandedKey === rowKey;
          const canExpand = isExpandable && expandable(row) !== null;
          const expandContent = expanded && canExpand ? expandable(row) : null;
          const extraClass = rowClassName ? rowClassName(row) : "";

          return (
            <React.Fragment key={rowKey}>
              <tr
                className={`${isSelected ? "selected" : ""} ${extraClass} ${onRowClick || canExpand ? "dt-row-clickable" : "dt-row-static"}`}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  if (canExpand) setExpandedKey(expanded ? null : rowKey);
                  if (onRowClick) onRowClick(row);
                }}
              >
                {columns.map((col) => (
                  <td key={col.key} style={{ textAlign: col.align || "left", width: col.width }}>
                    {isExpandable && col === columns[0] && (
                      canExpand
                        ? <span className={`expand-arrow ${expanded ? "open" : ""}`}>▶</span>
                        : <span className="expand-arrow hidden">▶</span>
                    )}
                    {col.render ? col.render(row[col.key], row) : row[col.key]}
                  </td>
                ))}
                {hasActions && (
                  <td className="actions">
                    <div className="actions-inner">
                      {renderActions ? renderActions(row) : (<>
                        {onEdit && <button className="btn btn-sm btn-edit" onClick={(e) => { e.stopPropagation(); onEdit(row); }}>{t("common.edit")}</button>}
                        {onDelete && !row.is_system && <button className="btn btn-sm btn-delete" onClick={(e) => { e.stopPropagation(); onDelete(row); }}>{t("common.delete")}</button>}
                      </>)}
                    </div>
                  </td>
                )}
              </tr>
              {expanded && expandContent && (
                <tr className="expand-row">
                  <td colSpan={99}>
                    {expandContent}
                  </td>
                </tr>
              )}
            </React.Fragment>
          );
        })}
      </tbody>
    </table>
  );
}
