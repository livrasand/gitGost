#pragma once
// Node Protocol — WebSocket client for the gitGost server.
// Implements: pair_request, hello (signed), heartbeat, repo_list push,
//             and command dispatch (resp/ack/fs_data).

#include <WiFi.h>
#include <ArduinoJson.h>
#include <WebSocketsClient.h>
#include <base64.h>
#include <mbedtls/base64.h>
#include "config.h"
#include "ch375b.h"
#include "storage.h"
#include "provision.h"

// Ed25519 via Monocypher (lightweight, no mbedtls dependency)
extern "C" {
#include <monocypher.h>
}

class NodeProtocol {
public:
    bool     paired  = false;
    bool     online  = false;
    String   token;
    uint64_t uptimeStart = 0;

    // Pending RPC response (filled by dispatch, read by caller)
    JsonDocument lastResp;
    bool         respReady = false;

    void begin() {
        uptimeStart = millis();
        if (provision.configured && !provision.authToken.isEmpty()) {
            token = provision.authToken;
            paired = true;
        }
    }

    void connect() {
        if (!provision.configured) return;
        String fullPath = String(provision.serverWsPath) + "?id=" + provision.nodeId;
        ws.begin(
            provision.serverHost.c_str(),
            provision.serverPort,
            fullPath.c_str()
        );
        ws.onEvent([this](WStype_t type, uint8_t* payload, size_t len) {
            this->onWS(type, payload, len);
        });
        ws.setReconnectInterval(RECONNECT_DELAY_MS);
        log_i("WS connecting to %s:%d%s", provision.serverHost.c_str(), provision.serverPort, fullPath.c_str());
    }

    void loop() {
        ws.loop();
        // Send heartbeat on interval
        static uint32_t lastHB = 0;
        if (online && millis() - lastHB > HEARTBEAT_INTERVAL_MS) {
            lastHB = millis();
            sendHeartbeat();
        }
        // While connected but not yet paired, keep (re)sending pair_request so
        // the server-side code stays fresh (its TTL is refreshed on every
        // register) and pairing completes whenever the user enters the code —
        // no need to catch the very first attempt.
        static uint32_t lastPair = 0;
        if (online && !paired && token.isEmpty() && provision.configured) {
            if (millis() - lastPair > PAIR_RETRY_MS) {
                lastPair = millis();
                ensurePairCode();
                sendPairRequest(provision.pairCode.c_str());
            }
        }
    }

    // Guarantees a (stable) pairing code exists before requesting pairing.
    void ensurePairCode() {
        if (provision.pairCode.isEmpty()) {
            provision.pairCode = Provision::genPairCode();
            provision.savePairCode(provision.pairCode);
        }
    }

    // ── Outgoing messages ─────────────────────────────

    void sendPairRequest(const char* code) {
        if (!provision.configured) return;
        JsonDocument doc;
        doc["type"]   = "pair_request";
        doc["code"]   = code;
        doc["name"]   = provision.nodeName;
        doc["pubkey"] = provision.pubkeyHex;
        doc["ts"]     = (double)millis();
        signDoc(doc, "pair");
        String msg = doc.as<String>();
        ws.sendTXT(msg);
        log_i("pair_request sent (code=%s)", code);
    }

    void sendHello() {
        if (!provision.configured) return;
        JsonDocument doc;
        doc["type"]   = "hello";
        doc["token"]  = token;
        doc["name"]   = provision.nodeName;
        doc["fw"]     = "1.0.0";
        doc["pubkey"] = provision.pubkeyHex;
        doc["ts"]     = (double)millis();
        signDoc(doc, "hello");
        String msg = doc.as<String>();
        ws.sendTXT(msg);
        log_i("hello sent");
    }

