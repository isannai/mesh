// IndexedDB wrapper for custom models — replaces broker's /v1/custom-models API.
//
// Record shape (matches prior REST response for drop-in replacement):
//   {
//     id: number,                   // autoIncrement
//     name: string,                  // unique within this browser (app-level check)
//     service: string,
//     download_url: string,
//     install_path: string,
//     files: string,                // JSON-stringified array (preserved for parity)
//     is_public: boolean,
//     architecture: string,
//     created_at: string,            // ISO timestamp
//     updated_at: string,
//   }
//
// Storage is per-browser — no cross-device sync. Wallet switching within the
// same browser is NOT isolated (matches prior broker-global behavior).

import { openDB } from "./db";

const STORE_NAME = "custom_models";

function tx(mode, fn) {
  return openDB().then(db => new Promise((resolve, reject) => {
    const t = db.transaction(STORE_NAME, mode);
    const store = t.objectStore(STORE_NAME);
    fn(store, resolve, reject);
    t.onerror = () => reject(t.error);
  }));
}

function nowISO() {
  return new Date().toISOString().replace("T", " ").slice(0, 19);
}

// Normalise input shape — strip out undefined fields, default empties.
function normalize(obj) {
  return {
    name: obj.name || "",
    service: obj.service || "",
    download_url: obj.download_url || "",
    install_path: obj.install_path || "ai/models",
    files: obj.files || "[]",
    is_public: !!obj.is_public,
    architecture: obj.architecture || "",
  };
}

// --- Public API (parity with former REST endpoints) ---

/** GET /v1/custom-models → [...] */
export function listCustomModels() {
  return tx("readonly", (store, resolve) => {
    const req = store.getAll();
    req.onsuccess = () => resolve(req.result || []);
  });
}

/** GET /v1/custom-models/:id → {...} */
export function getCustomModel(id) {
  return tx("readonly", (store, resolve) => {
    const req = store.get(Number(id));
    req.onsuccess = () => resolve(req.result || null);
  });
}

/** POST /v1/custom-models → {status, id} */
export function createCustomModel(payload) {
  const rec = {
    ...normalize(payload),
    created_at: nowISO(),
    updated_at: nowISO(),
  };
  return tx("readwrite", (store, resolve, reject) => {
    const req = store.add(rec);
    req.onsuccess = () => resolve({ status: "ok", id: req.result });
    req.onerror = () => {
      // Unique name collision
      reject(new Error(`name "${rec.name}" already exists`));
    };
  });
}

/** PUT /v1/custom-models/:id → {status} */
export function updateCustomModel(id, payload) {
  const nid = Number(id);
  return tx("readwrite", (store, resolve, reject) => {
    const getReq = store.get(nid);
    getReq.onsuccess = () => {
      if (!getReq.result) { reject(new Error("not found")); return; }
      const updated = {
        ...normalize(payload),
        id: nid,
        created_at: getReq.result.created_at,
        updated_at: nowISO(),
      };
      const putReq = store.put(updated);
      putReq.onsuccess = () => resolve({ status: "ok" });
      putReq.onerror = () => reject(new Error(putReq.error?.message || "update failed"));
    };
  });
}

/** DELETE /v1/custom-models/:id → {status} */
export function deleteCustomModel(id) {
  return tx("readwrite", (store, resolve) => {
    store.delete(Number(id));
    resolve({ status: "ok" });
  });
}
