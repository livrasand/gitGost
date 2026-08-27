#pragma once
// CH375B USB host controller — UART serial driver (9-bit protocol).
// The CH375B uses 1 start + 9 data bits + 1 stop at 9600 baud by default.
// D8=0: command, D8=1: data.
// Compiled always, but only talks hardware when ch375b_init() succeeds
// (i.e. a CH375B is physically wired and responds to GET_IC_VER).

#include <Arduino.h>
#include "config.h"

// ── CH375B command set (subset used by storage) ───
#define CH375B_CMD_GET_IC_VER     0x01
#define CH375B_CMD_SET_USB_MODE   0x15
#define CH375B_CMD_GET_STATUS     0x22
#define CH375B_CMD_RD_USB_DATA0   0x27
#define CH375B_CMD_WR_USB_DATA5   0x2D
#define CH375B_CMD_DISK_CONNECT   0x30
#define CH375B_CMD_DISK_MOUNT     0x31
#define CH375B_CMD_FILE_OPEN      0x32
#define CH375B_CMD_FILE_ENUM_DIR  0x33
#define CH375B_CMD_FILE_CREATE    0x34
#define CH375B_CMD_FILE_ERASE     0x45
#define CH375B_CMD_FILE_CLOSE     0x36
#define CH375B_CMD_BYTE_LOCATE    0x39
#define CH375B_CMD_BYTE_READ      0x3A
#define CH375B_CMD_BYTE_RD_GO     0x3B
#define CH375B_CMD_BYTE_WRITE     0x3D
#define CH375B_CMD_BYTE_WR_GO     0x3E
#define CH375B_CMD_DIR_INFO_READ  0x3F
#define CH375B_CMD_DISK_CAPACITY  0x40
#define CH375B_CMD_DISK_AVAIL     0x42
#define CH375B_CMD_SET_FILE_NAME  0x2F
#define CH375B_CMD_DIR_CREATE     0x40

// Status codes returned by GET_STATUS
#define CH375B_USB_INT_SUCCESS    0x14
#define CH375B_USB_INT_DISK_READ  0x1D
#define CH375B_USB_INT_DISK_WRITE 0x1E
#define CH375B_ERR_OPEN_DIR       0x41
#define CH375B_ERR_MISS_FILE      0x42

class CH375B {
public:
    bool     available = false;
    uint8_t  _version  = 0x00;

    bool init() {
        log_i("[CH375B] Initializing UART serial (TX=GPIO%d, RX=GPIO%d, baud=%d)...",
              CH375B_TX, CH375B_RX, CH375B_BAUD);

        pinMode(CH375B_TX, OUTPUT);
        digitalWrite(CH375B_TX, HIGH);
        pinMode(CH375B_RX, INPUT_PULLUP);
        delay(20);

        log_d("[CH375B] Sending GET_IC_VER command...");
        _version = xfer(CH375B_CMD_GET_IC_VER, true);

        if (_version == 0x00 || _version == 0xFF) {
            log_w("[CH375B] No response (got 0x%02X) — check: wiring, power (3.3V), TX/RX pins GPIO%d/GPIO%d",
                  _version, CH375B_TX, CH375B_RX);
            available = false;
            return false;
        }

        log_i("[CH375B] Chip detected! Version=0x%02X", _version);

        log_d("[CH375B] Setting USB host mode (mode=6, host+SOF)...");
        xfer(CH375B_CMD_SET_USB_MODE, true);
        xfer(0x06, false);

        available = true;
        log_i("[CH375B] Initialized and ready");
        return true;
    }

    uint8_t getVersion() const {
        return _version;
    }

    bool diskConnect() {
        xfer(CH375B_CMD_DISK_CONNECT, true);
        return waitStatus() == CH375B_USB_INT_SUCCESS;
    }

    bool diskMount() {
        xfer(CH375B_CMD_DISK_MOUNT, true);
        return waitStatus() == CH375B_USB_INT_SUCCESS;
    }