    void sendHeartbeat() {
        JsonDocument doc;
        doc["type"]          = "heartbeat";
        doc["uptime_s"]      = (millis() - uptimeStart) / 1000;
        doc["storage_free"]  = storage.freeBytes();
        doc["storage_total"] = storage.totalBytes();
        doc["storage_status"] = storage.getStatusString();
        doc["ram_total"]     = ESP.getHeapSize();
        doc["heap_free"]     = ESP.getFreeHeap();
        doc["wifi_rssi"]     = WiFi.RSSI();
        
        // Report individual storage medium status
        doc["sd_ok"] = storage.sdOk;
        doc["usb_ok"] = storage.usbOk;
        doc["flash_ok"] = storage.flashOk;
        
        // Report CH375B availability (chip detected, regardless of USB drive)
        doc["ch375b_available"] = ch375b.available;
        
        String msg = doc.as<String>();
        ws.sendTXT(msg);
    }

    void sendRepoList() {
        JsonDocument doc;
        doc["type"] = "repo_list";
        JsonArray repos = doc["repositories"].to<JsonArray>();

        storage.listDir("/",
            [&repos](const char* name, bool isDir, uint32_t size) {
                if (isDir) {
                    JsonObject r = repos.add<JsonObject>();
                    r["name"] = name;
                    r["size"] = size;
                }
            });

        String msg = doc.as<String>();
        ws.sendTXT(msg);
        log_i("repo_list sent (%d repos)", repos.size());
    }

    // ── Incoming dispatch ─────────────────────────────

    void onWS(WStype_t type, uint8_t* payload, size_t len) {
        switch (type) {
        case WStype_CONNECTED:
            log_i("WS connected");
            online = true;
            if (!token.isEmpty()) sendHello();
            else {
                ensurePairCode();
                sendPairRequest(provision.pairCode.c_str());
            }
            break;

        case WStype_DISCONNECTED:
            log_w("WS disconnected");
            online = false;
            break;

        case WStype_TEXT: {
            JsonDocument doc;
            DeserializationError err = deserializeJson(doc, payload, len);
            if (err) { log_w("bad JSON: %s", err.c_str()); return; }

            String mtype = doc["type"] | "";
            if (mtype == "cmd")        handleCmd(doc);
            else if (mtype == "pair_ok")   handlePairOk(doc);
            else if (mtype == "pair_rejected") handlePairRejected();
            else if (mtype == "error")     log_w("server error: %s", (const char*)(doc["error"] | "?"));
            break;
        }
        default: break;
        }
    }

    void handlePairOk(JsonDocument& doc) {
        token = doc["token"] | "";
        String nid = doc["node_id"] | "";
        paired = true;
        provision.saveToken(token);
        log_i("PAIRED! node_id=%s token=%s", nid.c_str(), token.c_str());
        // Re-hello on the live socket so we transition to ONLINE
        // immediately instead of waiting for the next reconnect.
        if (!token.isEmpty()) sendHello();
    }

    void handlePairRejected() {
        log_e("pairing rejected — check code and try again");
        paired = false;
        token = "";
    }

