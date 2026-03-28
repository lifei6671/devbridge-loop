/**
 * @typedef {{
 *   behaviorSummary: string;
 *   sanDNS: string;
 *   sanIPsAndCN: string;
 * }} ManagedCAIdentitySuggestion
 */

/**
 * @param {{
 *   controlPlaneGRPCH2ListenAddr: string;
 *   controlPlaneListenAddr: string;
 *   controlPlaneQUICListenAddr: string;
 * }} input
 * @returns {ManagedCAIdentitySuggestion}
 */
export function deriveManagedCAIdentitySuggestion(input) {
  const sanDNSSet = new Set();
  const sanIPSet = new Set();

  for (const listenAddr of [
    input.controlPlaneListenAddr,
    input.controlPlaneGRPCH2ListenAddr,
    input.controlPlaneQUICListenAddr,
  ]) {
    const host = parseManagedCAHostFromListenAddr(listenAddr);
    if (host === "") {
      continue;
    }
    if (isManagedCAUnspecifiedHost(host)) {
      continue;
    }
    if (isIPAddress(host)) {
      sanIPSet.add(normalizeIPAddress(host));
      continue;
    }
    sanDNSSet.add(host);
  }

  if (sanDNSSet.size === 0 && sanIPSet.size === 0) {
    sanDNSSet.add("localhost");
    sanIPSet.add("127.0.0.1");
  }

  const sanDNS = Array.from(sanDNSSet).sort();
  const sanIPs = Array.from(sanIPSet).sort();
  const commonName = sanDNS[0] ?? sanIPs[0] ?? "localhost";
  return {
    behaviorSummary: `SAN DNS=${sanDNS.join(", ") || "-"}；SAN IP=${sanIPs.join(", ") || "-"}；CN=${commonName}`,
    sanDNS: sanDNS.join(", ") || "-",
    sanIPsAndCN: `${sanIPs.join(", ") || "-"} / CN=${commonName}`,
  };
}

/**
 * @param {string} listenAddr
 * @returns {string}
 */
export function parseManagedCAHostFromListenAddr(listenAddr) {
  const normalizedListenAddr = listenAddr.trim();
  if (normalizedListenAddr === "" || normalizedListenAddr.startsWith(":")) {
    return "";
  }
  if (normalizedListenAddr.startsWith("[")) {
    const closingBracketIndex = normalizedListenAddr.indexOf("]");
    if (closingBracketIndex <= 1) {
      return "";
    }
    return normalizedListenAddr.slice(1, closingBracketIndex).trim();
  }

  const colonMatches = normalizedListenAddr.match(/:/g) ?? [];
  if (colonMatches.length === 0) {
    return normalizedListenAddr;
  }
  if (colonMatches.length === 1) {
    return normalizedListenAddr.slice(0, normalizedListenAddr.lastIndexOf(":")).trim();
  }
  return "";
}

/**
 * @param {string} host
 * @returns {boolean}
 */
function isIPAddress(host) {
  const normalizedHost = host.trim();
  if (normalizedHost === "") {
    return false;
  }
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(normalizedHost)) {
    return true;
  }
  return /^[0-9a-f:]+$/i.test(normalizedHost) && normalizedHost.includes(":");
}

/**
 * @param {string} host
 * @returns {string}
 */
function normalizeIPAddress(host) {
  const normalizedHost = host.trim().toLowerCase();
  if (normalizedHost === "::1") {
    return "::1";
  }
  return normalizedHost;
}

/**
 * @param {string} host
 * @returns {boolean}
 */
function isManagedCAUnspecifiedHost(host) {
  const normalizedHost = host.trim().toLowerCase();
  return normalizedHost === "" || normalizedHost === "0.0.0.0" || normalizedHost === "::" || normalizedHost === "[::]";
}
