#pragma once
// Unified storage interface — SD card, CH375B USB host, and internal
// LittleFS flash. Priority: SD → USB → Flash (fallback/overflow).
// When no external storage is present, repositories are stored on the
// ESP32's internal flash filesystem.

#include <SD.h>
#include <SPI.h>
#include <LittleFS.h>
#include "config.h"
#include "ch375b.h"

class Storage {
public:
    bool sdOk   = false;
    bool usbOk  = false;
    bool flashOk = false;
    bool usbDetecting = false;  // true while CH375B probe is in progress

    void begin() {
        log_i("Initializing storage subsystem...");

        // ── SD card initialization (with retry logic) ────────────
        log_i("[SD] Probing SD card on VSPI bus (CS=GPIO%d, CLK=%d, MISO=%d, MOSI=%d)",
              SD_CS, SD_CLK, SD_MISO, SD_MOSI);

        SPI.begin(SD_CLK, SD_MISO, SD_MOSI, SD_CS);

        const int sdRetries = 3;
        for (int attempt = 1; attempt <= sdRetries; attempt++) {
            log_i("[SD] Mount attempt %d/%d...", attempt, sdRetries);
            sdOk = SD.begin(SD_CS);

            if (sdOk) {
                uint64_t totalBytes = SD.totalBytes();
                uint64_t usedBytes  = SD.usedBytes();
                uint64_t freeBytes  = totalBytes - usedBytes;
                log_i("[SD] Mounted successfully!");
                log_i("[SD]   Total: %llu MB  |  Free: %llu MB  |  Used: %llu MB",
                      totalBytes / (1024*1024), freeBytes / (1024*1024), usedBytes / (1024*1024));
                break;
            } else {
                log_w("[SD] Mount failed (attempt %d/%d)", attempt, sdRetries);
                if (attempt < sdRetries) {
                    delay(150);
                }
            }
        }

        if (!sdOk) {
            log_e("[SD] Card NOT detected after %d attempts", sdRetries);
            log_e("[SD] Troubleshooting:");
            log_e("[SD]   1. Verify card is fully inserted");
            log_e("[SD]   2. Card must be FAT32/exFAT (not NTFS, ext4, etc.)");
            log_e("[SD]   3. Check VSPI wiring: CLK=%d MISO=%d MOSI=%d CS=%d",
                  SD_CLK, SD_MISO, SD_MOSI, SD_CS);
            log_e("[SD]   4. SD card reader must be powered at 3.3V (NOT 5V)");
            log_e("[SD]   5. Try a different SD card (some are ESP32-incompatible)");
        }

        // ── CH375B USB host initialization (dynamic runtime detection) ─
        if (!sdOk) {
            detectUSB();
        }

        // ── Internal LittleFS flash initialization ────────────
        if (!sdOk) {
            initFlash();
        }

        // ── Final storage status ──────────────────────────────
        if (!sdOk && !usbOk && !flashOk) {
            log_e("[STORAGE] CRITICAL: No storage backend available!");
            log_e("[STORAGE] Node will connect but cannot store repositories.");
            log_e("[STORAGE] Fix: insert FAT32 SD card, connect USB drive, or add LittleFS partition.");
        } else if (sdOk && usbOk && flashOk) {
            log_i("[STORAGE] Ready — Triple mode: SD + USB + Flash");
        } else if (sdOk && usbOk) {
            log_i("[STORAGE] Ready — Dual mode: SD + USB");
        } else if (sdOk && flashOk) {
            log_i("[STORAGE] Ready — Dual mode: SD + Flash");
        } else if (usbOk && flashOk) {
            log_i("[STORAGE] Ready — Dual mode: USB + Flash");
        } else if (sdOk) {
            log_i("[STORAGE] Ready — Primary: SD card");
        } else if (usbOk) {
            log_i("[STORAGE] Ready — Primary: USB (SD fallback active)");
        } else {
            log_i("[STORAGE] Ready — Primary: Internal Flash (no external storage)");
        }
        log_i("[STORAGE] sdOk=%s  usbOk=%s  flashOk=%s  ch375b_available=%s",
              sdOk ? "YES" : "NO", usbOk ? "YES" : "NO",
              flashOk ? "YES" : "NO", ch375b.available ? "YES" : "NO");
    }

