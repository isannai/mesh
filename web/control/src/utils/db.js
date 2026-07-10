// Shared IndexedDB opener for iann-broker.
//
// All stores (custom_models, my_nodes) share a single database so that
// version upgrades are handled in one place.

const DB_NAME = "iann-broker";
const DB_VERSION = 2;

export function openDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = (e) => {
      const db = req.result;
      // v1: custom_models store
      if (e.oldVersion < 1) {
        const store = db.createObjectStore("custom_models", {
          keyPath: "id",
          autoIncrement: true,
        });
        store.createIndex("name", "name", { unique: true });
      }
      // v2: my_nodes store
      if (e.oldVersion < 2) {
        db.createObjectStore("my_nodes", { keyPath: "id" });
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}
