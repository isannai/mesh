// IndexedDB store for my_nodes — replaces broker's /v1/my-nodes REST API.
//
// Record shape:
//   { id: string, label: string, auth: boolean, created_at: string }

import { openDB } from "./db";

const STORE = "my_nodes";

function tx(mode, fn) {
  return openDB().then(
    (db) =>
      new Promise((resolve, reject) => {
        const t = db.transaction(STORE, mode);
        const store = t.objectStore(STORE);
        fn(store, resolve, reject);
        t.onerror = () => reject(t.error);
      })
  );
}

function nowISO() {
  return new Date().toISOString().replace("T", " ").slice(0, 19);
}

/** List all my_nodes. */
export function listMyNodes() {
  return tx("readonly", (store, resolve) => {
    const req = store.getAll();
    req.onsuccess = () => resolve(req.result || []);
  });
}

/** Get a single node by ID. */
export function getMyNode(id) {
  return tx("readonly", (store, resolve) => {
    const req = store.get(id);
    req.onsuccess = () => resolve(req.result || null);
  });
}

/** Add or update a node. */
export function addMyNode(id, label = "") {
  const rec = { id, label, auth: false, created_at: nowISO() };
  return tx("readwrite", (store, resolve) => {
    const req = store.put(rec);
    req.onsuccess = () => resolve({ status: "ok" });
  });
}

/** Update node label. */
export function updateMyNode(id, label) {
  return tx("readwrite", (store, resolve, reject) => {
    const getReq = store.get(id);
    getReq.onsuccess = () => {
      if (!getReq.result) {
        reject(new Error("not found"));
        return;
      }
      const updated = { ...getReq.result, label };
      store.put(updated);
      resolve({ status: "ok" });
    };
  });
}

/** Delete a node. */
export function deleteMyNode(id) {
  return tx("readwrite", (store, resolve) => {
    store.delete(id);
    resolve({ status: "ok" });
  });
}

/** Update auth status for a node. */
export function updateMyNodeAuth(id, auth) {
  return tx("readwrite", (store, resolve, reject) => {
    const getReq = store.get(id);
    getReq.onsuccess = () => {
      if (!getReq.result) {
        reject(new Error("not found"));
        return;
      }
      const updated = { ...getReq.result, auth };
      store.put(updated);
      resolve({ status: "ok" });
    };
  });
}