    // Attempt USB detection independently (call when SD just failed or on hot-plug)
    bool detectUSB() {
        if (usbDetecting) return usbOk;  // avoid re-entrance
        usbDetecting = true;

        log_i("[USB] Probing CH375B USB host controller (TX=GPIO%d, RX=GPIO%d)...", CH375B_TX, CH375B_RX);

        const int usbRetries = 1;
        for (int attempt = 1; attempt <= usbRetries; attempt++) {
            log_i("[USB] CH375B detection attempt %d/%d...", attempt, usbRetries);

            if (ch375b.init()) {
                uint8_t ver = ch375b.getVersion();
                log_i("[USB] CH375B chip detected! Version=0x%02X", ver);

                log_i("[USB] Connecting USB device...");
                if (ch375b.diskConnect()) {
                    log_i("[USB] USB device connected, mounting filesystem...");
                    if (ch375b.diskMount()) {
                        usbOk = true;
                        uint32_t capKB = ch375b.diskCapacity();
                        log_i("[USB] Drive mounted successfully! Capacity=%lu MB", capKB / 2);

                        log_i("[USB] Activated as PRIMARY storage (SD unavailable)");
                        usbDetecting = false;
                        return true;
                    } else {
                        log_e("[USB] diskMount FAILED — check USB drive format (must be FAT32)");
                    }
                } else {
                    log_e("[USB] diskConnect FAILED — check USB drive is plugged in and powered");
                }
                break; // chip found but disk ops failed — don't retry chip detection
            } else {
                log_w("[USB] CH375B not responding (attempt %d/%d)", attempt, usbRetries);
            }
        }

        if (!ch375b.available) {
            log_i("[USB] CH375B not present — USB storage disabled");
        }
        usbDetecting = false;
        return false;
    }

    // Initialize internal LittleFS flash storage
    bool initFlash() {
        log_i("[FLASH] Probing internal LittleFS filesystem...");

        // Try to mount LittleFS; auto-format if the partition is corrupted
        flashOk = LittleFS.begin(true);

        if (flashOk) {
            uint64_t totalBytes = LittleFS.totalBytes();
            uint64_t usedBytes  = LittleFS.usedBytes();
            uint64_t freeBytes  = totalBytes - usedBytes;
            log_i("[FLASH] Mounted successfully!");
            log_i("[FLASH]   Total: %llu MB  |  Free: %llu MB  |  Used: %llu MB",
                  totalBytes / (1024*1024), freeBytes / (1024*1024), usedBytes / (1024*1024));
        } else {
            log_e("[FLASH] Mount failed — LittleFS partition may be missing or corrupted");
            log_e("[FLASH] Troubleshooting:");
            log_e("[FLASH]   1. Ensure partition table includes a LittleFS partition");
            log_e("[FLASH]   2. Check platformio.ini: board_build.filesystem = littlefs");
            log_e("[FLASH]   3. Try 'pio run --target menuconfig' to adjust partition size");
        }

        return flashOk;
    }

    // Translate external mount paths to internal flash paths.
    // e.g. "/sd/myrepo/README.md" → "/myrepo/README.md"
    String translateToFlashPath(const char* path) {
        String p = path;

        // Strip /sd/ prefix
        if (p.startsWith("/sd/")) {
            p = p.substring(3);
        }
        // Strip /usb/ prefix
        else if (p.startsWith("/usb/")) {
            p = p.substring(4);
        }
        // Handle bare mount points
        else if (p == "/sd") {
            p = "/";
        } else if (p == "/usb") {
            p = "/";
        }

        // Ensure path is not empty and starts with /
        if (p.length() == 0) {
            p = "/";
        }

        return p;
    }

    // ── Stats for heartbeat ───────────────────────────
    uint64_t totalBytes() {
        uint64_t t = 0;
        if (sdOk)  t += SD.totalBytes();
        if (usbOk) t += (uint64_t)ch375b.diskCapacity() * 512;
        if (flashOk) t += LittleFS.totalBytes();
        return t;
    }