    void handleCmd(JsonDocument& doc) {
        uint64_t reqId = doc["req_id"] | (uint64_t)0;
        String action  = doc["action"] | "";

        log_i("cmd: %s (req_id=%llu)", action.c_str(), reqId);

        if (action == "ping") {
            sendResp(reqId, "ack", nullptr);
            return;
        }
        
        if (action == "storage_reinit") {
            log_i("Received storage_reinit command - attempting to re-detect storage...");
            bool ok = storage.reinit();
            if (ok) {
                log_i("Storage re-initialization successful!");
                sendResp(reqId, "ack");
            } else {
                log_w("Storage re-initialization failed - no storage available");
                sendResp(reqId, "error", "storage_reinit_failed");
            }
            return;
        }
        
        if (action == "storage_status") {
            log_i("Received storage_status request");
            JsonDocument resp;
            resp["type"] = "storage_status";
            resp["req_id"] = reqId;
            resp["sd_ok"] = storage.sdOk;
            resp["usb_ok"] = storage.usbOk;
            resp["flash_ok"] = storage.flashOk;
            resp["ch375b_available"] = ch375b.available;
            resp["status"] = storage.getStatusString();
            resp["detailed"] = storage.getDetailedStatus();
            resp["total_bytes"] = storage.totalBytes();
            resp["free_bytes"] = storage.freeBytes();
            resp["sd_total"] = storage.sdOk ? SD.totalBytes() : 0;
            resp["sd_free"] = storage.sdOk ? (SD.totalBytes() - SD.usedBytes()) : 0;
            resp["usb_total"] = storage.usbOk ? (uint64_t)ch375b.diskCapacity() * 512 : 0;
            resp["flash_total"] = storage.flashTotalBytes();
            resp["flash_free"] = storage.flashFreeBytes();
            String msg = resp.as<String>();
            ws.sendTXT(msg);
            sendResp(reqId, "ack");
            return;
        }
        if (action == "repo_list") {
            sendRepoList();
            sendResp(reqId, "ack");
            return;
        }
        if (action == "repo_create") {
            String repo = doc["repo"] | "";
            
            // Validate repo name
            if (repo.isEmpty() || repo.length() > 64) {
                log_w("repo_create: Invalid repo name length: %d", repo.length());
                sendResp(reqId, "error", "invalid_repo_name");
                return;
            }
            
            // Check if we have any storage available
            if (!storage.hasStorage()) {
                log_e("repo_create: Cannot create repo - no storage available!");
                sendResp(reqId, "error", "no_storage_available");
                return;
            }
            
            String dir = String(SD_MOUNT_POINT) + "/" + repo;
            log_i("repo_create: Creating repository: %s at %s", repo.c_str(), dir.c_str());
            
            bool ok = storage.mkdir(dir.c_str());
            
            if (ok) {
                log_i("repo_create: Successfully created repo: %s", repo.c_str());
                sendResp(reqId, "ack");
                if (storage.sdOk) {
                    log_d("repo_create: Created on SD card");
                } else if (storage.usbOk) {
                    log_d("repo_create: Created on USB storage (fallback)");
                }
                sendRepoList();  // refresh
            } else {
                log_e("repo_create: Failed to create directory for: %s", repo.c_str());
                sendResp(reqId, "error", storage.sdOk ? "mkdir_failed_sd" : 
                         (storage.usbOk ? "mkdir_failed_usb" : "mkdir_failed_no_storage"));
            }
            return;
        }
        if (action == "repo_delete") {
            String repo = doc["repo"] | "";
            
            if (repo.isEmpty() || repo.length() > 64) {
                log_w("repo_delete: Invalid repo name: %s", repo.c_str());
                sendResp(reqId, "error", "invalid_repo_name");
                return;
            }
            
            if (!storage.hasStorage()) {
                log_e("repo_delete: Cannot delete - no storage available!");
                sendResp(reqId, "error", "no_storage_available");
                return;
            }
            
            String dir = String(SD_MOUNT_POINT) + "/" + repo;
            log_i("repo_delete: Removing repository: %s", repo.c_str());
            
            bool ok = storage.removeFile(dir.c_str());
            if (ok) {
                log_i("repo_delete: Successfully removed repo: %s", repo.c_str());
                sendResp(reqId, "ack");
                sendRepoList();
            } else {
                log_e("repo_delete: Failed to remove: %s", repo.c_str());
                sendResp(reqId, "error", "remove_failed");
            }
            return;
        }
        if (action == "fs_read") {
            handleFSRead(doc, reqId);
            return;
        }
        if (action == "fs_write") {
            handleFSWrite(doc, reqId);
            return;
        }
        if (action == "fs_list") {
            handleFSList(doc, reqId);
            return;
        }

        sendResp(reqId, "error", "unsupported_action");
    }

    void handleFSRead(JsonDocument& doc, uint64_t reqId) {
        String repo = doc["repo"]  | "";
        String path = doc["path"]  | "";
        uint32_t offset = doc["offset"] | (uint32_t)0;
        int len = doc["len"] | 8192;

        String fullPath = String(SD_MOUNT_POINT) + "/" + repo + "/" + path;

        uint8_t buf[8192];
        size_t n = storage.readFile(fullPath.c_str(), offset, buf, sizeof(buf));

        JsonDocument resp;
        resp["type"]   = "fs_data";
        resp["req_id"] = reqId;
        resp["ok"]     = true;
        resp["offset"] = offset;
        resp["len"]    = (int)n;
        resp["eof"]    = (n < (size_t)len);

        // Base64 encode
        resp["data_b64"] = base64::encode(buf, n);

        String msg = resp.as<String>();
        ws.sendTXT(msg);
    }