    uint32_t diskCapacity() {
        xfer(CH375B_CMD_DISK_CAPACITY, true);
        waitStatus();
        uint32_t cap = 0;
        readBuf((uint8_t*)&cap, 4);
        return cap;
    }

    bool fileOpen(const char* path) {
        xfer(CH375B_CMD_SET_FILE_NAME, true);
        sendString(path);
        xfer(CH375B_CMD_FILE_OPEN, true);
        return waitStatus() == CH375B_USB_INT_SUCCESS;
    }

    bool fileCreate(const char* path) {
        xfer(CH375B_CMD_SET_FILE_NAME, true);
        sendString(path);
        xfer(CH375B_CMD_FILE_CREATE, true);
        return waitStatus() == CH375B_USB_INT_SUCCESS;
    }

    bool fileClose() {
        xfer(CH375B_CMD_FILE_CLOSE, true);
        return waitStatus() == CH375B_USB_INT_SUCCESS;
    }

    bool fileErase(const char* path) {
        xfer(CH375B_CMD_SET_FILE_NAME, true);
        sendString(path);
        xfer(CH375B_CMD_FILE_ERASE, true);
        return waitStatus() == CH375B_USB_INT_SUCCESS;
    }

    bool byteLocate(uint32_t offset) {
        xfer(CH375B_CMD_BYTE_LOCATE, true);
        uint8_t b[4] = {
            (uint8_t)(offset),
            (uint8_t)(offset >> 8),
            (uint8_t)(offset >> 16),
            (uint8_t)(offset >> 24)
        };
        writeBuf(b, 4);
        return waitStatus() == CH375B_USB_INT_SUCCESS;
    }

    size_t byteRead(uint8_t* buf, uint16_t len) {
        xfer(CH375B_CMD_BYTE_READ, true);
        uint8_t b[2] = { (uint8_t)(len), (uint8_t)(len >> 8) };
        writeBuf(b, 2);

        size_t total = 0;
        while (total < len) {
            uint8_t st = waitStatus();
            if (st == CH375B_USB_INT_SUCCESS) break;
            if (st != CH375B_USB_INT_DISK_READ) return total;

            xfer(CH375B_CMD_RD_USB_DATA0, true);
            uint8_t chunk = xfer(0x00, false);
            for (uint8_t i = 0; i < chunk && total < len; i++) {
                buf[total++] = xfer(0x00, false);
            }
            xfer(CH375B_CMD_BYTE_RD_GO, true);
        }
        return total;
    }

    bool byteWrite(const uint8_t* data, uint16_t len) {
        uint16_t sent = 0;
        while (sent < len) {
            uint16_t chunk = len - sent;
            if (chunk > 255) chunk = 255;

            xfer(CH375B_CMD_BYTE_WRITE, true);
            xfer((uint8_t)chunk, false);

            for (uint16_t i = 0; i < chunk; i++) {
                xfer(data[sent + i], false);
            }

            uint8_t st = waitStatus();
            if (st != CH375B_USB_INT_SUCCESS && st != CH375B_USB_INT_DISK_WRITE) {
                return false;
            }
            sent += chunk;
        }
        return true;
    }

    bool dirEnumerate(const char* dirPath,
                      std::function<void(const char*, uint32_t)> cb) {
        xfer(CH375B_CMD_SET_FILE_NAME, true);
        sendString(dirPath);
        xfer(CH375B_CMD_FILE_OPEN, true);
        if (waitStatus() != CH375B_USB_INT_SUCCESS) return false;

        while (true) {
            xfer(CH375B_CMD_DIR_INFO_READ, true);
            xfer(0xFF, false);
            if (waitStatus() != CH375B_USB_INT_SUCCESS) break;

            uint8_t info[32];
            readBuf(info, 32);

            char name[13];
            memcpy(name, info, 11);
            name[11] = '\0';
            for (int i = 10; i >= 0 && name[i] == ' '; i--) name[i] = '\0';

            uint32_t fsize = info[28] | (info[29]<<8) | (info[30]<<16) | (info[31]<<24);
            uint8_t  attr   = info[11];

            if ((attr & 0x08) || strcmp(name, ".") == 0 || strcmp(name, "..") == 0) {
            } else {
                cb(name, fsize);
            }

            xfer(CH375B_CMD_FILE_ENUM_DIR, true);
            xfer(0x00, false);
            if (waitStatus() != CH375B_USB_INT_SUCCESS) break;
        }

        fileClose();
        return true;
    }