    uint64_t freeBytes() {
        uint64_t f = 0;
        if (sdOk)  f += SD.totalBytes() - SD.usedBytes();
        if (usbOk) f += (uint64_t)ch375b.diskCapacity() * 512;  // rough
        if (flashOk) f += LittleFS.totalBytes() - LittleFS.usedBytes();
        return f;
    }

    uint64_t flashTotalBytes() {
        if (flashOk) return LittleFS.totalBytes();
        return 0;
    }

    uint64_t flashFreeBytes() {
        if (flashOk) return LittleFS.totalBytes() - LittleFS.usedBytes();
        return 0;
    }

    // Check if any storage is available
    bool hasStorage() {
        return sdOk || usbOk || flashOk;
    }

    // Which backend is currently serving requests?
    const char* getPrimaryStorage() {
        if (sdOk && usbOk && flashOk) return "SD+USB+Flash";
        if (sdOk && usbOk) return "SD+USB";
        if (sdOk && flashOk) return "SD+Flash";
        if (usbOk && flashOk) return "USB+Flash";
        if (sdOk)          return "SD";
        if (usbOk)         return "USB";
        if (flashOk)       return "Flash";
        return "NONE";
    }

    // Get storage status as a human-readable string
    String getStatusString() {
        if (sdOk && usbOk && flashOk) return "SD+USB+Flash";
        if (sdOk && usbOk) return "SD+USB";
        if (sdOk && flashOk) return "SD+Flash";
        if (usbOk && flashOk) return "USB+Flash";
        if (sdOk)          return "SD";
        if (usbOk)         return "USB";
        if (flashOk)       return "Flash";
        return "NONE";
    }

    // Get detailed storage status for diagnostics
    String getDetailedStatus() {
        String status = "Storage: ";
        if (!sdOk && !usbOk && !flashOk) {
            status += "NONE (no storage available)";
        } else {
            if (sdOk) status += "SD";
            if (usbOk) {
                if (sdOk) status += "+USB";
                else status += "USB";
            }
            if (flashOk) {
                if (sdOk || usbOk) status += "+Flash";
                else status += "Flash";
            }
            status += " (active)";
        }
        return status;
    }

    // Re-initialize storage — useful for hot-plug detection
    // Returns true if any storage became available
    bool reinit() {
        log_i("[STORAGE] Re-initializing storage subsystem (hot-plug/resync)...");

        bool wasSdOk  = sdOk;
        bool wasUsbOk = usbOk;
        bool wasFlashOk = flashOk;

        // ── Re-initialize SD ──────────────────────────────
        sdOk = false;
        const int sdRetries = 2;
        for (int attempt = 1; attempt <= sdRetries; attempt++) {
            log_i("[SD] Re-mount attempt %d/%d...", attempt, sdRetries);
            sdOk = SD.begin(SD_CS);
            if (sdOk) {
                log_i("[SD] Re-mounted successfully!");
                break;
            }
            delay(100);
        }

        // ── Re-initialize CH375B/USB ──────────────────────
        usbOk = false;
        ch375b.available = false;  // force re-detection
        detectUSB();

        // ── Re-initialize LittleFS Flash ──────────────────
        flashOk = false;
        initFlash();

        bool nowAvailable = sdOk || usbOk || flashOk;
        bool wasAvailable = wasSdOk || wasUsbOk || wasFlashOk;

        if (!wasAvailable && nowAvailable) {
            log_i("[STORAGE] Storage became available after re-init! (sdOk=%s usbOk=%s flashOk=%s)",
                  sdOk ? "YES" : "NO", usbOk ? "YES" : "NO", flashOk ? "YES" : "NO");
        } else if (wasAvailable && !nowAvailable) {
            log_e("[STORAGE] Storage lost after re-init! (sdOk=%s usbOk=%s flashOk=%s)",
                  sdOk ? "YES" : "NO", usbOk ? "YES" : "NO", flashOk ? "YES" : "NO");
        } else if (wasAvailable && nowAvailable) {
            log_i("[STORAGE] Storage still available after re-init");
        } else {
            log_w("[STORAGE] Still no storage after re-init");
        }

        return nowAvailable;
    }

