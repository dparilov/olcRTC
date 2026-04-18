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
        // Fail-closed: do NOT fall back to plaintext SharedPreferences.
        // Use a non-persistent in-memory map so the app runs but secrets
        // are not saved to disk in an insecure way. The user will need
        // to re-enter secrets on next launch.
        android.util.Log.e("olcRTC", "Secure storage unavailable: ${e.message}. Secrets will NOT be persisted.")
        object : SharedPreferences {
            private val map = mutableMapOf<String, Any?>()
            override fun getAll(): MutableMap<String, *> = map
            override fun getString(key: String?, defValue: String?): String? = map[key] as? String ?: defValue
            override fun getStringSet(key: String?, defValues: MutableSet<String>?): MutableSet<String>? = defValues
            override fun getInt(key: String?, defValue: Int): Int = map[key] as? Int ?: defValue
            override fun getLong(key: String?, defValue: Long): Long = defValue
            override fun getFloat(key: String?, defValue: Float): Float = defValue
            override fun getBoolean(key: String?, defValue: Boolean): Boolean = defValue
            override fun contains(key: String?): Boolean = map.containsKey(key)
            override fun edit(): SharedPreferences.Editor = object : SharedPreferences.Editor {
                override fun putString(key: String?, value: String?): SharedPreferences.Editor { if (key != null) map[key] = value; return this }
                override fun putStringSet(key: String?, values: MutableSet<String>?): SharedPreferences.Editor = this
                override fun putInt(key: String?, value: Int): SharedPreferences.Editor { if (key != null) map[key] = value; return this }
                override fun putLong(key: String?, value: Long): SharedPreferences.Editor = this
                override fun putFloat(key: String?, value: Float): SharedPreferences.Editor = this
                override fun putBoolean(key: String?, value: Boolean): SharedPreferences.Editor = this
                override fun remove(key: String?): SharedPreferences.Editor { map.remove(key); return this }
                override fun clear(): SharedPreferences.Editor { map.clear(); return this }
                override fun commit(): Boolean = true
                override fun apply() {}
            }
            override fun registerOnSharedPreferenceChangeListener(listener: SharedPreferences.OnSharedPreferenceChangeListener?) {}
            override fun unregisterOnSharedPreferenceChangeListener(listener: SharedPreferences.OnSharedPreferenceChangeListener?) {}
        }
    }
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var diagnosticsJob: Job? = null
    private var logUploadJob: Job? = null
    private var lastHandledIntentPayload: String? = null
    private val _status = MutableStateFlow("Idle")
    private val _meeting = MutableStateFlow("No meeting link parsed yet")
    private val _diagnostics = MutableStateFlow("Diagnostics have not run yet")
    private val _logs = MutableStateFlow("")
    private val _tenantId = MutableStateFlow("")

    val status: StateFlow<String> = _status.asStateFlow()
    val meeting: StateFlow<String> = _meeting.asStateFlow()
    val diagnostics: StateFlow<String> = _diagnostics.asStateFlow()
    val logs: StateFlow<String> = _logs.asStateFlow()
    val tenantIdFlow: StateFlow<String> = _tenantId.asStateFlow()

    init {
        // Reset transient state on each app start (only secrets+cookies persist)
        prefs.edit().putBoolean("vpn_mode", false).putString("room_url", "").apply()
        Mobile.touch()
        Mobile.setDebug(true)
        Mobile.setLogWriter(LogWriter { line -> appendLog(line) })
        _status.value = if (Mobile.isRunning()) "Running" else "Idle"
        _tenantId.value = prefs.getString("tenant_id", "") ?: ""
        val versionName = try {
            appContext.packageManager.getPackageInfo(appContext.packageName, 0).versionName
        } catch (_: Exception) { "?" }
        appendLog("Telemost Client v$versionName initialized")
        // Start periodic log upload to Yandex Disk (every 60s)
        logUploadJob = scope.launch {
            while (true) {
                delay(10_000) // 10s for crash diagnostics (normally 60s)
                try { sendLogToDisk() } catch (_: Exception) {}
            }
        }
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
        // Use saved room URL from settings (not clipboard)
        val savedRoom = getRoomUrl()
        val roomId = if (savedRoom.isNotBlank()) parseMeeting(savedRoom) else null
        if (roomId == null) {
            _status.value = "Room URL not set"
            appendLog("Enter Telemost room URL in the Room URL field and use Create & Start")
            return
        }
        _meeting.value = roomId
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

    fun getYandexCookies(): String = prefs.getString("yandex_cookies", "") ?: ""

    fun setYandexCookies(cookies: String) {
        prefs.edit().putString("yandex_cookies", cookies).apply()
        appendLog("Yandex cookies saved (${cookies.length} chars)")
    }

    fun hasYandexCookies(): Boolean = getYandexCookies().isNotBlank()

    fun getRoomUrl(): String = prefs.getString("room_url", "") ?: ""

    fun getServerEndpoint(): String {
        var ep = prefs.getString("server_endpoint", "") ?: ""
        ep = ep.trim()
        if (ep.isNotBlank() && !ep.startsWith("http://") && !ep.startsWith("https://")) {
            ep = "http://$ep"
        }
        // Add default port if none specified (check after ://)
        val hostPart = ep.substringAfter("://", "")
        if (ep.isNotBlank() && hostPart.isNotBlank() && ':' !in hostPart) {
            ep = "$ep:8080"
        }
        return ep
    }
    fun setServerEndpoint(endpoint: String) {
        prefs.edit().putString("server_endpoint", endpoint).apply()
    }

    /**
     * Register tenant with bootstrap server.
     * Sends secret to POST /tenant/register, receives tenant_id + socks_port.
     * Idempotent: same secret = same tenant.
     */
    private fun registerTenant(endpoint: String, secret: String): Boolean {
        try {
            val url = java.net.URL("$endpoint/tenant/register")
            val conn = url.openConnection() as java.net.HttpURLConnection
            conn.requestMethod = "POST"
            conn.setRequestProperty("Content-Type", "application/json")
            conn.connectTimeout = 15_000
            conn.readTimeout = 15_000
            conn.doOutput = true
            val body = org.json.JSONObject().put("secret", secret).toString()
            conn.outputStream.use { it.write(body.toByteArray()) }

            val code = conn.responseCode
            val resp = try {
                conn.inputStream.bufferedReader().readText()
            } catch (_: Exception) {
                conn.errorStream?.bufferedReader()?.readText() ?: ""
            }

            if (code in 200..299) {
                val json = org.json.JSONObject(resp)
                val tenantId = json.optString("tenant_id", "")
                val socksPort = json.optInt("socks_port", 0)
                val apiPort = json.optInt("api_port", 0)
                val diskPath = json.optString("disk_path", "")
                val fallback = json.optBoolean("fallback_enabled", false)

                if (socksPort > 0) setSocksPort(socksPort)
                prefs.edit()
                    .putString("tenant_id", tenantId)
                _tenantId.value = tenantId
                    .putInt("tenant_api_port", apiPort)
                    .putString("tenant_disk_path", diskPath)
                    .putBoolean("tenant_fallback", fallback)
                    .apply()

                appendLog("[TENANT] Registered: id=$tenantId port=$socksPort fallback=$fallback")
                return true
            } else {
                val json = try { org.json.JSONObject(resp) } catch (_: Exception) { null }
                val msg = json?.optString("message", resp) ?: resp
                appendLog("[TENANT] Registration failed: $code $msg")
                return false
            }
        } catch (t: Throwable) {
            appendLog("[TENANT] Registration error: ${t.message}")
            return false
        }
    }

    fun getTenantId(): String = prefs.getString("tenant_id", "") ?: ""

    /**
     * Send signed room intent to server via direct API.
     * Returns record_id on success, null on failure.
     */
    private fun sendRoomIntent(endpoint: String, intentJson: String): String? {
        try {
            val url = java.net.URL("$endpoint/api/room-intent")
            val conn = url.openConnection() as java.net.HttpURLConnection
            conn.requestMethod = "POST"
            conn.setRequestProperty("Content-Type", "application/json")
            conn.connectTimeout = 15_000
            conn.readTimeout = 15_000
            conn.doOutput = true
            conn.outputStream.use { it.write(intentJson.toByteArray()) }

            val code = conn.responseCode
            val body = try {
                conn.inputStream.bufferedReader().readText()
            } catch (_: Exception) {
                conn.errorStream?.bufferedReader()?.readText() ?: ""
            }

            appendLog("[API] POST /api/room-intent -> $code")
            if (code in 200..299) {
                val json = org.json.JSONObject(body)
                val status = json.optString("status", "")
                val recordId = json.optString("record_id", "")
                val assignedPort = json.optInt("socks_port", 0)
                if (assignedPort > 0) {
                    setSocksPort(assignedPort)
                    appendLog("[API] Server assigned SOCKS port: $assignedPort")
                }
                appendLog("[API] Server: status=$status record_id=${recordId.take(8)}")
                return recordId
            } else {
                val json = try { org.json.JSONObject(body) } catch (_: Exception) { null }
                val msg = json?.optString("message", body) ?: body
                appendLog("[API] Server rejected: $code $msg")
                return null
            }
        } catch (t: Throwable) {
            appendLog("[API] Direct API failed: ${t.message}")
            return null
        }
    }

    /**
     * Poll room intent status from server.
     * Returns status string (accepted/starting/ready/failed/unknown).
     */
    private fun pollIntentStatus(endpoint: String, recordId: String): String {
        try {
            val url = java.net.URL("$endpoint/api/room-intent/$recordId")
            val conn = url.openConnection() as java.net.HttpURLConnection
            conn.requestMethod = "GET"
            conn.connectTimeout = 10_000
            conn.readTimeout = 10_000

            val body = conn.inputStream.bufferedReader().readText()
            val json = org.json.JSONObject(body)
            return json.optString("status", "unknown")
        } catch (t: Throwable) {
            appendLog("[API] Poll failed: ${t.message}")
            return "unknown"
        }
    }

    fun setRoomUrl(url: String) {
        prefs.edit().putString("room_url", url).apply()
        val roomId = parseMeeting(url)
        if (roomId != null) {
            _meeting.value = roomId
            appendLog("Room saved: $roomId")
        }
    }

    /**
     * One-button flow: create room with cookies -> publish to Disk -> launch tunnel.
     */
    fun createAndLaunch() {
        val cookies = getYandexCookies()
        val token = getOAuthToken()
        val secret = getMasterSecret()
        if (cookies.isBlank()) {
            appendLog("Cannot create room: Yandex cookies missing. Tap 'Login to Yandex' first.")
            _status.value = "Login required"
            return
        }
        if (secret.isBlank()) {
            appendLog("Cannot launch: Master secret required")
            _status.value = "Secret required"
            return
        }
        scope.launch {
            try {
                _status.value = "Creating room..."
                appendLog("Creating Telemost room via cookies...")
                val roomUri = Mobile.createRoom(cookies)
                appendLog("Room created: $roomUri")

                // Extract room ID (digits after /j/)
                val roomId = parseMeeting(roomUri) ?: roomUri
                _meeting.value = roomId
                // Save room URL so Launch Manual works after Stop
                setRoomUrl("https://telemost.yandex.ru/j/$roomId")

                // Deliver room intent: API first, Disk fallback
                val endpoint = getServerEndpoint()
                val intentJson = Mobile.buildSignedRoomIntent(secret, roomId, 3L)
                var intentDelivered = false

                if (endpoint.isNotBlank()) {
                    // Auto-register tenant if not yet registered
                    // Always re-register tenant (idempotent — same secret = same tenant)
                    appendLog("Registering tenant...")
                    _status.value = "Registering tenant..."
                    registerTenant(endpoint, secret)
                    appendLog("Sending room intent to $endpoint...")
                    _status.value = "Sending intent to server..."
                    val recordId = sendRoomIntent(endpoint, intentJson)
                    if (recordId != null) {
                        appendLog("Intent accepted, waiting for server ready...")
                        _status.value = "Intent accepted, server starting..."
                        // Poll for ready/failed (up to 30s)
                        var finalStatus = "timeout"
                        for (i in 1..15) {
                            kotlinx.coroutines.delay(2000)
                            val status = pollIntentStatus(endpoint, recordId)
                            appendLog("[API] Poll #$i: $status")
                            if (status == "ready") {
                                finalStatus = "ready"
                                break
                            }
                            if (status == "failed") {
                                finalStatus = "failed"
                                break
                            }
                        }
                        when (finalStatus) {
                            "ready" -> {
                                appendLog("Server ready")
                                _status.value = "Server ready"
                                intentDelivered = true
                            }
                            "failed" -> {
                                appendLog("Server failed to start room — trying fallback...")
                            }
                            else -> {
                                appendLog("Server did not reach ready within 30s — trying fallback...")
                            }
                        }
                    } else {
                        appendLog("Direct server path unavailable")
                    }
                }

                // Fallback: Disk publish if API failed/timed out
                if (!intentDelivered && token.isNotBlank()) {
                    appendLog("Fallback delivery via Yandex Disk...")
                    _status.value = "Fallback delivery via Yandex Disk..."
                    mobile.Mobile.publishRoomToDisk(token, secret, roomId, 3)
                    appendLog("Room $roomId published to Disk")
                    intentDelivered = true
                } else if (!intentDelivered) {
                    appendLog("No delivery path: set Server Endpoint or OAuth Token")
                }

                // Launch tunnel only if delivery succeeded
                if (intentDelivered) {
                    launchTunnel(roomId)
                } else {
                    _status.value = "No delivery path — tunnel not started"
                    appendLog("ERROR: Cannot start tunnel without successful server delivery")
                }
            } catch (t: Throwable) {
                appendLog("Create & launch failed: ${t.message}")
                _status.value = "Error: ${t.message}"
            }
        }
    }

        fun publishRoomToDisk() {
        val token = getOAuthToken()
        val secret = getMasterSecret()
        // Try current meeting first, then saved room URL
        var roomId = _meeting.value
        if (roomId.isBlank() || roomId == "No meeting link parsed yet") {
            val saved = getRoomUrl()
            val parsed = parseMeeting(saved)
            if (parsed != null) {
                roomId = parsed
                _meeting.value = roomId
            }
        }
        if (token.isBlank()) {
            appendLog("Cannot publish: OAuth token missing. Save it in Settings first.")
            return
        }
        if (roomId.isBlank() || roomId == "No meeting link parsed yet") {
            appendLog("Cannot publish: Room ID missing. Paste a Telemost link or enter in Settings.")
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
                if (!isTunnelReady()) _status.value = "Published to Disk"
            } catch (t: Throwable) {
                appendLog("Disk publish failed: ${t.message}")
            }
        }
    }

    /**
     * Start Server: send room intent to server without starting local tunnel.
     * Uses current room from Room URL / parsed meeting.
     */
    fun startServer() {
        val secret = getMasterSecret()
        if (secret.isBlank()) {
            appendLog("Cannot start server: Master secret required")
            _status.value = "Secret required"
            return
        }
        var roomId = _meeting.value
        if (roomId.isBlank() || roomId == "No meeting link parsed yet") {
            val saved = getRoomUrl()
            val parsed = parseMeeting(saved)
            if (parsed != null) { roomId = parsed; _meeting.value = roomId }
        }
        if (roomId.isBlank() || roomId == "No meeting link parsed yet") {
            appendLog("Cannot start server: Room URL required")
            _status.value = "Room URL required"
            return
        }
        scope.launch {
            try {
                val intentJson = Mobile.buildSignedRoomIntent(secret, roomId, 3L)
                val endpoint = getServerEndpoint()
                var delivered = false

                if (endpoint.isNotBlank()) {
                    _status.value = "Sending intent to server..."
                    val recordId = sendRoomIntent(endpoint, intentJson)
                    if (recordId != null) {
                        appendLog("Intent accepted, server starting...")
                        _status.value = "Intent accepted, server starting..."
                        // Poll for ready/failed (up to 30s)
                        for (i in 1..15) {
                            kotlinx.coroutines.delay(2000)
                            val status = pollIntentStatus(endpoint, recordId)
                            appendLog("[API] Poll #$i: $status")
                            if (status == "ready") {
                                appendLog("Server ready")
                                _status.value = "Server ready"
                                delivered = true
                                break
                            }
                            if (status == "failed") {
                                appendLog("Server failed to start room")
                                break
                            }
                        }
                        if (!delivered) {
                            appendLog("Server did not reach ready — trying fallback...")
                        }
                    } else {
                        appendLog("Direct server path unavailable")
                    }
                }

                if (!delivered) {
                    val token = getOAuthToken()
                    if (token.isNotBlank()) {
                        _status.value = "Fallback delivery via Yandex Disk..."
                        Mobile.publishRoomToDisk(token, secret, roomId, 3)
                        appendLog("Room $roomId published to Disk (fallback)")
                        _status.value = "Published to Disk"
                    } else {
                        appendLog("No delivery path: set Server Endpoint or OAuth Token")
                        _status.value = "No delivery path available"
                    }
                }
            } catch (t: Throwable) {
                appendLog("Start server failed: ${t.message}")
                _status.value = "Error: ${t.message}"
            }
        }
    }

    /**
     * Connect Client: start local tunnel using current room + derived key.
     * Does not send server bootstrap intent.
     */
    fun connectClient() {
        val secret = getMasterSecret()
        if (secret.isBlank()) {
            appendLog("Cannot connect: Master secret required")
            _status.value = "Secret required"
            return
        }
        var roomId = _meeting.value
        if (roomId.isBlank() || roomId == "No meeting link parsed yet") {
            val saved = getRoomUrl()
            val parsed = parseMeeting(saved)
            if (parsed != null) { roomId = parsed; _meeting.value = roomId }
        }
        if (roomId.isBlank() || roomId == "No meeting link parsed yet") {
            appendLog("Cannot connect: Room URL required")
            _status.value = "Room URL required"
            return
        }
        scope.launch {
            try {
                launchTunnel(roomId)
            } catch (t: Throwable) {
                appendLog("Connect client failed: ${t.message}")
                _status.value = "Error: ${t.message}"
            }
        }
    }

    fun isVpnMode(): Boolean = prefs.getBoolean("vpn_mode", false)

    fun setVpnMode(enabled: Boolean) {
        prefs.edit().putBoolean("vpn_mode", enabled).apply()
        appendLog("VPN mode: ${if (enabled) "ON (all traffic)" else "OFF (SOCKS only)"}")
    }

    fun startVpnService() {
        TunnelVpnService.logCallback = { msg -> appendLog(msg) }
        try {
            val port = getSocksPort()
            val intent = Intent(appContext, TunnelVpnService::class.java).apply {
                action = TunnelVpnService.ACTION_START
                putExtra(TunnelVpnService.EXTRA_SOCKS_PORT, port)
            }
            if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.O) {
                appContext.startForegroundService(intent)
            } else {
                appContext.startService(intent)
            }
            appendLog("VPN service started (port=$port)")
        } catch (t: Throwable) {
            appendLog("[VPN] ERROR starting service: ${t.message}")
        }
    }

    fun stopVpnService() {
        val intent = Intent(appContext, TunnelVpnService::class.java).apply {
            action = TunnelVpnService.ACTION_STOP
        }
        appContext.startService(intent)
        appendLog("VPN service stopped")
    }

    fun stopTunnel() {
        // Stop VPN if active
        stopVpnService()
        // Stop foreground service
        try {
            appContext.stopService(Intent(appContext, TunnelForegroundService::class.java))
        } catch (_: Exception) {}
        // Cancel periodic upload and do final upload
        logUploadJob?.cancel()
        sendLogToDisk()
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

    fun isTunnelReady(): Boolean {
        val s = _status.value
        return s == "SOCKS ready" || s.startsWith("Connected") || Mobile.isRunning()
    }

    fun rerunDiagnostics() {
        if (!isTunnelReady()) {
            _diagnostics.value = "Diagnostics skipped: tunnel not ready"
            appendLog("Diagnostics skipped: tunnel not ready (state=${_status.value})")
            return
        }
        diagnosticsJob?.cancel()
        _diagnostics.value = "Diagnostics running"
        appendLog("Diagnostics requested")
        diagnosticsJob = scope.launch {
            runCatching { DiagnosticsRunner.runAll("127.0.0.1", getSocksPort()) }
                .onSuccess {
                    if (isTunnelReady()) {
                        appendLog(it)
                        // Extract IP from diagnostics and update status
                        val ipMatch = Regex("External IP: (.+)").find(it)
                        if (ipMatch != null) {
                            val ip = ipMatch.groupValues[1]
                            _status.value = "Connected — IP: $ip"
                            _diagnostics.value = "OK — IP: $ip"
                        } else {
                            _diagnostics.value = "Finished (IP not detected)"
                        }
                    } else {
                        _diagnostics.value = "Diagnostics deferred due to reconnect"
                        appendLog("Diagnostics results discarded: tunnel state changed to ${_status.value}")
                    }
                }
                .onFailure {
                    if (isTunnelReady()) {
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

    fun clearLog() {
        _logs.value = "Log cleared\n"
    }

    fun uploadLogNow() {
        try { sendLogToDisk() } catch (_: Throwable) {}
    }

    fun sendLogToDisk() {
        val token = getOAuthToken()
        if (token.isBlank()) {
            appendLog("Cannot send log: OAuth token missing")
            return
        }
        scope.launch {
            try {
                val logContent = try { java.io.File(appContext.filesDir, "olcrtc-log.txt").readText() } catch (_: Exception) { _logs.value }
                val timestamp = java.text.SimpleDateFormat("yyyyMMdd-HHmmss", java.util.Locale.US).format(java.util.Date())
                val filename = "olcrtc-android-log-$timestamp.txt"
                appendLog("Uploading log to Yandex Disk ($filename)...")
                
                val urlConn = java.net.URL("https://cloud-api.yandex.net/v1/disk/resources/upload?path=app%3A%2Folcrtc%2F$filename&overwrite=true")
                    .openConnection() as java.net.HttpURLConnection
                urlConn.setRequestProperty("Authorization", "OAuth $token")
                val uploadUrl = org.json.JSONObject(urlConn.inputStream.bufferedReader().readText()).getString("href")
                urlConn.disconnect()
                
                val putConn = java.net.URL(uploadUrl).openConnection() as java.net.HttpURLConnection
                putConn.requestMethod = "PUT"
                putConn.setRequestProperty("Content-Type", "text/plain")
                putConn.doOutput = true
                putConn.outputStream.write(logContent.toByteArray())
                val code = putConn.responseCode
                putConn.disconnect()
                
                if (code in 200..201) {
                    appendLog("Log uploaded: $filename")
                } else {
                    appendLog("Log upload failed: HTTP $code")
                }
            } catch (t: Throwable) {
                appendLog("Log upload error: ${t.message}")
            }
        }
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
                    appendLog("Master secret is required. Open Settings, enter it, and press Save Settings.")
                    return@launch
                }
                val keyHex = Mobile.deriveKeyFromSecret(masterSecret, roomId)
                val socksPort = getSocksPort()
                // Stop any previous tunnel before starting new one
                if (Mobile.isRunning()) {
                    appendLog("Stopping previous tunnel before new start")
                    try { Mobile.stop() } catch (_: Exception) {}
                    Thread.sleep(1000)
                }
                Mobile.start(roomId, keyHex, socksPort.toLong(), false, "", "1.1.1.1:53")
                _status.value = "Connecting to Telemost"
                appendLog("Mobile.start completed, waiting ready")
                // Start foreground service to keep connection alive
                val serviceIntent = Intent(appContext, TunnelForegroundService::class.java)
                serviceIntent.putExtra("status", "Tunnel active: room $roomId")
                if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.O) {
                    appContext.startForegroundService(serviceIntent)
                } else {
                    appContext.startService(serviceIntent)
                }
                Mobile.waitReady(READY_TIMEOUT_MS)
                _status.value = "SOCKS ready"
                _diagnostics.value = "Diagnostics available (manual start recommended)"
                val port = getSocksPort()
                appendLog("Tunnel ready on local SOCKS port $port")
                // VPN mode is NOT auto-started — user must toggle manually each session
                // Detect external IP through the tunnel (retry — server may still be connecting to SFU)
                scope.launch {
                    for (attempt in 1..5) {
                        try {
                            val proxy = java.net.Proxy(java.net.Proxy.Type.SOCKS, java.net.InetSocketAddress("127.0.0.1", port))
                            val conn = java.net.URL("https://ifconfig.me").openConnection(proxy) as java.net.HttpURLConnection
                            conn.connectTimeout = 15000
                            conn.readTimeout = 15000
                            conn.setRequestProperty("User-Agent", "curl/7.0")
                            conn.setRequestProperty("Accept", "text/plain")
                            val ip = conn.inputStream.bufferedReader().readText().trim()
                            conn.disconnect()
                            _status.value = "Connected — IP: $ip"
                            appendLog("External IP: $ip")
                            return@launch
                        } catch (t: Throwable) {
                            appendLog("IP detection attempt $attempt/5: ${t.message}")
                            if (attempt < 5) kotlinx.coroutines.delay(3000L)
                        }
                    }
                    appendLog("IP detection failed after 5 attempts")
                }
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
            return fallback
        }

        // Final fallback: use saved room URL from settings
        val savedRoom = getRoomUrl()
        if (savedRoom.isNotBlank()) {
            val parsed = parseMeeting(savedRoom)
            if (parsed != null) {
                appendLog("Using saved room URL: $parsed")
                return parsed
            }
        }
        return null
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

    fun appendLog(line: String) {
        _logs.value += if (_logs.value.endsWith("\n")) "$line\n" else "\n$line\n"
        // Also persist to file for reliable upload
        try {
            val f = java.io.File(appContext.filesDir, "olcrtc-log.txt")
            f.appendText("$line\n")
        } catch (_: Exception) {}
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
        // Unified room ID contract: Telemost room IDs are always numeric
        private val TELEMOST_REGEX = Regex("https://telemost\\.yandex(?:\\.ru|\\.com)/j/(\\d+)")
        private val ROOM_ID_REGEX = Regex("^\\d+$")
        private const val READY_TIMEOUT_MS = 30_000L
        private const val DEFAULT_SOCKS_PORT = 1080
        // No default key — must be derived from master secret or provided explicitly
        private const val DEFAULT_KEY_HEX = ""
    }

    /**
     * Auto-send OAuth token to bootstrap server for tenant attachment.
     * Called automatically after SSO login completes.
     */
    fun sendOAuthToServer() {
        val token = getOAuthToken()
        val tenantId = getTenantId()
        val secret = getMasterSecret()
        val endpoint = getServerEndpoint()
        if (token.isBlank() || tenantId.isBlank() || secret.isBlank() || endpoint.isBlank()) {
            appendLog("[SSO] Cannot auto-attach OAuth: missing token/tenant/secret/endpoint")
            return
        }
        scope.launch {
            try {
                val url = java.net.URL("$endpoint/tenant/oauth")
                val conn = url.openConnection() as java.net.HttpURLConnection
                conn.requestMethod = "POST"
                conn.setRequestProperty("Content-Type", "application/json")
                conn.connectTimeout = 15000
                conn.readTimeout = 15000
                conn.doOutput = true
                val body = org.json.JSONObject()
                    .put("tenant_id", tenantId)
                    .put("secret", secret)
                    .put("oauth_token", token)
                    .toString()
                conn.outputStream.use { it.write(body.toByteArray()) }
                val code = conn.responseCode
                if (code in 200..299) {
                    appendLog("[SSO] OAuth token auto-sent to server and attached to tenant $tenantId")
                } else {
                    appendLog("[SSO] OAuth auto-attach failed: HTTP $code")
                }
            } catch (t: Throwable) {
                appendLog("[SSO] OAuth auto-attach error: ${t.message}")
            }
        }
    }
}
