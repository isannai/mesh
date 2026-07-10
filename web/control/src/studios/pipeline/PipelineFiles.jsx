import React, { useState, useEffect, useCallback } from "react";
import usePipelineStore from "./store";
import {
  listFolders, createFolder, renameFolder, deleteFolder,
  listPipelines, getPipeline, savePipeline, renamePipeline, movePipeline, deletePipeline,
} from "./pipelineDB";
import {
  buildPipelineExport, buildFolderExport, downloadJSON,
  detectSignatures, stripSignatures,
  pickFile, parseImportFile, validateImport,
  importPipeline, importFolder, loadFolderPipelines,
  pipelineFilename, folderFilename,
  FORMAT_PIPELINE, FORMAT_FOLDER,
} from "./pipelineIO";
import { useAuth } from "../../context/AuthContext";
import { useToast } from "@components/Toast/ToastContext";
import { useTranslation } from "@i18n";

export default function PipelineFiles() {
  const { t } = useTranslation();
  const [folders, setFolders] = useState([]);
  const [pipelines, setPipelines] = useState([]);
  const expanded = usePipelineStore(s => s.fileExpandedFolders);
  const setExpanded = usePipelineStore(s => s.setFileExpandedFolders);
  const [editing, setEditing] = useState(null); // { type: "folder"|"pipeline", id, name }
  const [contextMenu, setContextMenu] = useState(null); // { x, y, type, id, name, folder }
  const [dragOverFolder, setDragOverFolder] = useState(null);
  const [signDialog, setSignDialog] = useState(null); // { count, includeSignatures, onConfirm, onCancel }
  const pipelineId = usePipelineStore(s => s.pipelineId);
  const { auth } = useAuth();
  const toast = useToast();

  const reload = useCallback(async () => {
    const [f, p] = await Promise.all([listFolders(), listPipelines()]);
    setFolders(f);
    setPipelines(p);
  }, []);

  useEffect(() => { reload(); }, [reload]);

  // Close context menu on any outside click
  useEffect(() => {
    if (!contextMenu) return;
    const close = () => setContextMenu(null);
    document.addEventListener("click", close);
    return () => document.removeEventListener("click", close);
  }, [contextMenu]);

  // --- Actions ---

  const handleNewFolder = async () => {
    const id = await createFolder("New Folder");
    setExpanded(prev => ({ ...prev, [String(id)]: true }));
    await reload();
    setEditing({ type: "folder", id, name: "New Folder" });
  };

  const handleSave = async (folder = "") => {
    const state = usePipelineStore.getState();
    const name = state.pipelineName || "Untitled";
    const newId = await savePipeline({
      id: state.pipelineId,
      name,
      folder,
      nodes: state.nodes,
      edges: state.edges,
    });
    usePipelineStore.setState({ pipelineId: newId, pipelineName: name });
    await reload();
  };

  const handleSaveAs = async (folder = "") => {
    const name = prompt("Pipeline name:");
    if (!name?.trim()) return;
    const state = usePipelineStore.getState();
    const newId = await savePipeline({
      name: name.trim(),
      folder,
      nodes: state.nodes,
      edges: state.edges,
    });
    usePipelineStore.setState({ pipelineId: newId, pipelineName: name.trim() });
    await reload();
  };

  const handleLoad = async (id) => {
    const data = await getPipeline(id);
    if (!data) return;
    usePipelineStore.setState({
      nodes: data.nodes || [],
      edges: data.edges || [],
      pipelineName: data.name || "",
      pipelineId: data.id,
    });
  };

  const handleRename = async () => {
    if (!editing) return;
    const { type, id, name } = editing;
    if (!name.trim()) { setEditing(null); return; }
    if (type === "folder") await renameFolder(id, name.trim());
    else {
      await renamePipeline(id, name.trim());
      if (pipelineId === id) usePipelineStore.setState({ pipelineName: name.trim() });
    }
    setEditing(null);
    await reload();
  };

  const handleDelete = async (type, id) => {
    setContextMenu(null);
    if (type === "folder") {
      await deleteFolder(id);
    } else {
      await deletePipeline(id);
      if (usePipelineStore.getState().pipelineId === id) usePipelineStore.setState({ pipelineId: null });
    }
    await reload();
  };

  // --- Export / Import ---

  // Helper: prompt user when a graph carries sign credentials. Resolves to
  // {include: boolean} when user confirms, or null when canceled.
  const askSignaturePolicy = (count) => new Promise((resolve) => {
    setSignDialog({
      count,
      includeSignatures: false,
      onConfirm: (include) => { setSignDialog(null); resolve({ include }); },
      onCancel: () => { setSignDialog(null); resolve(null); },
    });
  });

  const handleExportPipeline = async (id, name) => {
    setContextMenu(null);
    try {
      const data = await getPipeline(id);
      if (!data) { toast.error("Pipeline not found"); return; }
      const sigNodes = detectSignatures(data);
      let graph = data;
      if (sigNodes.length > 0) {
        const decision = await askSignaturePolicy(sigNodes.length);
        if (!decision) return;
        if (!decision.include) graph = stripSignatures(data);
      }
      const exported = buildPipelineExport({ name, ...graph }, auth?.address);
      downloadJSON(pipelineFilename(name), exported);
      toast.success(`Exported "${name}"`);
    } catch (e) {
      toast.error(`Export failed: ${e.message}`);
    }
  };

  const handleExportFolder = async (folderId, folderName) => {
    setContextMenu(null);
    try {
      const ids = folderPipelines(folderId).map(p => p.id);
      if (ids.length === 0) { toast.error("Folder is empty"); return; }
      const graphs = await loadFolderPipelines(ids);
      const totalSigs = graphs.reduce((sum, g) => sum + detectSignatures(g).length, 0);
      let finalGraphs = graphs;
      if (totalSigs > 0) {
        const decision = await askSignaturePolicy(totalSigs);
        if (!decision) return;
        if (!decision.include) finalGraphs = graphs.map(stripSignatures);
      }
      const exported = buildFolderExport(folderName, finalGraphs, auth?.address);
      downloadJSON(folderFilename(folderName), exported);
      toast.success(`Exported folder "${folderName}"`);
    } catch (e) {
      toast.error(`Export failed: ${e.message}`);
    }
  };

  const handleImport = async () => {
    try {
      const file = await pickFile();
      if (!file) return;
      const text = await file.text();
      const parsed = parseImportFile(text);
      const { warnings } = validateImport(parsed);
      warnings.forEach(w => toast.error(w));

      if (parsed.format === FORMAT_PIPELINE) {
        const newId = await importPipeline(parsed, "");
        await reload();
        // Auto-load into canvas
        const data = await getPipeline(newId);
        if (data) {
          usePipelineStore.setState({
            nodes: data.nodes || [],
            edges: data.edges || [],
            pipelineName: data.name || "",
            pipelineId: data.id,
          });
        }
        toast.success(`Imported "${parsed.name || "pipeline"}"`);
      } else if (parsed.format === FORMAT_FOLDER) {
        const { pipelineIds } = await importFolder(parsed);
        await reload();
        toast.success(`Imported folder "${parsed.name}" (${pipelineIds.length} pipelines)`);
      }
    } catch (e) {
      toast.error(`Import failed: ${e.message}`);
    }
  };

  const onContextMenu = (e, type, item) => {
    e.preventDefault();
    e.stopPropagation();
    setContextMenu({
      x: e.clientX, y: e.clientY,
      type,
      id: item.id,
      name: item.name,
      folder: item.folder || "",
    });
  };

  // --- Render helpers ---

  const rootPipelines = pipelines.filter(p => !p.folder);
  const folderPipelines = (folderId) => pipelines.filter(p => String(p.folder) === String(folderId));

  const handleDrop = async (e, targetFolder) => {
    e.preventDefault();
    setDragOverFolder(null);
    const pipeId = e.dataTransfer.getData("pipeline-file-id");
    if (!pipeId) return;
    await movePipeline(Number(pipeId), targetFolder);
    await reload();
  };

  const renderPipelineItem = (p) => (
    <div
      key={p.id}
      className={`pf-item ${pipelineId === p.id ? "active" : ""}`}
      draggable
      onDragStart={(e) => { e.dataTransfer.setData("pipeline-file-id", String(p.id)); e.dataTransfer.effectAllowed = "move"; }}
      onClick={() => handleLoad(p.id)}
      onContextMenu={(e) => onContextMenu(e, "pipeline", p)}
      onDoubleClick={() => setEditing({ type: "pipeline", id: p.id, name: p.name })}
    >
      <span className="pf-icon">&#128196;</span>
      {editing?.type === "pipeline" && editing.id === p.id ? (
        <input
          className="pf-rename-input"
          value={editing.name}
          onChange={e => setEditing({ ...editing, name: e.target.value })}
          onBlur={handleRename}
          onKeyDown={e => { if (e.key === "Enter") handleRename(); if (e.key === "Escape") setEditing(null); }}
          autoFocus
          onClick={e => e.stopPropagation()}
        />
      ) : (
        <span className="pf-name">{p.name}</span>
      )}
    </div>
  );

  return (
    <div className="pf-container">
      {/* Toolbar */}
      <div className="pf-toolbar">
        <button className="pf-toolbar-btn" title={t("pipeline.file_new_folder")} onClick={handleNewFolder}>&#128193;+</button>
        <button className="pf-toolbar-btn" title={t("pipeline.file_save")} onClick={() => handleSave()}>&#128190;</button>
        <button className="pf-toolbar-btn" title={t("pipeline.file_save_as")} onClick={() => handleSaveAs()}>&#128190;+</button>
        <button className="pf-toolbar-btn" title={t("pipeline.file_import")} onClick={handleImport}>&#128229;</button>
      </div>

      {/* Tree */}
      <div
        className="pf-tree"
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => handleDrop(e, "")}
      >
        {folders.map(f => (
          <div key={f.id}>
            <div
              className={`pf-folder ${dragOverFolder === f.id ? "drag-over" : ""}`}
              onClick={() => setExpanded(prev => ({ ...prev, [f.id]: !prev[f.id] }))}
              onContextMenu={(e) => onContextMenu(e, "folder", f)}
              onDoubleClick={() => setEditing({ type: "folder", id: f.id, name: f.name })}
              onDragOver={(e) => { e.preventDefault(); e.stopPropagation(); setDragOverFolder(f.id); }}
              onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) setDragOverFolder(null); }}
              onDrop={(e) => { e.stopPropagation(); handleDrop(e, String(f.id)); }}
            >
              <span className="pf-arrow">{expanded[f.id] ? "\u25BC" : "\u25B6"}</span>
              <span className="pf-icon">&#128193;</span>
              {editing?.type === "folder" && editing.id === f.id ? (
                <input
                  className="pf-rename-input"
                  value={editing.name}
                  onChange={e => setEditing({ ...editing, name: e.target.value })}
                  onBlur={handleRename}
                  onKeyDown={e => { if (e.key === "Enter") handleRename(); if (e.key === "Escape") setEditing(null); }}
                  autoFocus
                  onClick={e => e.stopPropagation()}
                />
              ) : (
                <span className="pf-name">{f.name}</span>
              )}
              <span className="pf-count">{folderPipelines(f.id).length}</span>
            </div>
            {expanded[f.id] && (
              <div className="pf-folder-children">
                {folderPipelines(f.id).map(renderPipelineItem)}
                <div
                  className="pf-item pf-save-here"
                  onClick={() => handleSaveAs(String(f.id))}
                >
                  <span className="pf-icon">+</span>
                  <span className="pf-name">{t("pipeline.file_save_here")}</span>
                </div>
              </div>
            )}
          </div>
        ))}

        {/* Root pipelines */}
        {rootPipelines.map(renderPipelineItem)}
      </div>

      {/* Context Menu */}
      {contextMenu && (
        <div
          className="pf-context-menu"
          style={{ left: contextMenu.x, top: contextMenu.y }}
          onClick={(e) => e.stopPropagation()}
        >
          {contextMenu.type === "pipeline" && (
            <>
              <div className="pf-ctx-item" onClick={() => { handleLoad(contextMenu.id); setContextMenu(null); }}>Open</div>
              <div className="pf-ctx-item" onClick={() => { setEditing({ type: "pipeline", id: contextMenu.id, name: contextMenu.name }); setContextMenu(null); }}>{t("pipeline.file_rename")}</div>
              {folders.length > 0 && (
                <>
                  <div className="pf-ctx-separator" />
                  {contextMenu.folder && (
                    <div className="pf-ctx-item" onClick={async () => { await movePipeline(contextMenu.id, ""); setContextMenu(null); await reload(); }}>Move to Root</div>
                  )}
                  {folders.filter(f => String(f.id) !== String(contextMenu.folder)).map(f => (
                    <div key={f.id} className="pf-ctx-item" onClick={async () => { await movePipeline(contextMenu.id, String(f.id)); setContextMenu(null); await reload(); }}>Move to {f.name}</div>
                  ))}
                </>
              )}
              <div className="pf-ctx-separator" />
              <div className="pf-ctx-item" onClick={() => handleExportPipeline(contextMenu.id, contextMenu.name)}>Export</div>
              <div className="pf-ctx-separator" />
              <div className="pf-ctx-item danger" onClick={(e) => { e.stopPropagation(); handleDelete("pipeline", contextMenu.id); }}>Delete</div>
            </>
          )}
          {contextMenu.type === "folder" && (
            <>
              <div className="pf-ctx-item" onClick={() => { handleSaveAs(String(contextMenu.id)); setContextMenu(null); }}>Save here...</div>
              <div className="pf-ctx-item" onClick={() => { setEditing({ type: "folder", id: contextMenu.id, name: contextMenu.name }); setContextMenu(null); }}>Rename</div>
              <div className="pf-ctx-separator" />
              <div className="pf-ctx-item" onClick={() => handleExportFolder(contextMenu.id, contextMenu.name)}>Export Folder</div>
              <div className="pf-ctx-separator" />
              <div className="pf-ctx-item danger" onClick={(e) => { e.stopPropagation(); handleDelete("folder", contextMenu.id); }}>Delete Folder</div>
            </>
          )}
        </div>
      )}

      {/* Signature warning dialog */}
      {signDialog && (
        <div className="pf-modal-overlay" onClick={signDialog.onCancel}>
          <div className="pf-modal" onClick={(e) => e.stopPropagation()}>
            <div className="pf-modal-title">&#9888; Sign credentials detected</div>
            <div className="pf-modal-body">
              This pipeline contains <b>{signDialog.count}</b> node signature{signDialog.count > 1 ? "s" : ""}.
              <br />
              If included, anyone with the file can call those nodes using your credentials.
            </div>
            <label className="pf-modal-check">
              <input
                type="checkbox"
                checked={signDialog.includeSignatures}
                onChange={(e) => setSignDialog(prev => ({ ...prev, includeSignatures: e.target.checked }))}
              />
              <span>Include signatures (uncheck to strip them)</span>
            </label>
            <div className="pf-modal-actions">
              <button className="pf-modal-btn" onClick={signDialog.onCancel}>Cancel</button>
              <button className="pf-modal-btn primary" onClick={() => signDialog.onConfirm(signDialog.includeSignatures)}>Download</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
