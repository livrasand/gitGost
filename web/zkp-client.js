/* gitGost ZKP Auth Client
   Schnorr ZKP on P256 (secp256r1) — pure JS BigInt implementation.
   Matches internal/zkp/schnorr.go and internal/http/zkp.go. */

/* ── Deterministic "beam" avatar generator (ported from boring-avatars) ──
   Same algorithm as the profile.html inline copy; kept global so any page
   loading this script can render an identity's avatar without a network call. */
(function () {
  var BEAM_SIZE = 36;
  var BEAM_COLORS = ['#92A1C6', '#146A7C', '#F0AB3D', '#C271B4', '#C20D90'];

  function hashCode(str) {
    var hash = 0;
    for (var i = 0; i < str.length; i++) {
      hash = (hash << 5) - hash + str.charCodeAt(i);
      hash = hash & hash;
    }
    return Math.abs(hash);
  }
  function getUnit(n, range, index) {
    var value = n % range;
    if (index && (n % (index * range)) / index < range) return -value;
    return value;
  }
  function getBoolean(n, n2) { return !!(n % n2); }
  function pickColor(n) { return BEAM_COLORS[n % BEAM_COLORS.length]; }
  function getContrast(hex) {
    var r = parseInt(hex.slice(1, 3), 16);
    var g = parseInt(hex.slice(3, 5), 16);
    var b = parseInt(hex.slice(5, 7), 16);
    var yiq = (r * 299 + g * 587 + b * 114) / 1000;
    return yiq >= 128 ? '#000000' : '#ffffff';
  }

  function beamAvatarSvg(name, size) {
    var n = hashCode(String(name || ''));
    var wrapperColor = pickColor(n);
    var faceColor = getContrast(wrapperColor);
    var backgroundColor = pickColor(n + 13);
    var preTX = getUnit(n, 10, 1);
    var wrapperTranslateX = preTX < 5 ? preTX + BEAM_SIZE / 9 : preTX;
    var preTY = getUnit(n, 10, 2);
    var wrapperTranslateY = preTY < 5 ? preTY + BEAM_SIZE / 9 : preTY;
    var wrapperRotate = getUnit(n, 360);
    var wrapperScale = 1 + getUnit(n, BEAM_SIZE / 12) / 10;
    var isMouthOpen = getBoolean(n, 2);
    var isCircle = getBoolean(n, 1);
    var eyeSpread = getUnit(n, 5);
    var mouthSpread = getUnit(n, 3);
    var faceRotate = getUnit(n, 10, 3);
    var faceTranslateX = wrapperTranslateX > BEAM_SIZE / 6 ? wrapperTranslateX / 2 : getUnit(n, 8, 1);
    var faceTranslateY = wrapperTranslateY > BEAM_SIZE / 6 ? wrapperTranslateY / 2 : getUnit(n, 7, 2);
    var maskId = 'beam' + n;
    var mouth = isMouthOpen
      ? '<path d="M15 ' + (19 + mouthSpread) + 'c2 1 4 1 6 0" stroke="' + faceColor + '" fill="none" stroke-linecap="round"/>'
      : '<path d="M13,' + (19 + mouthSpread) + ' a1,0.75 0 0,0 10,0" fill="' + faceColor + '"/>';
    var s = size || BEAM_SIZE;
    return '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ' + BEAM_SIZE + ' ' + BEAM_SIZE + '" width="' + s + '" height="' + s + '" role="img" aria-label="' + String(name || '').replace(/"/g, '&quot;') + '">' +
      '<mask id="' + maskId + '" maskUnits="userSpaceOnUse" x="0" y="0" width="' + BEAM_SIZE + '" height="' + BEAM_SIZE + '">' +
      '<rect width="' + BEAM_SIZE + '" height="' + BEAM_SIZE + '" rx="' + (BEAM_SIZE * 2) + '" fill="#FFFFFF"/>' +
      '</mask>' +
      '<g mask="url(#' + maskId + ')">' +
      '<rect width="' + BEAM_SIZE + '" height="' + BEAM_SIZE + '" fill="' + backgroundColor + '"/>' +
      '<rect x="0" y="0" width="' + BEAM_SIZE + '" height="' + BEAM_SIZE + '" transform="translate(' + wrapperTranslateX + ' ' + wrapperTranslateY + ') rotate(' + wrapperRotate + ' ' + (BEAM_SIZE / 2) + ' ' + (BEAM_SIZE / 2) + ') scale(' + wrapperScale + ')" fill="' + wrapperColor + '" rx="' + (isCircle ? BEAM_SIZE : BEAM_SIZE / 6) + '"/>' +
      '<g transform="translate(' + faceTranslateX + ' ' + faceTranslateY + ') rotate(' + faceRotate + ' ' + (BEAM_SIZE / 2) + ' ' + (BEAM_SIZE / 2) + ')">' +
      mouth +
      '<rect x="' + (14 - eyeSpread) + '" y="14" width="1.5" height="2" rx="1" stroke="none" fill="' + faceColor + '"/>' +
      '<rect x="' + (20 + eyeSpread) + '" y="14" width="1.5" height="2" rx="1" stroke="none" fill="' + faceColor + '"/>' +
      '</g></g></svg>';
  }

  function beamAvatarDataUri(name, size) {
    return 'data:image/svg+xml,' + encodeURIComponent(beamAvatarSvg(name, size));
  }

  window.GitGostAvatar = { svg: beamAvatarSvg, dataUri: beamAvatarDataUri };
})();

(function () {
  'use strict';

  /* ── P256 curve parameters ─────────────────────────────────────── */
  const P = BigInt('0xFFFFFFFF00000001000000000000000000000000FFFFFFFFFFFFFFFFFFFFFFFF');
  const N = BigInt('0xFFFFFFFF00000000FFFFFFFFFFFFFFFFBCE6FAADA7179E84F3B9CAC2FC632551');
  const GX = BigInt('0x6B17D1F2E12C4247F8BCE6E563A440F277037D812DEB33A0F4A13945D898C296');
  const GY = BigInt('0x4FE342E2FE1A7F9B8EE7EB4A7C0F9E162BCE33576B315ECECBB6406837BF51F5');
  const A = P - 3n; // a = -3 mod p
  const G = { x: GX, y: GY };
  const INFINITY = null;

  /* ── Field arithmetic (mod p) ──────────────────────────────────── */
  function mod(a, m) { return ((a % m) + m) % m; }
  function fieldAdd(a, b) { return mod(a + b, P); }
  function fieldSub(a, b) { return mod(a - b, P); }
  function fieldMul(a, b) { return mod(a * b, P); }

  function fieldInv(a) {
    a = mod(a, P);
    if (a === 0n) throw new Error('division by zero');
    // Fermat: a^(p-2) mod p
    let result = 1n;
    let base = a;
    let exp = P - 2n;
    while (exp > 0n) {
      if (exp & 1n) result = mod(result * base, P);
      base = mod(base * base, P);
      exp >>= 1n;
    }
    return result;
  }

  /* ── Point operations (affine coordinates) ─────────────────────── */
  function pointEq(a, b) {
    if (!a && !b) return true;
    if (!a || !b) return false;
    return a.x === b.x && a.y === b.y;
  }

  function pointAdd(p1, p2) {
    if (!p1) return p2;
    if (!p2) return p1;
    if (p1.x === p2.x) {
      if (mod(p1.y + p2.y, P) === 0n) return INFINITY; // p + (-p)
      return pointDouble(p1);
    }
    const lam = fieldMul(fieldSub(p2.y, p1.y), fieldInv(fieldSub(p2.x, p1.x)));
    const rx = fieldSub(fieldSub(fieldMul(lam, lam), p1.x), p2.x);
    const ry = fieldSub(fieldMul(lam, fieldSub(p1.x, rx)), p1.y);
    return { x: rx, y: ry };
  }

  function pointDouble(p) {
    if (!p) return INFINITY;
    if (p.y === 0n) return INFINITY;
    const lam = fieldMul(fieldAdd(fieldMul(3n, fieldMul(p.x, p.x)), A), fieldInv(fieldMul(2n, p.y)));
    const rx = fieldSub(fieldSub(fieldMul(lam, lam), p.x), p.x);
    const ry = fieldSub(fieldMul(lam, fieldSub(p.x, rx)), p.y);
    return { x: rx, y: ry };
  }

  function scalarMul(k, point) {
    if (k === 0n || !point) return INFINITY;
    let result = INFINITY;
    let addend = point;
    let s = k;
    while (s > 0n) {
      if (s & 1n) result = result ? pointAdd(result, addend) : addend;
      addend = pointDouble(addend);
      s >>= 1n;
    }
    return result;
  }

  /* ── Byte / BigInt conversion helpers ──────────────────────────── */
  function bigIntToBytes(n) {
    if (n === 0n) return new Uint8Array(0);
    const hex = n.toString(16);
    const padded = hex.length % 2 ? '0' + hex : hex;
    const bytes = new Uint8Array(padded.length / 2);
    for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(padded.substr(i * 2, 2), 16);
    return bytes;
  }

  function bigIntToPaddedBytes(n, len) {
    const raw = bigIntToBytes(n);
    if (raw.length >= len) return raw.slice(raw.length - len);
    const padded = new Uint8Array(len);
    padded.set(raw, len - raw.length);
    return padded;
  }

  function bytesToBigInt(bytes) {
    let hex = '';
    for (let i = 0; i < bytes.length; i++) hex += bytes[i].toString(16).padStart(2, '0');
    return hex ? BigInt('0x' + hex) : 0n;
  }

  /* ── Point serialization (uncompressed: 04 || X || Y, each 32 bytes) ─ */
  function pointToBytes(pt) {
    const out = new Uint8Array(65);
    out[0] = 0x04;
    out.set(bigIntToPaddedBytes(pt.x, 32), 1);
    out.set(bigIntToPaddedBytes(pt.y, 32), 33);
    return out;
  }

  function bytesToPoint(bytes) {
    if (bytes[0] !== 0x04 || bytes.length !== 65) return null;
    const x = bytesToBigInt(bytes.slice(1, 33));
    const y = bytesToBigInt(bytes.slice(33, 65));
    // Validate on curve: y^2 ≡ x^3 + ax + b (mod p)
    const lhs = fieldMul(y, y);
    const rhs = fieldAdd(fieldAdd(fieldMul(fieldMul(x, x), x), fieldMul(A, x)), B_P256);
    if (lhs !== rhs) return null;
    return { x, y };
  }

  // Precompute b = G_y^2 - G_x^3 - a*G_x (mod p) for curve validation
  const B_P256 = mod(fieldMul(GY, GY) - fieldMul(fieldMul(GX, GX), GX) - fieldMul(A, GX), P);

  /* ── Base64url encode / decode (no padding) ────────────────────── */
  function base64urlEncode(bytes) {
    let binary = '';
    for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function base64urlDecode(str) {
    str = str.replace(/-/g, '+').replace(/_/g, '/');
    while (str.length % 4) str += '=';
    const binary = atob(str);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes;
  }

  /* ── SHA-256 via SubtleCrypto ──────────────────────────────────── */
  async function sha256(data) {
    const hash = await crypto.subtle.digest('SHA-256', data);
    return new Uint8Array(hash);
  }

  /* ── BIP39 recovery phrase (12 words, 128-bit entropy) ─────────── */
  function bytesToBits(bytes) {
    let bits = '';
    for (let i = 0; i < bytes.length; i++) bits += bytes[i].toString(2).padStart(8, '0');
    return bits;
  }

  function bitsToBytes(bits) {
    const bytes = new Uint8Array(Math.ceil(bits.length / 8));
    for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt((bits.substr(i * 8, 8) + '00000000').substr(0, 8), 2);
    return bytes;
  }

  async function generateMnemonic() {
    const words = window.GITGOST_BIP39_EN;
    if (!words || !Array.isArray(words) || words.length !== 2048) throw new Error('BIP39 wordlist not loaded');
    const entropy = new Uint8Array(16); // 128 bits
    crypto.getRandomValues(entropy);
    const hash = await sha256(entropy);
    const bits = bytesToBits(entropy) + bytesToBits(hash).substr(0, 4); // 128 + 4 checksum = 132
    const out = [];
    for (let i = 0; i < 12; i++) out.push(words[parseInt(bits.substr(i * 11, 11), 2)]);
    return out.join(' ');
  }

  async function validateMnemonic(phrase) {
    const words = window.GITGOST_BIP39_EN;
    if (!words || !Array.isArray(words) || words.length !== 2048) return false;
    if (typeof phrase !== 'string') return false;
    const list = phrase.trim().toLowerCase().split(/\s+/);
    if (list.length !== 12) return false;
    const indexMap = new Map();
    for (let i = 0; i < words.length; i++) indexMap.set(words[i], i);
    let bits = '';
    for (const w of list) {
      const idx = indexMap.get(w);
      if (idx === undefined) return false;
      bits += idx.toString(2).padStart(11, '0');
    }
    const entropyBits = bits.substr(0, 128);
    const csBits = bits.substr(128, 4);
    const hash = await sha256(bitsToBytes(entropyBits));
    return bytesToBits(hash).substr(0, 4) === csBits;
  }

  /* ── PBKDF2-HMAC-SHA512 seed derivation ────────────────────────── */
  async function pbkdf2Sha512(passwordStr, saltStr, iterations, keyLen) {
    const key = await crypto.subtle.importKey(
      'raw',
      new TextEncoder().encode(passwordStr),
      'PBKDF2',
      false,
      ['deriveBits']
    );
    const bits = await crypto.subtle.deriveBits(
      { name: 'PBKDF2', hash: 'SHA-512', salt: new TextEncoder().encode(saltStr), iterations },
      key,
      keyLen * 8
    );
    return new Uint8Array(bits);
  }

  /* Recover a keypair from the phrase + identity.
     salt = "mnemonic" + "gitgost:<identity>"; seed = PBKDF2-SHA512(phrase, salt, 2048, 64). */
  async function phraseToKey(phrase, identity) {
    const normalized = String(phrase).trim().toLowerCase();
    const seed = await pbkdf2Sha512(normalized, 'mnemonicgitgost:' + identity, 2048, 64);
    const privKey = 1n + (bytesToBigInt(seed.slice(0, 32)) % (N - 1n)); // in [1, N-1]
    const pubKey = scalarMul(privKey, G);
    if (!pubKey) throw new Error('degenerate key');
    return { privateKey: privKey, publicKey: pubKey, publicKeyBytes: pointToBytes(pubKey) };
  }

  /* ── Key generation ────────────────────────────────────────────── */
  function generateKey() {
    const privBytes = new Uint8Array(32);
    crypto.getRandomValues(privBytes);
    const privKey = bytesToBigInt(privBytes);
    const pubKey = scalarMul(privKey, G);
    if (!pubKey) throw new Error('degenerate key');
    return { privateKey: privKey, publicKey: pubKey, publicKeyBytes: pointToBytes(pubKey) };
  }

  /* ── hashToScalar: SHA-256(challenge || pubBytes || commitmentX || commitmentY) mod N ─ */
  async function hashToScalar(challengeBytes, pubBytes, commitmentX, commitmentY) {
    const cxB = bigIntToBytes(commitmentX);
    const cyB = bigIntToBytes(commitmentY);
    const total = new Uint8Array(challengeBytes.length + pubBytes.length + cxB.length + cyB.length);
    let off = 0;
    total.set(challengeBytes, off); off += challengeBytes.length;
    total.set(pubBytes, off); off += pubBytes.length;
    total.set(cxB, off); off += cxB.length;
    total.set(cyB, off);
    const hash = await sha256(total);
    return bytesToBigInt(hash) % N;
  }

  /* ── Schnorr Prove ─────────────────────────────────────────────── */
  async function prove(privateKey, challengeBytes, publicKeyBytes) {
    for (let attempt = 0; attempt < 64; attempt++) {
      // random nonce in [1, N-1]
      const nonceBytes = new Uint8Array(32);
      crypto.getRandomValues(nonceBytes);
      const nonce = (bytesToBigInt(nonceBytes) % (N - 1n)) + 1n;
      const commitment = scalarMul(nonce, G);
      if (!commitment) continue;
      const c = await hashToScalar(challengeBytes, publicKeyBytes, commitment.x, commitment.y);
      const response = (c * privateKey + nonce) % N;
      // canonical: reject if response > N/2
      if (response > (N >> 1n)) continue;
      return { commitmentX: commitment.x, commitmentY: commitment.y, response };
    }
    throw new Error('failed to generate canonical proof');
  }

  /* ── API Client ────────────────────────────────────────────────── */
  const API_BASE = window.GITGOST_API || '';

  async function apiRegister(identity, publicKeyBase64url, captchaToken, website) {
    const res = await fetch(API_BASE + '/api/zkp/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ identity, public_key: publicKeyBase64url, captcha_token: captchaToken || '', website: website || '' })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'registration failed');
    return data;
  }

  async function apiGetChallenge(identity) {
    const res = await fetch(API_BASE + '/api/zkp/challenge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ identity })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'challenge failed');
    return data; // { challenge_id, challenge }
  }

  async function apiVerify(identity, challengeId, proof) {
    const res = await fetch(API_BASE + '/api/zkp/verify', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        identity,
        challenge_id: challengeId,
        commitment_x: base64urlEncode(bigIntToPaddedBytes(proof.commitmentX, 32)),
        commitment_y: base64urlEncode(bigIntToPaddedBytes(proof.commitmentY, 32)),
        response: base64urlEncode(bigIntToBytes(proof.response))
      })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'verification failed');
    return data; // { authenticated, identity, session_token }
  }

  /* ── Challenge/verify helper: proves knowledge of the private key and
     returns the server-verified session token ── */
  async function authenticateIdentity(identity, keyData) {
    const { challenge_id, challenge } = await apiGetChallenge(identity);
    const challengeBytes = base64urlDecode(challenge);
    const proof = await prove(keyData.privateKey, challengeBytes, keyData.publicKeyBytes);
    return await apiVerify(identity, challenge_id, proof);
  }

  /* ── localStorage session / key management ──────────────────────── */
  const STORAGE_PREFIX = 'zkp_';
  const IDENTITIES_KEY = STORAGE_PREFIX + 'identities';
  const SESSION_KEY = STORAGE_PREFIX + 'session';

  function getIdentities() {
    try { return JSON.parse(localStorage.getItem(IDENTITIES_KEY)) || []; }
    catch { return []; }
  }

  function saveIdentity(identity, keyData) {
    const ids = getIdentities();
    if (!ids.includes(identity)) ids.push(identity);
    localStorage.setItem(IDENTITIES_KEY, JSON.stringify(ids));
    localStorage.setItem(STORAGE_PREFIX + 'key_' + identity, JSON.stringify(keyData));
  }

  function loadKey(identity) {
    try {
      const raw = localStorage.getItem(STORAGE_PREFIX + 'key_' + identity);
      if (!raw) return null;
      const d = JSON.parse(raw);
      // Support both formats: with or without '0x' prefix
      const privStr = d.priv.startsWith('0x') ? d.priv : '0x' + d.priv;
      return { privateKey: BigInt(privStr), publicKeyBytes: base64urlDecode(d.pubB64) };
    } catch { return null; }
  }

  function removeIdentity(identity) {
    const ids = getIdentities().filter(i => i !== identity);
    localStorage.setItem(IDENTITIES_KEY, JSON.stringify(ids));
    localStorage.removeItem(STORAGE_PREFIX + 'key_' + identity);
  }

  function getSession() {
    try {
      const s = JSON.parse(localStorage.getItem(SESSION_KEY));
      if (s && s.identity && s.ts) return s;
    } catch {}
    return null;
  }

  function getSessionToken() {
    const s = getSession();
    return (s && s.session_token) ? s.session_token : null;
  }

  function setSession(identity, sessionToken) {
    localStorage.setItem(SESSION_KEY, JSON.stringify({ identity, ts: Date.now(), session_token: sessionToken || '' }));
  }

  function clearSession() {
    localStorage.removeItem(SESSION_KEY);
  }

  /* ── Auth Modal UI ─────────────────────────────────────────────── */
  function injectStyles() {
    if (document.getElementById('zkp-auth-styles')) return;
    const css = `
/* ── ZKP Auth Modal ─────────────────────────────────────── */
.zkp-overlay{position:fixed;inset:0;z-index:9999;background:rgba(0,0,0,.65);display:flex;align-items:center;justify-content:center;backdrop-filter:blur(4px)}
.zkp-modal{background:var(--bg-secondary,#161b22);border:1px solid var(--border,#30363d);border-radius:8px;width:370px;max-width:92vw;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;color:var(--fg,#c9d1d9);box-shadow:0 8px 32px rgba(0,0,0,.4)}
.zkp-modal-header{display:flex;justify-content:space-between;align-items:center;padding:.7rem 1rem;border-bottom:1px solid var(--border,#30363d);font-size:.82rem;font-weight:600}
.zkp-close{background:none;border:none;color:var(--fg-muted,#8b949e);cursor:pointer;font-size:1.2rem;line-height:1;padding:0 2px}
.zkp-close:hover{color:var(--fg,#c9d1d9)}
.zkp-tabs{display:flex;border-bottom:1px solid var(--border,#30363d)}
.zkp-tab{flex:1;padding:.5rem;background:none;border:none;color:var(--fg-muted,#8b949e);cursor:pointer;font-family:inherit;font-size:.78rem;border-bottom:2px solid transparent;transition:color .15s,border-color .15s}
.zkp-tab:hover{color:var(--fg,#c9d1d9)}
.zkp-tab.active{color:var(--accent,#58a6ff);border-bottom-color:var(--accent,#58a6ff)}
.zkp-tab-content{padding:1rem}
.zkp-field{margin-bottom:.7rem}
.zkp-field label{display:block;font-size:.72rem;color:var(--fg-muted,#8b949e);margin-bottom:.2rem;text-transform:uppercase;letter-spacing:.04em}
.zkp-field input{width:100%;padding:.4rem .55rem;background:var(--bg,#0d1117);border:1px solid var(--border,#30363d);border-radius:4px;color:var(--fg,#c9d1d9);font-family:inherit;font-size:.8rem;box-sizing:border-box}
.zkp-field input:focus{outline:none;border-color:var(--accent,#58a6ff)}
.zkp-hint{font-size:.68rem;color:var(--fg-muted,#8b949e);margin-bottom:.75rem;line-height:1.45}
.zkp-btn{width:100%;padding:.5rem;background:var(--accent,#58a6ff);color:var(--bg,#0d1117);border:none;border-radius:4px;font-family:inherit;font-size:.8rem;font-weight:600;cursor:pointer;transition:opacity .15s}
.zkp-btn:hover{opacity:.88}
.zkp-btn:disabled{opacity:.45;cursor:not-allowed}
.zkp-btn-danger{background:#f85149;color:#fff}
.zkp-btn-ghost{background:none;color:var(--fg-muted,#8b949e);border:1px solid var(--border,#30363d);font-size:.72rem;padding:.3rem .6rem;width:auto;display:inline-block}
.zkp-btn-ghost:hover{color:var(--fg,#c9d1d9);border-color:var(--fg-muted,#8b949e)}
.zkp-status{margin-top:.55rem;font-size:.7rem;min-height:1em;word-break:break-word}
.zkp-status.ok{color:#3fb950}
.zkp-status.err{color:#f85149}
.zkp-status.busy{color:var(--fg-muted,#8b949e)}
.zkp-session-bar{padding:.6rem 1rem;border-top:1px solid var(--border,#30363d);display:flex;align-items:center;justify-content:space-between}
.zkp-session-id{font-size:.78rem;color:var(--accent,#58a6ff)}
.zkp-ids-list{margin:.3rem 0 .7rem;max-height:120px;overflow-y:auto}
.zkp-id-item{display:flex;align-items:center;justify-content:space-between;padding:.25rem .4rem;border-radius:3px;font-size:.72rem;cursor:pointer;border:1px solid transparent}
.zkp-id-item:hover{background:var(--bg,#0d1117);border-color:var(--border,#30363d)}
.zkp-id-item .zkp-id-name{color:var(--fg,#c9d1d9);flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.zkp-id-item .zkp-id-del{color:var(--fg-muted,#8b949e);font-size:.65rem;padding:0 4px;opacity:0;transition:opacity .15s}
.zkp-id-item:hover .zkp-id-del{opacity:1}
.zkp-id-item .zkp-id-del:hover{color:#f85149}
/* Sidebar auth button */
.zkp-sidebar-btn{display:flex;align-items:center;gap:.4rem;padding:.35rem .5rem;margin:.8rem 0 .2rem;border-radius:4px;border:1px solid var(--border,#30363d);background:var(--bg,#0d1117);color:var(--fg-muted,#8b949e);cursor:pointer;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:.75rem;width:100%;text-align:left;transition:border-color .15s}
.zkp-sidebar-btn:hover{border-color:var(--fg-muted,#8b949e);color:var(--fg,#c9d1d9)}
.zkp-sidebar-btn .zkp-dot{width:6px;height:6px;border-radius:50%;background:var(--fg-muted,#8b949e);flex-shrink:0}
.zkp-sidebar-btn.authed .zkp-dot{background:#3fb950}
.zkp-statusbar-session{display:flex;align-items:center;gap:.5rem;margin-right:.375rem;color:var(--fg-muted,#8b949e);text-decoration:none;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:.75rem;white-space:nowrap;transition:color .2s}
.zkp-statusbar-session:hover{color:var(--fg,#c9d1d9);text-decoration:underline}
.zkp-statusbar-avatar{display:inline-flex;align-items:center;justify-content:center;width:16px;height:16px;border-radius:50%;flex-shrink:0;background:var(--accent,#58a6ff);color:var(--bg,#0d1117);font-size:.6rem;font-weight:700;line-height:1}
.zkp-statusbar-guest{display:flex;align-items:center;gap:.3rem;margin-right:.375rem;color:var(--fg-muted,#8b949e);font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:.75rem;white-space:nowrap}
.zkp-statusbar-guest button{border:0;padding:0;margin:0;background:transparent;color:var(--accent,#58a6ff);font:inherit;cursor:pointer}
.zkp-statusbar-guest button:hover{text-decoration:underline}
.zkp-actions-menu{position:relative;margin-right:.5rem;display:flex;align-items:center}
.zkp-actions-dropdown{position:absolute;bottom:calc(100% + .4rem);right:0;min-width:150px;background:var(--bg,#fff);border:1px solid var(--border,#30363d);border-radius:4px;box-shadow:0 8px 24px rgb(0 0 0 / 18%);padding:.4rem 0;z-index:1001}
.zkp-actions-dropdown[hidden]{display:none}
.zkp-actions-dropdown-title{font-weight:700;font-size:.72rem;text-align:center;padding-bottom:.4rem;margin-bottom:.25rem;border-bottom:1px solid var(--border,#30363d);color:var(--fg,#c9d1d9)}
.zkp-actions-dropdown a{display:block;text-align:center;padding:.3rem .7rem;font-size:.72rem;color:var(--fg-muted,#8b949e);text-decoration:none}
.zkp-actions-dropdown a:hover{color:var(--fg,#c9d1d9);background:var(--bg-hover,#ebebeb)}
.command-statusbar{position:fixed!important;top:auto!important;right:0;bottom:0;left:0;z-index:1000;padding:.35rem .75rem!important;background:var(--bg-secondary,#f5f5f5)!important;border-top:1px solid var(--border,#30363d);border-bottom:0!important}
.command-statusbar .statusbar-left{position:relative}
.command-statusbar .statusbar-command-trigger{border:0;padding:0;color:var(--fg-muted,#8b949e);background:transparent;font:inherit;cursor:pointer}
.command-statusbar .statusbar-command-trigger:hover,.command-statusbar .statusbar-command-trigger:focus-visible{color:var(--fg,#c9d1d9);outline:none}
.command-statusbar .statusbar-command-menu{position:absolute;bottom:calc(100% + .25rem);left:0;width:14rem;padding:.25rem 0;border:1px solid var(--border,#30363d);background:var(--bg,#fff);box-shadow:0 8px 24px rgb(0 0 0 / 18%)}
.command-statusbar .statusbar-command-menu[hidden]{display:none}
.command-statusbar .statusbar-command-menu a,.command-statusbar .statusbar-command-menu button{display:flex;align-items:center;width:100%;border:0;padding:.4rem .75rem;color:var(--fg,#c9d1d9);background:transparent;font:inherit;font-size:.72rem;text-align:left;text-decoration:none;cursor:pointer}
.command-statusbar .statusbar-command-menu a[hidden],.command-statusbar .statusbar-command-menu button[hidden]{display:none}
.command-statusbar .statusbar-command-menu a:hover,.command-statusbar .statusbar-command-menu button:hover,.command-statusbar .statusbar-command-menu a:focus-visible,.command-statusbar .statusbar-command-menu button:focus-visible{background:var(--bg-hover,#ebebeb);outline:none}
.command-statusbar .statusbar-command-menu-divider{height:1px;margin:.25rem 0;background:var(--border,#30363d)}
.zkp-phrase{display:grid;grid-template-columns:repeat(3,1fr);gap:.35rem;margin:.5rem 0}
.zkp-phrase .zkp-word{display:flex;align-items:center;gap:.35rem;background:var(--bg-hover,#161b22);border:1px solid var(--border,#30363d);border-radius:4px;padding:.3rem .45rem;font-size:.72rem}
.zkp-phrase .zkp-word i{font-style:normal;color:var(--fg-muted,#8b949e);min-width:1.1rem;text-align:right;font-size:.62rem}
.zkp-field textarea{width:100%;box-sizing:border-box;background:var(--bg,#0d1117);color:var(--fg,#e6edf3);border:1px solid var(--border,#30363d);border-radius:4px;padding:.45rem .5rem;font-size:.78rem;font-family:inherit}
.zkp-check{display:flex;align-items:center;gap:.45rem;margin:.65rem 0 .4rem;font-size:.72rem;color:var(--fg-muted,#8b949e);cursor:pointer}
.zkp-check input{accent-color:var(--accent,#2da44e);cursor:pointer}
.zkp-phrase-actions{display:flex;gap:.4rem;flex-wrap:wrap;margin:.4rem 0}
.zkp-captcha{margin:.6rem 0}
.zkp-reg-notice{font-size:.68rem;color:var(--fg-muted,#8b949e);margin:.4rem 0;line-height:1.45}
`;
    const style = document.createElement('style');
    style.id = 'zkp-auth-styles';
    style.textContent = css;
    document.head.appendChild(style);
  }

  let pendingReg = null;

  function createModal() {
    if (document.getElementById('zkp-overlay')) return;
    const overlay = document.createElement('div');
    overlay.id = 'zkp-overlay';
    overlay.className = 'zkp-overlay';
    overlay.style.display = 'none';
    overlay.innerHTML = `
<div class="zkp-modal" role="dialog" aria-modal="true" aria-label="ZKP Authentication">
  <div class="zkp-modal-header">
    <span>zero-knowledge auth</span>
    <button class="zkp-close" id="zkp-close" aria-label="Close">&times;</button>
  </div>
  <div class="zkp-tabs">
    <button class="zkp-tab active" data-zkp-tab="register">register</button>
    <button class="zkp-tab" data-zkp-tab="login">login</button>
    <button class="zkp-tab" data-zkp-tab="recover">recover</button>
  </div>
  <!-- Register -->
  <div class="zkp-tab-content" data-zkp-panel="register">
    <div class="zkp-field"><label>identity</label>
      <input type="text" id="zkp-reg-identity" placeholder="pick a username" maxlength="128" autocomplete="off" spellcheck="false">
    </div>
    <p class="zkp-hint">generates a 12-word recovery phrase and a keypair stored locally in this browser. no password needed — keep the phrase secret; it is the only way to log in again on a new device.</p>
    <button class="zkp-btn" id="zkp-reg-btn">generate &amp; register</button>
    <div class="zkp-status" id="zkp-reg-status"></div>
    <div id="zkp-reg-phrase" style="display:none">
      <p class="zkp-hint">your recovery phrase — write it down now, you will only see it once:</p>
      <div class="zkp-phrase" id="zkp-reg-phrase-words"></div>
      <div class="zkp-phrase-actions">
        <button class="zkp-btn-ghost" id="zkp-reg-copy" type="button">copy phrase</button>
        <button class="zkp-btn-ghost" id="zkp-reg-download" type="button">save as .txt</button>
      </div>
      <div class="zkp-captcha">
        <menta-widget id="zkp-reg-captcha" data-cap-api-endpoint="${API_BASE || ''}/api/captcha" data-cap-i18n-initial-state="I'm not a robot"></menta-widget>
      </div>
      <input type="text" id="zkp-reg-website" tabindex="-1" autocomplete="off" aria-hidden="true" style="position:absolute;left:-9999px;width:1px;height:1px;opacity:0">
      <label class="zkp-check"><input type="checkbox" id="zkp-reg-confirm"> I have stored my recovery phrase somewhere safe</label>
      <p class="zkp-reg-notice">gitgost forge users only. registrations without a node are removed from the server after 7 days.</p>
      <button class="zkp-btn" id="zkp-reg-continue" disabled>I understand — finish registration</button>
    </div>
  </div>
  <!-- Login -->
  <div class="zkp-tab-content" data-zkp-panel="login" style="display:none">
    <div class="zkp-field"><label>identity</label>
      <input type="text" id="zkp-login-identity" placeholder="your identity" maxlength="128" autocomplete="off" spellcheck="false">
    </div>
    <p class="zkp-hint">stored identities:</p>
    <div class="zkp-ids-list" id="zkp-ids-list"></div>
    <button class="zkp-btn" id="zkp-login-btn">authenticate</button>
    <div class="zkp-status" id="zkp-login-status"></div>
  </div>
  <!-- Recover -->
  <div class="zkp-tab-content" data-zkp-panel="recover" style="display:none">
    <div class="zkp-field"><label>identity</label>
      <input type="text" id="zkp-rec-identity" placeholder="the identity you registered" maxlength="128" autocomplete="off" spellcheck="false">
    </div>
    <div class="zkp-field"><label>12-word recovery phrase</label>
      <textarea id="zkp-rec-phrase" rows="3" placeholder="abandon ability able about above absent …" autocomplete="off" spellcheck="false"></textarea>
    </div>
    <p class="zkp-hint">restores the keypair for this identity on this device so you can log in again.</p>
    <button class="zkp-btn" id="zkp-rec-btn">restore &amp; authenticate</button>
    <div class="zkp-status" id="zkp-recover-status"></div>
  </div>
  <!-- Session bar (shown when authed) -->
  <div class="zkp-session-bar" id="zkp-session-bar" style="display:none">
    <span class="zkp-session-id" id="zkp-session-identity"></span>
    <button class="zkp-btn-ghost" id="zkp-logout-btn">logout</button>
  </div>
</div>`;
    document.body.appendChild(overlay);
    bindModalEvents(overlay);
  }

  function bindModalEvents(overlay) {
    // Tabs
    overlay.querySelectorAll('.zkp-tab').forEach(tab => {
      tab.addEventListener('click', () => {
        overlay.querySelectorAll('.zkp-tab').forEach(t => t.classList.remove('active'));
        tab.classList.add('active');
        const name = tab.dataset.zkpTab;
        overlay.querySelectorAll('[data-zkp-panel]').forEach(p => p.style.display = 'none');
        overlay.querySelector(`[data-zkp-panel="${name}"]`).style.display = '';
      });
    });
    // Close
    overlay.querySelector('#zkp-close').addEventListener('click', closeModal);
    overlay.addEventListener('click', e => { if (e.target === overlay) closeModal(); });
    document.addEventListener('keydown', e => { if (e.key === 'Escape') closeModal(); });
    // Register
    overlay.querySelector('#zkp-reg-btn').addEventListener('click', doRegister);
    // Register phrase step
    overlay.querySelector('#zkp-reg-confirm').addEventListener('change', e => {
      document.getElementById('zkp-reg-continue').disabled = !e.target.checked;
    });
    overlay.querySelector('#zkp-reg-continue').addEventListener('click', confirmRegPhrase);
    overlay.querySelector('#zkp-reg-copy').addEventListener('click', copyRegPhrase);
    overlay.querySelector('#zkp-reg-download').addEventListener('click', downloadRegPhrase);
    // Login
    overlay.querySelector('#zkp-login-btn').addEventListener('click', doLogin);
    // Recover
    overlay.querySelector('#zkp-rec-btn').addEventListener('click', doRecover);
    // Logout
    overlay.querySelector('#zkp-logout-btn').addEventListener('click', doLogout);
  }

  function openModal() {
    createModal();
    const overlay = document.getElementById('zkp-overlay');
    overlay.style.display = '';
    if (pendingReg) showRegPhrase(pendingReg.mnemonic, pendingReg.identity);
    refreshSessionUI();
    refreshIdsList();
  }

  function closeModal() {
    const overlay = document.getElementById('zkp-overlay');
    if (overlay) overlay.style.display = 'none';
  }

  function setStatus(panel, cls, msg) {
    const el = document.getElementById('zkp-' + panel + '-status');
    if (!el) return;
    el.className = 'zkp-status ' + cls;
    el.textContent = msg;
  }

  function refreshIdsList() {
    const list = document.getElementById('zkp-ids-list');
    if (!list) return;
    const ids = getIdentities();
    const session = getSession();
    if (ids.length === 0) {
      list.innerHTML = '<div style="font-size:.68rem;color:var(--fg-muted,#8b949e);padding:.2rem .4rem">none yet</div>';
      return;
    }
    list.innerHTML = '';
    ids.forEach(id => {
      const div = document.createElement('div');
      div.className = 'zkp-id-item';
      div.innerHTML = `<span class="zkp-id-name">${escHtml(id)}</span><span class="zkp-id-del" data-del="${escHtml(id)}">&times;</span>`;
      div.addEventListener('click', e => {
        if (e.target.dataset.del) {
          const target = e.target.dataset.del;
          if (!window.confirm('Remove "' + target + '" from this browser? Unless you saved its 12-word recovery phrase you will never be able to log in as this identity again.')) return;
          removeIdentity(target);
          refreshIdsList();
          return;
        }
        document.getElementById('zkp-login-identity').value = id;
      });
      list.appendChild(div);
    });
  }

  function refreshSessionUI() {
    const session = getSession();
    const bar = document.getElementById('zkp-session-bar');
    const identEl = document.getElementById('zkp-session-identity');
    const loginPanel = document.querySelector('[data-zkp-panel="login"]');
    const regPanel = document.querySelector('[data-zkp-panel="register"]');
    if (session) {
      if (bar) bar.style.display = '';
      if (identEl) identEl.textContent = session.identity;
      // show both panels still, but session bar visible
    } else {
      if (bar) bar.style.display = 'none';
    }
    // Update sidebar buttons
    document.querySelectorAll('.zkp-sidebar-btn').forEach(btn => {
      btn.classList.toggle('authed', !!session);
      const label = btn.querySelector('.zkp-label');
      if (label) label.textContent = session ? session.identity : 'zkp auth';
    });
    renderStatusbarSession();
  }

  async function doRegister() {
    const identity = (document.getElementById('zkp-reg-identity')?.value || '').trim();
    if (!identity) return setStatus('reg', 'err', 'enter an identity');
    if (identity.length > 128) return setStatus('reg', 'err', 'max 128 chars');
    const btn = document.getElementById('zkp-reg-btn');
    btn.disabled = true;
    setStatus('reg', 'busy', 'generating recovery phrase…');
    try {
      const mnemonic = await generateMnemonic();
      const key = await phraseToKey(mnemonic, identity);
      pendingReg = { identity, mnemonic, key };
      setStatus('reg', '', '');
      showRegPhrase(mnemonic, identity);
    } catch (e) {
      setStatus('reg', 'err', e.message || 'phrase generation failed');
    } finally {
      btn.disabled = false;
    }
  }

  function showRegPhrase(mnemonic, identity) {
    const wordsEl = document.getElementById('zkp-reg-phrase-words');
    if (wordsEl) wordsEl.innerHTML = mnemonic.split(' ').map((w, i) => `<div class="zkp-word"><i>${i + 1}</i><span>${escHtml(w)}</span></div>`).join('');
    const identityInput = document.getElementById('zkp-reg-identity');
    if (identityInput) identityInput.disabled = true;
    const confirmEl = document.getElementById('zkp-reg-confirm');
    if (confirmEl) confirmEl.checked = false;
    const continueBtn = document.getElementById('zkp-reg-continue');
    if (continueBtn) continueBtn.disabled = true;
    if (identity) setStatus('reg', 'ok', 'phrase generated for "' + identity + '" — save it now');
    const phraseEl = document.getElementById('zkp-reg-phrase');
    if (phraseEl) phraseEl.style.display = '';
  }

  function hideRegPhrase() {
    const identityInput = document.getElementById('zkp-reg-identity');
    if (identityInput) identityInput.disabled = false;
    const phraseEl = document.getElementById('zkp-reg-phrase');
    if (phraseEl) phraseEl.style.display = 'none';
    const confirmEl = document.getElementById('zkp-reg-confirm');
    if (confirmEl) confirmEl.checked = false;
    const continueBtn = document.getElementById('zkp-reg-continue');
    if (continueBtn) continueBtn.disabled = true;
    pendingReg = null;
  }

  async function confirmRegPhrase() {
    if (!pendingReg) return;
    const confirmEl = document.getElementById('zkp-reg-confirm');
    if (!confirmEl || !confirmEl.checked) return;
    const captchaToken = getRegCaptchaToken();
    if (!captchaToken) {
      setStatus('reg', 'err', 'please complete the captcha before finishing registration');
      if (confirmEl) confirmEl.checked = false;
      const continueBtn = document.getElementById('zkp-reg-continue');
      if (continueBtn) continueBtn.disabled = true;
      return;
    }
    const identity = pendingReg.identity;
    const key = pendingReg.key;
    const website = (document.getElementById('zkp-reg-website')?.value || '').trim();
    const continueBtn = document.getElementById('zkp-reg-continue');
    if (continueBtn) continueBtn.disabled = true;
    setStatus('reg', 'busy', 'registering…');
    try {
      const pubB64 = base64urlEncode(key.publicKeyBytes);
      await apiRegister(identity, pubB64, captchaToken, website);
      resetRegCaptcha();
      saveIdentity(identity, { priv: '0x' + key.privateKey.toString(16), pubB64 });
      setStatus('reg', 'busy', 'authenticating…');
      const verifyData = await authenticateIdentity(identity, { privateKey: key.privateKey, publicKeyBytes: key.publicKeyBytes });
      setSession(identity, verifyData.session_token);
      setStatus('reg', 'ok', 'registered & authenticated as ' + identity);
      hideRegPhrase();
      refreshSessionUI();
      refreshIdsList();
    } catch (e) {
      resetRegCaptcha();
      setStatus('reg', 'err', e.message || 'registration failed');
      if (confirmEl) confirmEl.checked = false;
      if (continueBtn) continueBtn.disabled = true;
    }
  }

  async function copyRegPhrase() {
    if (!pendingReg) return;
    try {
      await navigator.clipboard.writeText(pendingReg.mnemonic);
      setStatus('reg', 'ok', 'phrase copied — paste it somewhere safe');
    } catch (e) {
      setStatus('reg', 'err', 'could not copy — write it down manually');
    }
  }

  function getRegCaptchaToken() {
    const w = document.getElementById('zkp-reg-captcha');
    return w && w.token ? w.token : '';
  }

  async function resetRegCaptcha() {
    const w = document.getElementById('zkp-reg-captcha');
    if (w) {
      w.token = null;
      w.sessionId = null;
      w.sessionSignature = null;
      try { if (typeof w.render === 'function') await w.render(); } catch (e) { /* noop */ }
      try { if (typeof w.initSession === 'function') await w.initSession(); } catch (e) { /* noop */ }
    }
    const websiteInput = document.getElementById('zkp-reg-website');
    if (websiteInput) websiteInput.value = '';
  }

  function downloadRegPhrase() {
    if (!pendingReg) return;
    try {
      const safeName = (pendingReg.identity || 'identity').replace(/[^a-zA-Z0-9._-]+/g, '_').slice(0, 64);
      const blob = new Blob([pendingReg.mnemonic + '\n'], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = 'gitgost-recovery-' + safeName + '.txt';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setStatus('reg', 'ok', 'phrase saved to .txt — keep it offline');
    } catch (e) {
      setStatus('reg', 'err', 'could not save file — write the phrase down manually');
    }
  }

  async function doRecover() {
    const identity = (document.getElementById('zkp-rec-identity')?.value || '').trim();
    if (!identity) return setStatus('recover', 'err', 'enter an identity');
    const phrase = (document.getElementById('zkp-rec-phrase')?.value || '').trim().toLowerCase().split(/\s+/).join(' ');
    if (!phrase) return setStatus('recover', 'err', 'enter your 12-word recovery phrase');
    const btn = document.getElementById('zkp-rec-btn');
    btn.disabled = true;
    setStatus('recover', 'busy', 'recovering…');
    try {
      if (!(await validateMnemonic(phrase))) return setStatus('recover', 'err', 'invalid recovery phrase — check the words and order');
      const key = await phraseToKey(phrase, identity);
      const pubB64 = base64urlEncode(key.publicKeyBytes);
      saveIdentity(identity, { priv: '0x' + key.privateKey.toString(16), pubB64 });
      setStatus('recover', 'busy', 'authenticating…');
      const verifyData = await authenticateIdentity(identity, { privateKey: key.privateKey, publicKeyBytes: key.publicKeyBytes });
      setSession(identity, verifyData.session_token);
      setStatus('recover', 'ok', 'restored & authenticated as ' + identity);
      refreshSessionUI();
      refreshIdsList();
    } catch (e) {
      setStatus('recover', 'err', e.message || 'recovery failed');
    } finally {
      btn.disabled = false;
    }
  }

  async function doLogin() {
    const identity = (document.getElementById('zkp-login-identity')?.value || '').trim();
    if (!identity) return setStatus('login', 'err', 'enter an identity');
    const stored = loadKey(identity);
    if (!stored) return setStatus('login', 'err', 'no private key for "' + identity + '" in this browser — keys never leave the device they were created on. If you saved the 12-word recovery phrase for this identity, open the "recover" tab to restore it; otherwise register a new identity instead');
    const btn = document.getElementById('zkp-login-btn');
    btn.disabled = true;
    setStatus('login', 'busy', 'requesting challenge…');
    try {
      const verifyData = await authenticateIdentity(identity, stored);
      setSession(identity, verifyData.session_token);
      setStatus('login', 'ok', 'authenticated as ' + identity);
      refreshSessionUI();
    } catch (e) {
      setStatus('login', 'err', e.message || 'authentication failed');
    } finally {
      btn.disabled = false;
    }
  }

  function doLogout() {
    clearSession();
    setStatus('login', '', '');
    setStatus('reg', '', '');
    refreshSessionUI();
  }

  function escHtml(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  /* ── Sidebar button injection ───────────────────────────────────── */
  function injectSidebarBtn(container) {
    if (!container || container.querySelector('.zkp-sidebar-btn')) return;
    const btn = document.createElement('button');
    btn.className = 'zkp-sidebar-btn';
    btn.type = 'button';
    const session = getSession();
    if (session) btn.classList.add('authed');
    btn.innerHTML = `<span class="zkp-dot"></span><span class="zkp-label">${session ? escHtml(session.identity) : 'zkp auth'}</span>`;
    btn.addEventListener('click', openModal);
    container.appendChild(btn);
  }

  /* ── Authenticated statusbar identity (for repo.html / profile.html) ── */
  function renderStatusbarSession() {
    const session = getSession();
    const containers = document.querySelectorAll('.command-statusbar .statusbar-right');
    containers.forEach(container => {
      container.querySelectorAll('.zkp-statusbar-session, .zkp-statusbar-guest, .zkp-actions-menu').forEach(el => el.remove());
      if (!session) {
        const guest = document.createElement('span');
        guest.className = 'zkp-statusbar-guest';
        const label = document.createElement('span');
        label.textContent = 'browsing as goster';
        const signIn = document.createElement('button');
        signIn.type = 'button';
        signIn.textContent = '(sign in)';
        signIn.addEventListener('click', openModal);
        guest.append(label, signIn);
        container.appendChild(guest);
        return;
      }
      const actionsMenu = document.createElement('div');
      actionsMenu.className = 'zkp-actions-menu';
      const actionsDropdown = document.createElement('div');
      actionsDropdown.className = 'zkp-actions-dropdown';
      actionsDropdown.hidden = true;
      actionsDropdown.innerHTML = `<a href="/gg/${encodeURIComponent(session.identity)}">profile</a>` +
        '<a href="#" onclick="event.preventDefault(); window.dispatchEvent(new CustomEvent(\'zkp-new-repo\'));">new repo</a>' +
        '<a href="/settings">settings</a>' +
        '<a href="#" id="zkp-dropdown-logout" onclick="event.preventDefault(); window.GitGostAuth.logout();">logout</a>';

      const link = document.createElement('a');
      link.className = 'zkp-statusbar-session';
      link.href = '/gg/' + encodeURIComponent(session.identity);
      link.setAttribute('aria-haspopup', 'true');
      link.setAttribute('aria-expanded', 'false');
      link.setAttribute('aria-label', 'Open actions for ' + session.identity);
      const label = document.createElement('span');
      label.textContent = 'logged in as ' + session.identity;
      const avatar = document.createElement('span');
      avatar.className = 'zkp-statusbar-avatar';
      avatar.setAttribute('aria-hidden', 'true');
      if (window.GitGostAvatar) {
        avatar.style.backgroundImage = 'url("' + window.GitGostAvatar.dataUri(session.identity, 32) + '")';
        avatar.style.backgroundSize = 'cover';
      } else {
        avatar.textContent = session.identity.slice(0, 1).toUpperCase();
      }
      link.append(label, avatar);

      link.addEventListener('click', function (e) {
        e.preventDefault();
        const open = actionsDropdown.hasAttribute('hidden');
        document.querySelectorAll('.zkp-actions-dropdown').forEach(d => { if (d !== actionsDropdown) d.hidden = true; });
        actionsDropdown.hidden = !open;
        link.setAttribute('aria-expanded', String(open));
      });

      actionsMenu.append(link, actionsDropdown);
      container.appendChild(actionsMenu);
    });
    document.querySelectorAll('#command-logout').forEach(logout => { logout.hidden = !session; });
    document.querySelectorAll('.command-statusbar [data-cmd="logout"]').forEach(logout => { logout.hidden = !session; });
  }

  // Delegación global: el dropdown se cierra al hacer clic fuera.
  document.addEventListener('click', function (e) {
    if (!e.target.closest('.zkp-actions-menu')) {
      document.querySelectorAll('.zkp-actions-dropdown:not([hidden])').forEach(d => { d.hidden = true; });
    }
  });

  function closeCommandMenu() {
    document.querySelectorAll('.command-statusbar .statusbar-command-menu').forEach(menu => {
      menu.hidden = true;
      const trigger = menu.parentElement?.querySelector('.statusbar-command-trigger');
      if (trigger) trigger.setAttribute('aria-expanded', 'false');
    });
  }

  function runCommand(command) {
    if (command === 'logout') {
      window.GitGostAuth?.logout();
      return;
    }
    if (typeof window.showPage === 'function') {
      window.showPage(command);
      return;
    }
    if (command === 'home') window.location.href = '/';
  }

  function initCommandStatusbars() {
    document.querySelectorAll('.command-statusbar').forEach(statusbar => {
      const trigger = statusbar.querySelector('.statusbar-command-trigger');
      const menu = statusbar.querySelector('.statusbar-command-menu');
      if (!trigger || !menu || trigger.dataset.bound) return;
      trigger.dataset.bound = 'true';
      trigger.addEventListener('click', event => {
        event.stopPropagation();
        const open = menu.hidden;
        menu.hidden = !open;
        trigger.setAttribute('aria-expanded', String(open));
      });
      menu.querySelector('[data-cmd="toggle-theme"]')?.addEventListener('click', () => {
        if (typeof window.toggleTheme === 'function') window.toggleTheme();
        menu.hidden = true;
        trigger.setAttribute('aria-expanded', 'false');
      });
      menu.querySelector('[data-cmd="logout"]')?.addEventListener('click', () => {
        runCommand('logout');
        menu.hidden = true;
        trigger.setAttribute('aria-expanded', 'false');
      });
      menu.querySelectorAll('a[data-cmd]').forEach(link => {
        link.addEventListener('click', event => {
          event.preventDefault();
          runCommand(link.dataset.cmd || '');
          closeCommandMenu();
        });
      });
      menu.addEventListener('click', event => {
        if (event.target.closest('a')) {
          menu.hidden = true;
          trigger.setAttribute('aria-expanded', 'false');
        }
      });
      document.addEventListener('click', event => {
        if (!event.target.closest('.command-statusbar')) {
          menu.hidden = true;
          trigger.setAttribute('aria-expanded', 'false');
        }
      });
      document.addEventListener('keydown', event => {
        if (event.key === 'Escape') {
          menu.hidden = true;
          trigger.setAttribute('aria-expanded', 'false');
        }
      });
    });
  }

  /* ── Profile button ─────────────────────────────────────────────── */
  function createProfileBtn() {
    const btn = document.createElement('a');
    const session = getSession();
    btn.href = session ? '/gg/' + encodeURIComponent(session.identity) : '/profile.html';
    btn.className = 'zkp-profile-btn';
    btn.id = 'zkp-profile-btn';
    btn.textContent = 'my profile';
    return btn;
  }

  /* ── Auto-init ─────────────────────────────────────────────────── */
  function init() {
    injectStyles();

    // Sidebar button (index.html)
    const sidebar = document.querySelector('.sidebar nav.nav-links, aside.sidebar nav.nav-links');
    if (sidebar) {
      injectSidebarBtn(sidebar);
      const pBtn = createProfileBtn();
      pBtn.style.cssText = 'display:flex;align-items:center;gap:6px;padding:6px 12px;margin:4px 8px;border-radius:4px;color:var(--accent,#58a6ff);text-decoration:none;font-size:.85rem;font-family:ui-monospace,SFMono-Regular,Menlo,monaco,consolas,monospace;border:1px solid var(--border,#30363d);background:var(--bg-secondary,#161b22)';
      sidebar.appendChild(pBtn);
    }

    // Statusbar button (repo.html, profile.html)
    const sbRight = document.querySelector('.statusbar-right');
    if (sbRight) {
      renderStatusbarSession();
    }
    initCommandStatusbars();

    // Listen for custom event
    window.addEventListener('zkp-open-auth', openModal);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  /* ── Public API ────────────────────────────────────────────────── */
  window.GitGostAuth = {
    open: openModal,
    close: closeModal,
    getSession,
    getSessionToken,
    getIdentities,
    logout: doLogout,
    // Expose for debugging
    _generateKey: generateKey,
    _prove: prove,
  };
})();