    void handleFSWrite(JsonDocument& doc, uint64_t reqId) {
        String repo = doc["repo"] | "";
        String path = doc["path"] | "";
        uint32_t offset = doc["offset"] | (uint32_t)0;
        String b64 = doc["data"] | "";

        String fullPath = String(SD_MOUNT_POINT) + "/" + repo + "/" + path;

        // Decode base64 via mbedtls
        size_t decodedLen = 0;
        // First pass: get output length
        mbedtls_base64_decode(NULL, 0, &decodedLen,
                              (const uint8_t*)b64.c_str(), b64.length());
        uint8_t* decoded = (uint8_t*)malloc(decodedLen);
        if (!decoded) {
            sendResp(reqId, "error", "alloc_failed");
            return;
        }
        size_t actualLen = 0;
        int rc = mbedtls_base64_decode(decoded, decodedLen, &actualLen,
                                       (const uint8_t*)b64.c_str(), b64.length());

        bool ok = false;
        if (rc == 0 && actualLen > 0) {
            ok = storage.writeFile(fullPath.c_str(), offset, decoded, actualLen);
        }
        free(decoded);

        sendResp(reqId, ok ? "ack" : "error",
                 ok ? nullptr : "write_failed");
    }

    void handleFSList(JsonDocument& doc, uint64_t reqId) {
        String repo = doc["repo"] | "";
        String path = doc["path"] | "";

        String fullPath = String(SD_MOUNT_POINT) + "/" + repo + "/" + path;

        JsonDocument resp;
        resp["type"]   = "fs_data";
        resp["req_id"] = reqId;
        resp["ok"]     = true;

        JsonArray entries = resp["entries"].to<JsonArray>();
        storage.listDir(fullPath.c_str(),
            [&entries](const char* name, bool isDir, uint32_t size) {
                JsonObject e = entries.add<JsonObject>();
                e["name"] = name;
                e["dir"]  = isDir;
                e["size"] = size;
            });

        String msg = resp.as<String>();
        ws.sendTXT(msg);
    }

    void sendResp(uint64_t reqId, const char* type, const char* errMsg = nullptr) {
        JsonDocument doc;
        doc["type"]   = type;
        doc["req_id"] = reqId;
        if (errMsg) doc["error"] = errMsg;
        else        doc["ok"]    = true;
        String msg = doc.as<String>();
        ws.sendTXT(msg);
    }

private:
    WebSocketsClient ws;

    void signDoc(JsonDocument& doc, const char* action) {
        String nid = provision.nodeId;
        double ts  = doc["ts"] | 0.0;
        String payload = String(action) + ":" + nid + ":" + String((uint64_t)ts);

        uint8_t seed[32];
        hexDecode(provision.privkeyHex.c_str(), seed, 32);

        uint8_t sig[64];
        crypto_sign(sig, seed, NULL,
            (const uint8_t*)payload.c_str(), payload.length());

        doc["sig"] = hexEncode(sig, 64);
    }

    static void hexDecode(const char* hex, uint8_t* out, size_t len) {
        for (size_t i = 0; i < len; i++) {
            uint8_t hi = hexCharToNibble(hex[i * 2]);
            uint8_t lo = hexCharToNibble(hex[i * 2 + 1]);
            out[i] = (hi << 4) | lo;
        }
    }

    static String hexEncode(const uint8_t* data, size_t len) {
        String s;
        s.reserve(len * 2);
        const char* tbl = "0123456789ABCDEF";
        for (size_t i = 0; i < len; i++) {
            s += tbl[data[i] >> 4];
            s += tbl[data[i] & 0x0F];
        }
        return s;
    }

    static uint8_t hexCharToNibble(char c) {
        if (c >= '0' && c <= '9') return c - '0';
        if (c >= 'A' && c <= 'F') return c - 'A' + 10;
        if (c >= 'a' && c <= 'f') return c - 'a' + 10;
        return 0;
    }
};

extern NodeProtocol node;
extern Provision provision;
