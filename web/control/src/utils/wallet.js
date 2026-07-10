// Wallet connection and IANN signing utilities (Broker)

const AUTH_KEY = "iann_auth_broker";

/**
 * Connect to MetaMask and return the selected address.
 */
export async function connectMetaMask() {
  if (!window.ethereum) {
    throw new Error("MetaMask is not installed");
  }
  const accounts = await window.ethereum.request({ method: "eth_requestAccounts" });
  if (!accounts || accounts.length === 0) {
    throw new Error("No accounts found");
  }
  return accounts[0];
}

/**
 * Build the IANN signing message.
 * Format: {role}:{target}:{service}:{nonce}:{expiresAt}:{nodes}
 */
export function buildMessage(role = "owner", target = "control", ttlSec = 3600) {
  const expiresAt = Math.floor(Date.now() / 1000) + ttlSec;
  const message = `${role}:${target}:*:0:${expiresAt}:*`;
  return { message, expiresAt };
}

/**
 * Sign a message using MetaMask personal_sign.
 */
export async function signWithMetaMask(address, message) {
  if (!window.ethereum) {
    throw new Error("MetaMask is not installed");
  }
  const sig = await window.ethereum.request({
    method: "personal_sign",
    params: [message, address],
  });
  return sig;
}

/**
 * Save auth session to localStorage.
 */
export function saveSession(address, message, sig, expiresAt) {
  const data = { address, message, sig, expiresAt };
  localStorage.setItem(AUTH_KEY, JSON.stringify(data));
  return data;
}

/**
 * Load auth session from localStorage.
 * Returns null if no session or expired.
 */
export function loadSession() {
  try {
    const raw = localStorage.getItem(AUTH_KEY);
    if (!raw) return null;
    const data = JSON.parse(raw);
    if (!data?.sig || !data?.expiresAt) return null;
    if (Date.now() / 1000 > data.expiresAt) {
      localStorage.removeItem(AUTH_KEY);
      return null;
    }
    return data;
  } catch {
    return null;
  }
}

/**
 * Clear auth session.
 */
export function clearSession() {
  localStorage.removeItem(AUTH_KEY);
}

/**
 * Get auth headers for API requests.
 */
export function getAuthHeaders() {
  const session = loadSession();
  if (!session) return {};
  return {
    Authorization: "ISANN " + session.sig,
    "X-ISANN-Message": session.message,
  };
}

/**
 * Truncate address for display: 0xB171...0BcF
 */
export function truncateAddress(addr) {
  if (!addr || addr.length <= 10) return addr || "";
  return addr.slice(0, 6) + "..." + addr.slice(-4);
}
