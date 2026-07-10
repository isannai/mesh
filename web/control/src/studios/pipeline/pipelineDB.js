// IndexedDB wrapper for pipeline storage with folder support.
// DB: "iann_pipelines" v2
// Store "pipelines": { id (auto), name, folder, nodes, edges, updatedAt }
// Store "folders":   { id (auto), name, order }

const DB_NAME = "iann_pipelines";
const DB_VERSION = 3;

function openDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = (e) => {
      const db = req.result;
      // pipelines store
      if (!db.objectStoreNames.contains("pipelines")) {
        const ps = db.createObjectStore("pipelines", { keyPath: "id", autoIncrement: true });
        ps.createIndex("folder", "folder", { unique: false });
      }
      // folders store
      if (!db.objectStoreNames.contains("folders")) {
        db.createObjectStore("folders", { keyPath: "id", autoIncrement: true });
      }
      // migration: add folder index + field to existing pipelines
      if (e.oldVersion < 3) {
        const tx = req.transaction;
        if (tx) {
          const store = tx.objectStore("pipelines");
          if (!store.indexNames.contains("folder")) {
            store.createIndex("folder", "folder", { unique: false });
          }
          store.openCursor().onsuccess = (ev) => {
            const cursor = ev.target.result;
            if (cursor) {
              if (!cursor.value.folder && cursor.value.folder !== "") {
                cursor.update({ ...cursor.value, folder: "" });
              }
              cursor.continue();
            }
          };
        }
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function tx(storeName, mode, fn) {
  return openDB().then(db => {
    return new Promise((resolve, reject) => {
      const t = db.transaction(storeName, mode);
      const store = t.objectStore(storeName);
      fn(store, resolve, reject);
      t.onerror = () => reject(t.error);
    });
  });
}

// --- Folders ---

export function listFolders() {
  return tx("folders", "readonly", (store, resolve) => {
    const req = store.getAll();
    req.onsuccess = () => resolve(req.result || []);
  });
}

export function createFolder(name) {
  return tx("folders", "readwrite", (store, resolve) => {
    const req = store.add({ name, order: Date.now() });
    req.onsuccess = () => resolve(req.result);
  });
}

export function renameFolder(id, name) {
  return tx("folders", "readwrite", (store, resolve, reject) => {
    const getReq = store.get(id);
    getReq.onsuccess = () => {
      if (!getReq.result) { reject(new Error("not found")); return; }
      store.put({ ...getReq.result, name });
      resolve();
    };
  });
}

export function deleteFolder(id) {
  // Delete folder and move its pipelines to root
  return openDB().then(db => {
    return new Promise((resolve, reject) => {
      const t = db.transaction(["folders", "pipelines"], "readwrite");
      t.objectStore("folders").delete(id);
      // Move pipelines in this folder to root (scan all, no index needed)
      const pStore = t.objectStore("pipelines");
      pStore.openCursor().onsuccess = (e) => {
        const cursor = e.target.result;
        if (cursor) {
          if (String(cursor.value.folder) === String(id)) {
            pStore.put({ ...cursor.value, folder: "" });
          }
          cursor.continue();
        }
      };
      t.oncomplete = () => resolve();
      t.onerror = () => reject(t.error);
    });
  });
}

// --- Pipelines ---

export function listPipelines() {
  return tx("pipelines", "readonly", (store, resolve) => {
    const req = store.getAll();
    req.onsuccess = () => resolve(
      (req.result || []).map(({ id, name, folder, updatedAt }) => ({ id, name, folder: folder || "", updatedAt }))
    );
  });
}

export function getPipeline(id) {
  return tx("pipelines", "readonly", (store, resolve) => {
    const req = store.get(id);
    req.onsuccess = () => resolve(req.result || null);
  });
}

export function savePipeline({ id, name, folder, nodes, edges }) {
  const updatedAt = Date.now();
  if (id) {
    return tx("pipelines", "readwrite", (store, resolve, reject) => {
      const getReq = store.get(id);
      getReq.onsuccess = () => {
        if (!getReq.result) { reject(new Error("not found")); return; }
        store.put({ id, name, folder: folder || getReq.result.folder || "", nodes, edges, updatedAt });
        resolve(id);
      };
    });
  }
  return tx("pipelines", "readwrite", (store, resolve) => {
    const req = store.add({ name, folder: folder || "", nodes, edges, updatedAt });
    req.onsuccess = () => resolve(req.result);
  });
}

export function renamePipeline(id, name) {
  return tx("pipelines", "readwrite", (store, resolve, reject) => {
    const getReq = store.get(id);
    getReq.onsuccess = () => {
      if (!getReq.result) { reject(new Error("not found")); return; }
      store.put({ ...getReq.result, name, updatedAt: Date.now() });
      resolve();
    };
  });
}

export function movePipeline(id, folder) {
  return tx("pipelines", "readwrite", (store, resolve, reject) => {
    const getReq = store.get(id);
    getReq.onsuccess = () => {
      if (!getReq.result) { reject(new Error("not found")); return; }
      store.put({ ...getReq.result, folder: folder || "", updatedAt: Date.now() });
      resolve();
    };
  });
}

export function deletePipeline(id) {
  return tx("pipelines", "readwrite", (store, resolve) => {
    store.delete(id);
    resolve();
  });
}
