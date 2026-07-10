// Per-card UI visibility config — owner can disable individual workspace
// cards / sidebar entries via /v1/admin/cards. Public read at /v1/cards.

import { getAuthHeaders } from "@utils/wallet";

/** Fetch card visibility map. Returns { [cardId]: { enabled: bool } }. */
export async function fetchCards() {
  try {
    const resp = await fetch("/v1/cards");
    if (!resp.ok) return {};
    const data = await resp.json();
    return (data && data.cards) || {};
  } catch {
    return {};
  }
}

/** Owner-only — update the entire card map. */
export async function updateCards(cards) {
  const resp = await fetch("/v1/admin/cards", {
    method: "PUT",
    headers: { "Content-Type": "application/json", ...getAuthHeaders() },
    body: JSON.stringify({ cards }),
  });
  if (!resp.ok) throw new Error("updateCards: HTTP " + resp.status);
  return resp.json();
}

/** Returns true if the card should be shown given the current config map.
 *  Missing key in config = enabled (default).
 */
export function isCardEnabled(cardId, cardsConfig) {
  const e = cardsConfig?.[cardId];
  if (!e) return true;
  return e.enabled !== false;
}