    // ── File I/O (SD-primary, USB fallback, Flash fallback) ───────────

    bool fileExists(const char* path) {
        if (sdOk) {
            bool exists = SD.exists(path);
            if (exists) {
                log_d("[FS] EXISTS on SD: %s", path);
                return true;
            }
            log_d("[FS] Not found on SD: %s → trying USB...", path);
        }

        if (usbOk && ch375b.available) {
            bool exists = ch375b.fileOpen(path);
            if (exists) {
                ch375b.fileClose();
                log_d("[FS] EXISTS on USB: %s", path);
                return true;
            }
            log_d("[FS] Not found on USB: %s → trying Flash...", path);
        }

        if (flashOk) {
            String flashPath = translateToFlashPath(path);
            bool exists = LittleFS.exists(flashPath.c_str());
            if (exists) {
                log_d("[FS] EXISTS on Flash: %s → %s", path, flashPath.c_str());
                return true;
            }
            log_d("[FS] Not found on Flash: %s → %s", path, flashPath.c_str());
        }

        if (!sdOk && !usbOk && !flashOk) {
            log_w("[FS] fileExists: NO STORAGE AVAILABLE for: %s", path);
        }
        return false;
    }

    // Read up to `maxLen` bytes from `path` starting at `offset`.
    // Returns actual bytes read, writes into `buf`.
    size_t readFile(const char* path, uint32_t offset,
                    uint8_t* buf, size_t maxLen) {
        // Try SD first
        if (sdOk) {
            File f = SD.open(path, FILE_READ);
            if (f) {
                log_d("[FS] READ from SD: %s (offset=%u, maxLen=%u)", path, offset, maxLen);
                f.seek(offset);
                size_t n = f.read(buf, maxLen);
                f.close();
                if (n > 0) {
                    log_d("[FS] Read %u bytes from SD: %s", (unsigned)n, path);
                } else {
                    log_w("[FS] SD read returned 0 bytes: %s", path);
                }
                return n;
            }
            log_d("[FS] SD open failed: %s → trying USB...", path);
        }

        // Fall back to USB
        if (usbOk) {
            if (ch375b.fileOpen(path)) {
                log_d("[FS] READ from USB: %s (offset=%u, maxLen=%u)", path, offset, maxLen);
                ch375b.byteLocate(offset);
                size_t n = ch375b.byteRead(buf, maxLen);
                ch375b.fileClose();
                if (n > 0) {
                    log_d("[FS] Read %u bytes from USB: %s", (unsigned)n, path);
                } else {
                    log_w("[FS] USB read returned 0 bytes: %s", path);
                }
                return n;
            }
            log_d("[FS] USB open failed: %s → trying Flash...", path);
        }

        // Fall back to internal flash
        if (flashOk) {
            String flashPath = translateToFlashPath(path);
            File f = LittleFS.open(flashPath.c_str(), "r");
            if (f) {
                log_d("[FS] READ from Flash: %s → %s (offset=%u, maxLen=%u)",
                      path, flashPath.c_str(), offset, maxLen);
                f.seek(offset);
                size_t n = f.read(buf, maxLen);
                f.close();
                if (n > 0) {
                    log_d("[FS] Read %u bytes from Flash: %s", (unsigned)n, path);
                } else {
                    log_w("[FS] Flash read returned 0 bytes: %s", path);
                }
                return n;
            }
            log_d("[FS] Flash open failed: %s", path);
        }

        if (!sdOk && !usbOk && !flashOk) {
            log_e("[FS] readFile: NO STORAGE AVAILABLE for: %s", path);
        } else {
            log_e("[FS] readFile: File not found on any backend: %s", path);
        }
        return 0;
    }