    bool dirCreate(const char* path) {
        xfer(CH375B_CMD_SET_FILE_NAME, true);
        sendString(path);
        xfer(CH375B_CMD_DIR_CREATE, true);
        return waitStatus() == CH375B_USB_INT_SUCCESS;
    }

private:
    static constexpr uint32_t BIT_DELAY_US = 105;
    static constexpr uint32_t HALF_BIT_DELAY_US = 52;

    static void delayBits(uint32_t n) {
        for (uint32_t i = 0; i < n; i++) {
            delayMicroseconds(BIT_DELAY_US);
        }
    }

    void send9(uint16_t bits) {
        digitalWrite(CH375B_TX, LOW);
        delayMicroseconds(BIT_DELAY_US);

        for (int i = 0; i < 9; i++) {
            digitalWrite(CH375B_TX, (bits & 0x01) ? HIGH : LOW);
            delayMicroseconds(BIT_DELAY_US);
            bits >>= 1;
        }

        digitalWrite(CH375B_TX, HIGH);
        delayMicroseconds(BIT_DELAY_US);
    }

    uint8_t read9() {
        uint32_t start = micros();
        while (digitalRead(CH375B_RX) == HIGH) {
            if (micros() - start > 5000) {
                log_w("[CH375B] read9 timeout waiting for start bit");
                return 0x00;
            }
        }

        delayMicroseconds(BIT_DELAY_US + HALF_BIT_DELAY_US);

        uint16_t bits = 0;
        for (int i = 0; i < 9; i++) {
            bits >>= 1;
            if (digitalRead(CH375B_RX) == HIGH) {
                bits |= 0x0100;
            }
            delayMicroseconds(BIT_DELAY_US);
        }

        return (uint8_t)bits;
    }

    uint8_t xfer(uint8_t out, bool isCommand) {
        uint8_t in = 0;
        {
            noInterrupts();
            send9((uint16_t)out | (isCommand ? 0x0000 : 0x0100));
            in = read9();
            interrupts();
        }
        delayMicroseconds(BIT_DELAY_US * 2);
        return in;
    }

    void writeBuf(const uint8_t* data, size_t len) {
        for (size_t i = 0; i < len; i++) {
            noInterrupts();
            send9((uint16_t)data[i] | 0x0100);
            interrupts();
            delayMicroseconds(BIT_DELAY_US);
        }
    }

    void readBuf(uint8_t* data, size_t len) {
        for (size_t i = 0; i < len; i++) {
            data[i] = read9();
        }
    }

    void sendString(const char* s) {
        while (*s) {
            noInterrupts();
            send9((uint16_t)*s++ | 0x0100);
            interrupts();
            delayMicroseconds(BIT_DELAY_US);
        }
        noInterrupts();
        send9(0x0100);
        interrupts();
        delayMicroseconds(BIT_DELAY_US);
    }

    uint8_t waitStatus(uint32_t timeoutMs = 2000) {
        unsigned long start = millis();
        while (millis() - start < timeoutMs) {
            uint8_t st = xfer(CH375B_CMD_GET_STATUS, true);
            if (st != 0) {
                if (st != CH375B_USB_INT_SUCCESS) {
                    log_d("[CH375B] Status event: 0x%02X (after %u ms)",
                          st, (unsigned)(millis() - start));
                }
                return st;
            }
            delay(2);
        }
        log_e("[CH375B] TIMEOUT waiting for status after %u ms", (unsigned)timeoutMs);
        return 0xFF;
    }
};

extern CH375B ch375b;
