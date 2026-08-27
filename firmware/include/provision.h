#pragma once
// gitGost Node Provisioning — WiFi AP + captive portal + first-run setup.
// Stores node_id, wifi_ssid, wifi_pass, server_host, server_port,
// server_ws_path, node_name, pubkey_hex, privkey_hex in NVS Preferences.

#include <Arduino.h>
#include <WiFi.h>
#include <DNSServer.h>
#include <Preferences.h>
#include <WebServer.h>
#include "config.h"
#include "protocol.h"
extern "C" {
#include <monocypher.h>
}

class Provision {
public:
    bool configured = false;
    String nodeId;
    String nodeName;
    String wifiSSID;
    String wifiPass;
    String serverHost;
    uint16_t serverPort;
    String serverWsPath;
    String pubkeyHex;
    String privkeyHex;
    String pairCode;
    String authToken;
    bool pairing = false;

    void begin() {
        Preferences prefs;
        prefs.begin("gitgost", true);
        configured = prefs.getBool("configured", false);
        if (!configured) {
            prefs.end();
            return;
        }
        nodeId       = prefs.getString("node_id", "");
        nodeName     = prefs.getString("node_name", "");
        wifiSSID     = prefs.getString("wifi_ssid", "");
        wifiPass     = prefs.getString("wifi_pass", "");
        serverHost   = prefs.getString("server_host", "");
        serverPort   = prefs.getUShort("server_port", 443);
        serverWsPath = prefs.getString("server_ws_path", "/node/ws");
        pubkeyHex    = prefs.getString("pubkey_hex", "");
        privkeyHex   = prefs.getString("privkey_hex", "");
        pairCode     = prefs.getString("pair_code", "");
        authToken    = prefs.getString("auth_token", "");
        prefs.end();
    }

    bool save(const String& name, const String& ssid, const String& pass,
              const String& host, uint16_t port, const String& path,
              const String& nid, const String& pub, const String& priv) {
        Preferences prefs;
        prefs.begin("gitgost", false);
        prefs.putBool("configured", true);
        prefs.putString("node_id", nid);
        prefs.putString("node_name", name);
        prefs.putString("wifi_ssid", ssid);
        prefs.putString("wifi_pass", pass);
        prefs.putString("server_host", host);
        prefs.putUShort("server_port", port);
        prefs.putString("server_ws_path", path);
        prefs.putString("pubkey_hex", pub);
        prefs.putString("privkey_hex", priv);
        prefs.putString("pair_code", pairCode);
        prefs.putString("auth_token", authToken);
        prefs.end();
        nodeId = nid;
        nodeName = name;
        wifiSSID = ssid;
        wifiPass = pass;
        serverHost = host;
        serverPort = port;
        serverWsPath = path;
        pubkeyHex = pub;
        privkeyHex = priv;
        configured = true;
        return true;
    }

    bool saveToken(const String& token) {
        Preferences prefs;
        prefs.begin("gitgost", false);
        prefs.putString("auth_token", token);
        prefs.end();
        authToken = token;
        return true;
    }

    bool savePairCode(const String& code) {
        Preferences prefs;
        prefs.begin("gitgost", false);
        prefs.putString("pair_code", code);
        prefs.end();
        pairCode = code;
        return true;
    }

    bool reset() {
        Preferences prefs;

        if (!prefs.begin("gitgost", false)) {
            return false;
        }

        prefs.clear();
        prefs.end();

        // Limpiar también el estado en RAM
        configured = false;
        nodeId = "";
        nodeName = "";
        wifiSSID = "";
        wifiPass = "";
        serverHost = "";
        serverPort = 443;
        serverWsPath = "/node/ws";
        pubkeyHex = "";
        privkeyHex = "";
        pairCode = "";
        authToken = "";
        pairing = false;

        return true;
    }

    static String genNodeId() {
        uint8_t b[4];
        for (int i = 0; i < 4; i++) b[i] = random(0, 256);
        char out[9];
        for (int i = 0; i < 4; i++) sprintf(out + i * 2, "%02X", b[i]);
        return String(out);
    }

    static String genKeyHex() {
        uint8_t seed[32];
        for (int i = 0; i < 32; i++) seed[i] = random(0, 256);
        uint8_t pub[32];
        crypto_sign_public_key(pub, seed);
        char out[65];
        for (int i = 0; i < 32; i++) sprintf(out + i * 2, "%02X", pub[i]);
        return String(out);
    }

    static void genKeyPairHex(String& pub, String& priv) {
        uint8_t seed[32];
        for (int i = 0; i < 32; i++) seed[i] = random(0, 256);
        uint8_t pubkey[32];
        crypto_sign_public_key(pubkey, seed);
        char pubout[65];
        char privout[65];
        for (int i = 0; i < 32; i++) {
            sprintf(pubout + i * 2, "%02X", pubkey[i]);
            sprintf(privout + i * 2, "%02X", seed[i]);
        }
        pub = String(pubout);
        priv = String(privout);
    }

    static String genPairCode() {
        const char* alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
        char code[7];
        for (int i = 0; i < 6; i++) code[i] = alphabet[random(0, 32)];
        code[6] = 0;
        return String(code);
    }

    void startAP(WebServer* server, DNSServer* dns) {
        String apSSID = "gitGost-" + nodeId;
        WiFi.mode(WIFI_AP);
        WiFi.softAP(apSSID.c_str());
        dns->start(53, "*", WiFi.softAPIP());
        server->begin();
    }

    void stopAP() {
        WiFi.softAPdisconnect(true);
        WiFi.mode(WIFI_STA);
    }

    bool connectSTA() {
        if (wifiSSID.isEmpty()) return false;
        WiFi.mode(WIFI_STA);
        WiFi.begin(wifiSSID.c_str(), wifiPass.c_str());
        unsigned long start = millis();
        while (WiFi.status() != WL_CONNECTED && millis() - start < 20000) delay(100);
        return WiFi.status() == WL_CONNECTED;
    }
};

extern Provision provision;