    // Write `len` bytes to `path` at `offset`.  Creates file if needed.
    bool writeFile(const char* path, uint32_t offset,
                   const uint8_t* data, size_t len) {
        // Try SD first
        if (sdOk) {
            File f = SD.open(path, FILE_WRITE);
            if (f) {
                log_d("[FS] WRITE to SD: %s (offset=%u, len=%u)", path, offset, len);
                f.seek(offset);
                size_t written = f.write(data, len);
                f.close();
                if (written == len) {
                    log_d("[FS] Wrote %u bytes to SD: %s", (unsigned)written, path);
                    return true;
                }
                log_e("[FS] Partial write on SD: %u/%u bytes for %s",
                      (unsigned)written, (unsigned)len, path);
            } else {
                log_w("[FS] SD open failed for write: %s → trying USB...", path);
            }
        }

        // Fall back to USB
        if (usbOk) {
            log_d("[FS] WRITE to USB: %s (offset=%u, len=%u)", path, offset, len);
            // CH375B fileCreate overwrites if exists, which is desired for write
            if (ch375b.fileCreate(path)) {
                if (ch375b.byteLocate(offset)) {
                    bool ok = ch375b.byteWrite(data, len);
                    ch375b.fileClose();
                    if (ok) {
                        log_d("[FS] Wrote %u bytes to USB: %s", (unsigned)len, path);
                        return true;
                    }
                    log_e("[FS] USB byteWrite failed: %s", path);
                } else {
                    log_e("[FS] USB byteLocate failed: %s", path);
                }
            } else {
                log_e("[FS] USB fileCreate failed: %s", path);
            }
        }

        // Fall back to internal flash
        if (flashOk) {
            String flashPath = translateToFlashPath(path);
            log_d("[FS] WRITE to Flash: %s → %s (offset=%u, len=%u)",
                  path, flashPath.c_str(), offset, len);

            File f = LittleFS.open(flashPath.c_str(), "r+");
            if (!f) {
                f = LittleFS.open(flashPath.c_str(), "w");
            }
            if (f) {
                f.seek(offset);
                size_t written = f.write(data, len);
                f.close();
                if (written == len) {
                    log_d("[FS] Wrote %u bytes to Flash: %s", (unsigned)written, path);
                    return true;
                }
                log_e("[FS] Partial write on Flash: %u/%u bytes for %s",
                      (unsigned)written, (unsigned)len, path);
            } else {
                log_e("[FS] Flash open failed for write: %s", path);
            }
        }

        if (!sdOk && !usbOk && !flashOk) {
            log_e("[FS] writeFile: NO STORAGE AVAILABLE for: %s", path);
        } else {
            log_e("[FS] writeFile: FAILED on all backends for: %s", path);
        }
        return false;
    }

    bool removeFile(const char* path) {
        if (sdOk) {
            if (SD.remove(path)) {
                log_d("[FS] REMOVED from SD: %s", path);
                return true;
            }
            log_d("[FS] SD remove failed: %s → trying USB...", path);
        }

        if (usbOk) {
            bool ok = ch375b.fileErase(path);
            if (ok) {
                log_d("[FS] REMOVED from USB: %s", path);
                return true;
            }
            log_w("[FS] USB remove failed: %s → trying Flash...", path);
        }

        if (flashOk) {
            String flashPath = translateToFlashPath(path);
            if (LittleFS.remove(flashPath.c_str())) {
                log_d("[FS] REMOVED from Flash: %s → %s", path, flashPath.c_str());
                return true;
            }
            log_w("[FS] Flash remove failed: %s", path);
        }

        if (!sdOk && !usbOk && !flashOk) {
            log_e("[FS] removeFile: NO STORAGE AVAILABLE for: %s", path);
        }
        return false;
    }

    bool mkdir(const char* path) {
        log_d("[FS] MKDIR: %s (primary=%s)", path, getPrimaryStorage());

        if (sdOk) {
            bool ok = SD.mkdir(path);
            if (ok) {
                log_i("[FS] MKDIR OK on SD: %s", path);
                return true;
            }
            log_w("[FS] MKDIR failed on SD: %s → trying USB...", path);
        }

        if (usbOk) {
            bool ok = ch375b.dirCreate(path);
            if (ok) {
                log_i("[FS] MKDIR OK on USB (fallback): %s", path);
                return true;
            }
            log_e("[FS] MKDIR failed on USB: %s → trying Flash...", path);
        }

        if (flashOk) {
            String flashPath = translateToFlashPath(path);
            if (LittleFS.mkdir(flashPath.c_str())) {
                log_i("[FS] MKDIR OK on Flash: %s → %s", path, flashPath.c_str());
                return true;
            }
            log_e("[FS] MKDIR failed on Flash: %s", path);
        }

        if (!sdOk && !usbOk && !flashOk) {
            log_e("[FS] mkdir: NO STORAGE AVAILABLE! Cannot create: %s", path);
        } else if (sdOk && !usbOk && !flashOk) {
            log_e("[FS] mkdir: SD available but mkdir failed for: %s", path);
        } else if (!sdOk && usbOk && !flashOk) {
            log_e("[FS] mkdir: USB available but mkdir failed for: %s", path);
        } else if (!sdOk && !usbOk && flashOk) {
            log_e("[FS] mkdir: Flash available but mkdir failed for: %s", path);
        }

        return false;
    }

