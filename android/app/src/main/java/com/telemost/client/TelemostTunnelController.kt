package com.telemost.client

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import mobile.LogWriter
import mobile.Mobile

class TelemostTunnelController(private val appContext: Context) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var diagnosticsJob: Job? = null
    private val _status = MutableStateFlow("Idle")
    private val _meeting = MutableStateFlow("No clipboard link parsed yet")
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

    fun launchFromClipboard() {
        val clipboard = appContext.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        val text = clipboard.primaryClip?.getItemAt(0)?.coerceToText(appContext)?.toString()?.trim().orEmpty()
        val roomId = parseMeeting(text)
        if (roomId == null) {
            _status.value = "Clipboard link not found"
            _meeting.value = "Clipboard does not contain a Telemost invite"
            appendLog("No valid Telemost link/id in clipboard")
            return
        }

        _meeting.value = roomId
        _status.value = "Starting tunnel"
        appendLog("Launch requested for room=$roomId")

        scope.launch {
            try {
                Mobile.start(roomId, DEFAULT_KEY_HEX, DEFAULT_SOCKS_PORT.toLong(), false, "", "")
                _status.value = "Connecting to Telemost"
                appendLog("Mobile.start completed, waiting ready")
                Mobile.waitReady(READY_TIMEOUT_MS)
                _status.value = "SOCKS ready"
                _diagnostics.value = "Diagnostics available (manual start recommended)"
                appendLog("Tunnel ready on local SOCKS port $DEFAULT_SOCKS_PORT")
                scheduleReconnectWatchdog()
            } catch (t: Throwable) {
                _status.value = "Error"
                appendLog("Launch failed: ${t.message}")
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
        private const val DEFAULT_KEY_HEX = "d9d528926ca69ef9d422fcdd010cc27c8cd2c3ae37aa21927e2b3f8c59a921f3"
    }
}
