package com.telemost.client

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import mobile.LogWriter
import mobile.Mobile

class TelemostTunnelController(private val appContext: Context) {
    private val prefs: SharedPreferences = try {
        val masterKey = MasterKey.Builder(appContext)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            appContext,
            "olcrtc_secure_prefs",
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
        )
    } catch (e: Exception) {
        // Fallback to regular prefs only if crypto init fails (should not happen)
        appContext.getSharedPreferences("olcrtc_prefs", Context.MODE_PRIVATE)
    }
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var diagnosticsJob: Job? = null
    private var lastHandledIntentPayload: String? = null
    private val _status = MutableStateFlow("Idle")
    private val _meeting = MutableStateFlow("No meeting link parsed yet")
    private val _diagnostics = MutableStateFlow("Diagnostics have not run yet")
    private val _logs = MutableStateFlow("Telemost Client v0.1\n")

    val status: StateFlow<String> = _status.asStateFlow()
    val meeting: StateFlow<String> = _meeting.asStateFlow()
    val diagnostics: StateFlow<String> = _diagnostics.asStateFlow()
    val logs: StateFlow<String> = _logs.asStateFlow()

    init {
        Mobile.touch()
        Mobile.setDebug(true)
        Mobile.setLogWriter(LogWriter { line -> appendLog(line) })
        _status.value = if (Mobile.isRunning()) "Running" else "Idle"
        appendLog("Controller initialized")
    }

    fun handleIntent(intent: Intent?) {
        val payload = extractIntentPayload(intent) ?: return
        if (payload == lastHandledIntentPayload) return
        lastHandledIntentPayload = payload

        val roomId = parseMeeting(payload)
        if (roomId != null) {
            _meeting.value = roomId
            appendLog("Meeting parsed from launch intent: $roomId")
        } else {
            appendLog("Launch intent did not contain a valid Telemost room")
        }
    }

    fun launchFromClipboard() {
        val roomId = resolveMeetingFromClipboardOrIntent()
        if (roomId == null) {
            _status.value = "Meeting link not found"
            _meeting.value = "Clipboard/intent does not contain a Telemost invite"
            appendLog("No valid Telemost link/id found in clipboard or launch intent")
            return
        }

        launchTunnel(roomId)
    }

    fun getKeyHex(): String = prefs.getString("key_hex", "") ?: ""

    fun setKeyHex(key: String) {
        prefs.edit().putString("key_hex", key).apply()
        appendLog("Encryption key updated")
    }

    fun getSocksPort(): Int = prefs.getInt("socks_port", DEFAULT_SOCKS_PORT)

    fun setSocksPort(port: Int) {
        prefs.edit().putInt("socks_port", port).apply()
        appendLog("SOCKS port updated to $port")
    }

    fun getOAuthToken(): String = prefs.getString("oauth_token", "") ?: ""

    fun setOAuthToken(token: String) {
        prefs.edit().putString("oauth_token", token).apply()
        appendLog("OAuth token updated")
    }

    fun getMasterSecret(): String = prefs.getString("master_secret", "") ?: ""

    fun setMasterSecret(secret: String) {
        prefs.edit().putString("master_secret", secret).apply()
        appendLog("Master secret updated")
    }

    fun publishRoomToDisk() {
        val token = getOAuthToken()
        val secret = getMasterSecret()
        val roomId = _meeting.value
        if (token.isBlank() || roomId.isBlank()) {
            appendLog("Cannot publish: OAuth token or room ID missing")
            return
        }
        if (secret.isBlank()) {
            appendLog("Cannot publish: Master secret required for signing room records")
            return
        }
        scope.launch {
            try {
                Mobile.publishRoomToDisk(token, secret, roomId, 3)
                appendLog("Room $roomId published to Yandex Disk")
                _status.value = "Published to Disk"
            } catch (t: Throwable) {
                appendLog("Disk publish failed: ${t.message}")
            }
        }
    }

    fun stopTunnel() {
        diagnosticsJob?.cancel()
        scope.launch {
            try {
                Mobile.stop()
                _status.value = "Stopped"
                _diagnostics.value = "Diagnostics stopped"
                appendLog("Tunnel stopped")
            } catch (t: Throwable) {
                _status.value = "Error"
                appendLog("Stop failed: ${t.message}")
            }
        }
    }

    fun rerunDiagnostics() {
        if (_status.value != "SOCKS ready") {
            _diagnostics.value = "Diagnostics skipped: tunnel not ready"
            appendLog("Diagnostics skipped: tunnel not ready (state=${_status.value})")
            return
        }
        diagnosticsJob?.cancel()
        _diagnostics.value = "Diagnostics running"
        appendLog("Diagnostics rerun requested")
        diagnosticsJob = scope.launch {
            runCatching { DiagnosticsRunner.runAll("127.0.0.1", DEFAULT_SOCKS_PORT) }
                .onSuccess {
                    if (_status.value == "SOCKS ready") {
                        _diagnostics.value = "Diagnostics finished"
                        appendLog(it)
                    } else {
                        _diagnostics.value = "Diagnostics deferred due to reconnect"
                        appendLog("Diagnostics results discarded: tunnel state changed to ${_status.value}")
                    }
                }
                .onFailure {
                    if (_status.value == "SOCKS ready") {
                        _diagnostics.value = "Diagnostics failed"
                        appendLog("Diagnostics failed: ${it.message}")
                    } else {
                        _diagnostics.value = "Diagnostics interrupted by reconnect"
                        appendLog("Diagnostics interrupted while state=${_status.value}: ${it.message}")
                    }
                }
        }
    }

    fun copyLogToClipboard() {
        val clipboard = appContext.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText("telemost-log", _logs.value))
        appendLog("Log copied to clipboard")
    }

    private fun launchTunnel(roomId: String) {
        _meeting.value = roomId
        _status.value = "Starting tunnel"
        appendLog("Launch requested for room=$roomId")

        scope.launch {
            try {
                val masterSecret = getMasterSecret()
                if (masterSecret.isBlank()) {
                    _status.value = "Error"
                    appendLog("Master secret is required. Configure it in Settings.")
                    return@launch
                }
                val keyHex = Mobile.deriveKeyFromSecret(masterSecret, roomId)
                val socksPort = getSocksPort()
                Mobile.start(roomId, keyHex, socksPort.toLong(), false, "", "")
                _status.value = "Connecting to Telemost"
                appendLog("Mobile.start completed, waiting ready")
                Mobile.waitReady(READY_TIMEOUT_MS)
                _status.value = "SOCKS ready"
                _diagnostics.value = "Diagnostics available (manual start recommended)"
                val port = getSocksPort()
                appendLog("Tunnel ready on local SOCKS port $port")
                scheduleReconnectWatchdog()
            } catch (t: Throwable) {
                _status.value = "Error"
                appendLog("Launch failed: ${t.message}")
            }
        }
    }

    private fun resolveMeetingFromClipboardOrIntent(): String? {
        val clipboard = appContext.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        val clip = clipboard.primaryClip
        val itemCount = clip?.itemCount ?: 0
        appendLog("Clipboard probe: items=$itemCount")

        val candidates = mutableListOf<String>()
        for (index in 0 until itemCount) {
            val item = clip?.getItemAt(index) ?: continue
            item.text?.toString()?.trim()?.takeIf { it.isNotBlank() }?.let { candidates += it }
            item.coerceToText(appContext)?.toString()?.trim()?.takeIf { it.isNotBlank() }?.let { candidates += it }
            item.htmlText?.trim()?.takeIf { it.isNotBlank() }?.let { candidates += it }
            item.uri?.toString()?.trim()?.takeIf { it.isNotBlank() }?.let { candidates += it }
            item.intent?.dataString?.trim()?.takeIf { it.isNotBlank() }?.let { candidates += it }
        }

        val uniqueCandidates = candidates.distinct()
        appendLog("Clipboard candidates: ${uniqueCandidates.size}")
        uniqueCandidates.forEachIndexed { index, value ->
            appendLog("Clipboard[$index]: ${value.take(200)}")
        }

        uniqueCandidates.firstNotNullOfOrNull { parseMeeting(it) }?.let { return it }

        val fallback = lastHandledIntentPayload?.let { parseMeeting(it) }
        if (fallback != null) {
            appendLog("Using meeting parsed from launch intent fallback")
        }
        return fallback
    }

    private fun extractIntentPayload(intent: Intent?): String? {
        val parts = listOfNotNull(
            intent?.dataString,
            intent?.getStringExtra(Intent.EXTRA_TEXT),
            intent?.getStringExtra(Intent.EXTRA_PROCESS_TEXT),
            intent?.clipData?.getItemAt(0)?.coerceToText(appContext)?.toString()
        )
            .map { it.trim() }
            .filter { it.isNotBlank() }

        return parts.firstOrNull()
    }

    private fun appendLog(line: String) {
        _logs.value += if (_logs.value.endsWith("\n")) "$line\n" else "\n$line\n"
    }

    private fun scheduleReconnectWatchdog() {
        scope.launch {
            while (Mobile.isRunning()) {
                delay(1500)
                val currentLogs = _logs.value
                if (currentLogs.contains("Reconnecting...")) {
                    if (_status.value != "Reconnecting") {
                        _status.value = "Reconnecting"
                        _diagnostics.value = "Diagnostics paused during reconnect"
                        appendLog("Controller detected reconnect state")
                    }
                }
                if (currentLogs.contains("Reconnected successfully")) {
                    if (_status.value == "Reconnecting") {
                        _status.value = "SOCKS ready"
                        _diagnostics.value = "Diagnostics available after reconnect"
                        appendLog("Controller detected successful reconnect")
                    }
                }
            }
        }
    }

    private fun parseMeeting(raw: String): String? {
        if (raw.isBlank()) return null
        val direct = raw.trim()
        if (ROOM_ID_REGEX.matches(direct)) return direct
        val match = TELEMOST_REGEX.find(direct) ?: return null
        return match.groupValues[1]
    }

    companion object {
        private val TELEMOST_REGEX = Regex("https://telemost\\.yandex(?:\\.ru|\\.com)/j/([A-Za-z0-9_-]+)")
        private val ROOM_ID_REGEX = Regex("[A-Za-z0-9_-]{6,}")
        private const val READY_TIMEOUT_MS = 30_000L
        private const val DEFAULT_SOCKS_PORT = 1080
        // No default key — must be derived from master secret or provided explicitly
        private const val DEFAULT_KEY_HEX = ""
    }
}