    bool rmdir(const char* path) {
        if (sdOk) {
            bool ok = SD.rmdir(path);
            if (ok) {
                log_d("[FS] RMDIR on SD: %s", path);
                return true;
            }
            log_w("[FS] rmdir failed on SD: %s", path);
        }

        // CH375B doesn't have direct rmdir — skip for now
        if (usbOk) {
            log_w("[FS] rmdir: CH375B does not support rmdir for: %s", path);
        }

        if (flashOk) {
            String flashPath = translateToFlashPath(path);
            if (LittleFS.rmdir(flashPath.c_str())) {
                log_d("[FS] RMDIR on Flash: %s → %s", path, flashPath.c_str());
                return true;
            }
            log_w("[FS] rmdir failed on Flash: %s", path);
        }

        if (!sdOk && !usbOk && !flashOk) {
            log_e("[FS] rmdir: NO STORAGE AVAILABLE for: %s", path);
        }
        return false;
    }

    // List directory entries.  Calls cb(name, isDir, size).
    void listDir(const char* dirPath,
                 std::function<void(const char*, bool, uint32_t)> cb) {
        log_d("[FS] LISTDIR: %s (primary=%s)", dirPath, getPrimaryStorage());

        if (sdOk) {
            log_d("[FS] Scanning SD directory: %s", dirPath);
            File dir = SD.open(dirPath);
            if (!dir) {
                log_w("[FS] Failed to open SD directory: %s", dirPath);
            } else if (!dir.isDirectory()) {
                log_w("[FS] Path is not a directory on SD: %s", dirPath);
                dir.close();
            } else {
                File entry;
                int count = 0;
                while ((entry = dir.openNextFile())) {
                    count++;
                    cb(entry.name(), entry.isDirectory(), entry.size());
                    entry.close();
                }
                dir.close();
                log_d("[FS] Found %d entries in SD: %s", count, dirPath);
                return;
            }
        }

        if (usbOk) {
            log_d("[FS] Scanning USB directory: %s", dirPath);
            int count = 0;
            ch375b.dirEnumerate(dirPath,
                [&](const char* name, uint32_t size) {
                    count++;
                    // CH375B doesn't easily expose directory attribute
                    // Assume files for now
                    cb(name, false, size);
                });
            log_d("[FS] Found %d entries in USB: %s", count, dirPath);
            return;
        }

        if (flashOk) {
            log_d("[FS] Scanning Flash directory: %s", dirPath);
            String flashPath = translateToFlashPath(dirPath);
            File dir = LittleFS.open(flashPath.c_str());
            if (!dir) {
                log_w("[FS] Failed to open Flash directory: %s → %s", dirPath, flashPath.c_str());
            } else if (!dir.isDirectory()) {
                log_w("[FS] Path is not a directory on Flash: %s → %s", dirPath, flashPath.c_str());
                dir.close();
            } else {
                File entry;
                int count = 0;
                while ((entry = dir.openNextFile())) {
                    count++;
                    cb(entry.name(), entry.isDirectory(), entry.size());
                    entry.close();
                }
                dir.close();
                log_d("[FS] Found %d entries in Flash: %s", count, dirPath);
                return;
            }
        }

        if (!sdOk && !usbOk && !flashOk) {
            log_w("[FS] listDir: NO STORAGE AVAILABLE for: %s", dirPath);
        }
    }
};

extern Storage storage;
