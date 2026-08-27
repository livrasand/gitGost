// gitGost Node — ESP32 firmware
// Standalone (SD/USB) + auto-detected CH375B USB host + internal LittleFS fallback.
// PlatformIO / Arduino framework for ESP32.

#include "Arduino.h"
#include "config.h"
#include "ch375b.h"
#include "storage.h"
#include "protocol.h"
#include "provision.h"

// Global instances
CH375B      ch375b;
Storage     storage;
NodeProtocol node;
Provision   provision;

WebServer   gostServer(80);
DNSServer   gostDNS;

// ── Provisioning reset button ──────────────────────────
#define PROVISION_RESET_BUTTON 0
#define PROVISION_RESET_HOLD_MS 5000

static uint32_t provisionButtonPressedAt = 0;
static bool provisionResetTriggered = false;

// ── Captive portal HTML ───────────────────────────────
static const char PORTAL_HTML[] PROGMEM = R"rawliteral(
<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>gitGost Setup</title><style>
*{box-sizing:border-box;margin:0;padding:0}body{font-family:system-ui,sans-serif;background:#0b0d10;color:#e6e6e6;padding:1.2rem;min-height:100vh;display:flex;align-items:center;justify-content:center}
.card{width:100%;max-width:420px;background:#12161c;border:1px solid #232a33;border-radius:12px;padding:1.2rem;box-shadow:0 10px 30px rgba(0,0,0,.4)}
h1{font-size:1.05rem;color:#ffa657;margin-bottom:.6rem;display:flex;align-items:center;gap:.4rem}
label{display:block;font-size:.72rem;color:#9ca3af;margin:.6rem 0 .2rem}
input{width:100%;background:#0b0d10;border:1px solid #232a33;color:#e6e6e6;padding:.55rem .6rem;border-radius:6px;font:inherit}
button{width:100%;margin-top:.7rem;background:#ffa657;color:#000;border:none;padding:.6rem;border-radius:6px;font-weight:600;cursor:pointer}
button.secondary{background:#1f2937;color:#e6e6e6;border:1px solid #232a33}
.row{display:flex;gap:.5rem}.row>div{flex:1}
.code{background:#0b0d10;border:1px dashed #232a33;padding:.6rem;border-radius:6px;text-align:center;font:1.2rem ui-monospace,monospace;letter-spacing:.15em;color:#22c55e;margin:.4rem 0}
.hint{font-size:.68rem;color:#6b7280;margin-top:.3rem}
.status{margin-top:.6rem;font-size:.78rem;color:#9ca3af}
.error{color:#ef4444}.ok{color:#22c55e}
</style></head><body><div class="card">
<h1>⚡ gitGost Setup</h1>
<div id="step1">
<label>WiFi SSID</label><input id="ssid" placeholder="MyWiFi">
<label>WiFi Password</label><input id="pass" type="password" placeholder="••••••">
<label>Server URL</label><input id="server" placeholder="gitgost.livrasand.com">
<div class="row"><div><label>Server port</label><input id="port" value="443"></div><div><label>Node name</label><input id="name" placeholder="desk-node"></div></div>
<label>Node ID</label><input id="nid" placeholder="auto">
<span class="hint">Dejar vacío para generar uno automáticamente.</span>
<button onclick="save()">Save & Generate keys</button>
</div>
<div id="step2" style="display:none">
<label>Pairing code</label>
<div class="code" id="code">---</div>
<label>Node ID</label>
<div class="code" id="nodeId">---</div>
<span class="hint">Enter the pairing code in the web UI or API. Node ID is used for WebSocket connection.</span>
<div class="status" id="status">Waiting for pairing...</div>
<button class="secondary" onclick="reset()">Reset</button>
</div>
</div><script>
function q(id){return document.getElementById(id)}
function save(){
  const ssid=q('ssid').value.trim();const pass=q('pass').value.trim();const server=q('server').value.trim();const port=parseInt(q('port').value||'443',10);const name=q('name').value.trim();const nid=q('nid').value.trim().toUpperCase();
  if(!ssid||!server){alert('SSID and server required');return}
  fetch('/api/save',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ssid,pass,server,port,name,nid})}).then(r=>r.json()).then(d=>{
    if(d.error){alert(d.error);return}
    q('step1').style.display='none';q('step2').style.display='block';
    document.title='gitGost - '+d.node_id;
    if(d.pair_code) document.getElementById('code').textContent=d.pair_code;
    if(d.node_id) document.getElementById('nodeId').textContent=d.node_id;
    pollStatus(d.node_id);
  })
}
function pollStatus(nid){
  const el=q('status');
  setInterval(()=>{fetch('/api/status?nid='+nid).then(r=>r.json()).then(d=>{
    if(d.paired){el.className='status ok';el.textContent='Paired! Restarting...';setTimeout(()=>location.reload(),2000)}
    else if(d.code){document.getElementById('code').textContent=d.code}
    if(d.node_id){document.getElementById('nodeId').textContent=d.node_id}
  })},1500)
}
function reset(){fetch('/api/reset',{method:'POST'}).then(()=>location.reload())}
</script></body></html>
)rawliteral";

// ── WiFi ──────────────────────────────────────────────
void wifiConnect() {
    if (WiFi.status() == WL_CONNECTED) return;

    log_i("WiFi connecting to %s", provision.wifiSSID.c_str());
    WiFi.mode(WIFI_STA);
    WiFi.begin(provision.wifiSSID.c_str(), provision.wifiPass.c_str());

    unsigned long start = millis();
    while (WiFi.status() != WL_CONNECTED && millis() - start < WIFI_TIMEOUT_MS) {
        delay(100);
    }

    if (WiFi.status() == WL_CONNECTED) {
        log_i("WiFi connected  IP=%s  RSSI=%d dBm",
              WiFi.localIP().toString().c_str(), WiFi.RSSI());
    } else {
        log_e("WiFi connection failed");
    }
}

void checkProvisionResetButton() {
    bool pressed = digitalRead(PROVISION_RESET_BUTTON) == LOW;

    if (pressed) {
        if (provisionButtonPressedAt == 0) {
            provisionButtonPressedAt = millis();
            log_i("BOOT button pressed — hold for 5 seconds to reset provisioning");
        }

        if (!provisionResetTriggered &&
            millis() - provisionButtonPressedAt >= PROVISION_RESET_HOLD_MS) {

            provisionResetTriggered = true;

            log_w("Resetting gitGost provisioning...");

            if (provision.reset()) {
                log_i("Provisioning reset successfully");
            } else {
                log_e("Failed to reset provisioning");
            }

            delay(500);
            ESP.restart();
        }
    } else {
        provisionButtonPressedAt = 0;
        provisionResetTriggered = false;
    }
}

// ── Storage health monitoring ────────────────────────
#define STORAGE_HEALTH_CHECK_INTERVAL_MS 30000  // check every 30s
#define STORAGE_REINIT_ON_FAILURE         true  // auto reinit if storage lost

static uint32_t lastStorageHealthCheck = 0;

void storageHealthCheck() {
    if (!storage.hasStorage()) {
        // No storage at all — try to re-detect
        if (STORAGE_REINIT_ON_FAILURE) {
            log_w("[HEALTH] No storage available — attempting re-detection...");
            storage.reinit();
        }
        return;
    }

    // Storage exists — verify it's still responsive
    bool sdWasOk  = storage.sdOk;
    bool usbWasOk = storage.usbOk;
    bool flashWasOk = storage.flashOk;

    // Quick liveness check: try to list root dir
    storage.listDir("/",
        [](const char* name, bool isDir, uint32_t size) {
            (void)name; (void)isDir; (void)size;
        }
    );

    if (sdWasOk && !storage.sdOk) {
        log_e("[HEALTH] SD card became unavailable — possible removal or corruption");
    }
    if (usbWasOk && !storage.usbOk) {
        log_e("[HEALTH] USB storage became unavailable — possible removal");
    }
    if (flashWasOk && !storage.flashOk) {
        log_e("[HEALTH] Internal Flash storage became unavailable — possible corruption");
    }

    // If all backends died, try full re-init
    if (!storage.sdOk && !storage.usbOk && !storage.flashOk) {
        log_e("[HEALTH] All storage backends lost!");
        if (STORAGE_REINIT_ON_FAILURE) {
            log_i("[HEALTH] Attempting full storage re-initialization...");
            storage.reinit();
        }
    }
}

// ── Setup ─────────────────────────────────────────────
void setup() {
    Serial.begin(115200);
    delay(500);
    pinMode(PROVISION_RESET_BUTTON, INPUT_PULLUP);
    log_i("\n=== gitGost node firmware  v1.0.0 ===");

    // 1. Provisioning / NVS
    provision.begin();
    if (!provision.configured) {
        log_i("First run — starting provisioning AP");
        provision.nodeId = Provision::genNodeId();
        provision.pairCode = Provision::genPairCode();
        provision.startAP(&gostServer, &gostDNS);
        log_i("AP started: gitGost-%s, IP=%s", provision.nodeId.c_str(), WiFi.softAPIP().toString().c_str());
        log_i("Pair code: %s", provision.pairCode.c_str());
        // Web handlers
        gostServer.on("/", HTTP_GET, []() { gostServer.send(200, "text/html", PORTAL_HTML); });
        gostServer.on("/api/save", HTTP_POST, []() {
            String body = gostServer.arg("plain");
            JsonDocument doc;
            DeserializationError err = deserializeJson(doc, body);
            if (err) { gostServer.send(400, "application/json", "{\"error\":\"bad_json\"}"); return; }
            String ssid = doc["ssid"] | "";
            String pass = doc["pass"] | "";
            String server = doc["server"] | "";
            uint16_t port = doc["port"] | 443;
            String name = doc["name"] | "";
            String nid = doc["nid"] | "";
            if (nid.isEmpty()) nid = Provision::genNodeId();
            if (name.isEmpty()) name = "gitGost-node";
            String pubhex, privhex;
            Provision::genKeyPairHex(pubhex, privhex);
            provision.save(name, ssid, pass, server, port, "/node/ws", nid, pubhex, privhex);
            provision.pairing = true;
            gostServer.send(200, "application/json", "{\"node_id\":\"" + nid + "\",\"pair_code\":\"" + provision.pairCode + "\"}");
            delay(3000);
            ESP.restart();
        });
        gostServer.on("/api/status", HTTP_GET, []() {
            String nid = gostServer.arg("nid");
            String out = "{\"paired\":false";
            if (provision.pairing && !provision.pairCode.isEmpty()) {
                out += ",\"code\":\"" + provision.pairCode + "\"";
                out += ",\"node_id\":\"" + provision.nodeId + "\"";
            }
            out += "}";
            gostServer.send(200, "application/json", out);
        });
        gostServer.on("/api/reset", HTTP_POST, []() {
            Preferences prefs;
            prefs.begin("gitgost", false);
            prefs.clear();
            prefs.end();
            gostServer.send(200, "application/json", "{\"ok\":true}");
            delay(500);
            ESP.restart();
        });
        return;
    }

    // 2. WiFi
    wifiConnect();

    // 3. Storage (SD → USB → Flash priority with fallback)
    storage.begin();

    // 4. Node protocol
    node.begin();
    node.connect();

    lastStorageHealthCheck = millis();
    log_i("setup complete — entering loop");
}

// ── Loop ──────────────────────────────────────────────
void loop() {
    checkProvisionResetButton();

    if (!provision.configured) {
        gostDNS.processNextRequest();
        gostServer.handleClient();
        return;
    }

    // Reconnect WiFi if dropped
    if (WiFi.status() != WL_CONNECTED) {
        wifiConnect();
    }

    // Periodic storage health check (hot-plug detection)
    if (millis() - lastStorageHealthCheck > STORAGE_HEALTH_CHECK_INTERVAL_MS) {
        lastStorageHealthCheck = millis();
        storageHealthCheck();
    }

    // Run WebSocket + heartbeat
    node.loop();
}
