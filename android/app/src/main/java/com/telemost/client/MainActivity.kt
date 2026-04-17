package com.telemost.client

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import android.net.VpnService
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.Switch
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.OutlinedTextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import kotlinx.coroutines.launch
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp

class MainActivity : ComponentActivity() {
    private val controller by lazy { TelemostTunnelController(applicationContext) }

    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            controller.startVpnService()
        }
    }

    private val loginLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            val cookies = result.data?.getStringExtra(YandexLoginActivity.EXTRA_COOKIES) ?: ""
            if (cookies.isNotBlank()) {
                controller.setYandexCookies(cookies)
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Global crash catcher — saves to prefs (survives crash) + triggers immediate upload
        val defaultHandler = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, ex ->
            try {
                val trace = ex.stackTraceToString().take(2000)
                val crashMsg = "[CRASH] ${ex.javaClass.simpleName}: ${ex.message}\n$trace"
                // Save to prefs so we can show it on next launch
                applicationContext.getSharedPreferences("olcrtc", MODE_PRIVATE)
                    .edit().putString("last_crash", crashMsg).commit()
                controller.appendLog(crashMsg)
                controller.uploadLogNow()
                Thread.sleep(3000)
            } catch (_: Throwable) {}
            defaultHandler?.uncaughtException(thread, ex)
        }
        // Show previous crash if any
        val lastCrash = applicationContext.getSharedPreferences("olcrtc", MODE_PRIVATE)
            .getString("last_crash", null)
        if (lastCrash != null) {
            controller.appendLog("=== PREVIOUS CRASH ===\n$lastCrash\n=== END CRASH ===")
            applicationContext.getSharedPreferences("olcrtc", MODE_PRIVATE)
                .edit().remove("last_crash").apply()
        }
        setContent {
            MaterialTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    controller.handleIntent(this@MainActivity.intent)
                    MainScreen(controller, onLogin = {
                        loginLauncher.launch(Intent(this@MainActivity, YandexLoginActivity::class.java))
                    }, onVpnRequest = {
                        try {
                            controller.appendLog("[VPN] onVpnRequest: calling VpnService.prepare()")
                            val prepareIntent = VpnService.prepare(this@MainActivity)
                            if (prepareIntent != null) {
                                controller.appendLog("[VPN] prepare() returned intent — launching permission dialog")
                                vpnPermissionLauncher.launch(prepareIntent)
                            } else {
                                controller.appendLog("[VPN] prepare() = null — already granted, starting service")
                                controller.startVpnService()
                            }
                        } catch (t: Throwable) {
                            controller.appendLog("[VPN] CRASH in onVpnRequest: ${t.javaClass.simpleName}: ${t.message}")
                        }
                    })
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
    }
}

@Composable
private fun MainScreen(controller: TelemostTunnelController, onLogin: () -> Unit = {}, onVpnRequest: () -> Unit = {}) {
    val status by controller.status.collectAsState()
    val meeting by controller.meeting.collectAsState()
    val diagnostics by controller.diagnostics.collectAsState()
    val logs by controller.logs.collectAsState()
    val scrollState = rememberScrollState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp)
            .verticalScroll(scrollState),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Text("Telemost Client", style = MaterialTheme.typography.headlineSmall)
        Text("Status: $status")
        Text("Meeting: $meeting")
        Text("Diagnostics: $diagnostics")
        Text(OlcRtcProbe.probe())

        // Settings section — auto-show on first install if no master secret
        var socksPort by remember { mutableStateOf(controller.getSocksPort().toString()) }
        var oauthToken by remember { mutableStateOf(controller.getOAuthToken()) }
        var masterSecret by remember { mutableStateOf(controller.getMasterSecret()) }
        var roomUrl by remember { mutableStateOf(controller.getRoomUrl()) }
        var validationMsg by remember { mutableStateOf("") }

        // Sync roomUrl from controller when meeting changes (after Create & Launch)
        LaunchedEffect(meeting) {
            val saved = controller.getRoomUrl()
            if (saved.isNotBlank()) roomUrl = saved
        }

        // Secret status
        Text(
            if (masterSecret.isNotBlank()) "\u2713 Master secret configured"
            else "\u26A0 Master secret required — open Settings",
            style = MaterialTheme.typography.bodySmall
        )


            Text("Security", style = MaterialTheme.typography.titleSmall)
            OutlinedTextField(
                value = masterSecret,
                onValueChange = { masterSecret = it; validationMsg = "" },
                label = { Text("Master Secret (required)") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
                visualTransformation = PasswordVisualTransformation()
            )
            OutlinedTextField(
                value = oauthToken,
                onValueChange = { oauthToken = it },
                label = { Text("OAuth Token (optional, for publishing)") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
            Text("Connection", style = MaterialTheme.typography.titleSmall)
            OutlinedTextField(
                value = socksPort,
                onValueChange = { socksPort = it },
                label = { Text("SOCKS Port") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
            Text("Telemost", style = MaterialTheme.typography.titleSmall)
            OutlinedTextField(
                value = roomUrl,
                onValueChange = { roomUrl = it },
                label = { Text("Room URL or link (optional)") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
                placeholder = { Text("https://telemost.yandex.ru/j/...") }
            )
            if (validationMsg.isNotBlank()) {
                Text(validationMsg, color = MaterialTheme.colorScheme.error)
            }
            // Settings auto-save on Launch/Publish — no separate Save button needed

        // Yandex Login status
        Text(
            if (controller.hasYandexCookies()) "\u2713 Yandex logged in"
            else "\u26A0 Yandex login required for room creation",
            style = MaterialTheme.typography.bodySmall
        )

        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { onLogin() }) {
                Text("Login to Yandex")
            }
            Button(onClick = {
                // Auto-save settings, then create room + publish + launch
                if (masterSecret.isBlank()) { validationMsg = "Master secret is required"; return@Button }
                if (masterSecret.length < 8) { validationMsg = "Min 8 characters"; return@Button }
                socksPort.toIntOrNull()?.let { controller.setSocksPort(it) }
                controller.setOAuthToken(oauthToken)
                controller.setMasterSecret(masterSecret)
                validationMsg = ""
                controller.createAndLaunch()
            }) {
                Text("Create & Launch")
            }
            Button(onClick = { controller.stopTunnel() }) {
                Text("Stop")
            }
        }

        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = {
                // Manual launch with existing room URL
                if (masterSecret.isBlank()) { validationMsg = "Master secret is required"; return@Button }
                if (masterSecret.length < 8) { validationMsg = "Min 8 characters"; return@Button }
                socksPort.toIntOrNull()?.let { controller.setSocksPort(it) }
                controller.setOAuthToken(oauthToken)
                controller.setMasterSecret(masterSecret)
                if (roomUrl.isNotBlank()) controller.setRoomUrl(roomUrl)
                validationMsg = ""
                controller.launchFromClipboard()
            }) {
                Text("Launch (manual)")
            }
            Button(onClick = {
                if (masterSecret.isBlank()) { validationMsg = "Master secret is required"; return@Button }
                if (masterSecret.length < 8) { validationMsg = "Min 8 characters"; return@Button }
                socksPort.toIntOrNull()?.let { controller.setSocksPort(it) }
                controller.setOAuthToken(oauthToken)
                controller.setMasterSecret(masterSecret)
                if (roomUrl.isNotBlank()) controller.setRoomUrl(roomUrl)
                validationMsg = ""
                controller.publishRoomToDisk()
            }) {
                Text("Publish")
            }
        }

        // VPN mode toggle
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = androidx.compose.ui.Alignment.CenterVertically
        ) {
            Text("VPN mode (all traffic)")
            Switch(
                checked = controller.isVpnMode(),
                enabled = status.contains("SOCKS ready") || status.startsWith("Connected") || status == "Running",
                onCheckedChange = { enabled ->
                    try {
                        controller.setVpnMode(enabled)
                        if (enabled) {
                            onVpnRequest()
                        } else {
                            controller.stopVpnService()
                        }
                    } catch (t: Throwable) {
                        controller.appendLog("[VPN] CRASH in toggle: ${t.javaClass.simpleName}: ${t.message}")
                    }
                }
            )
        }

        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { controller.rerunDiagnostics() }) {
                Text("Run Diagnostics")
            }
            Button(onClick = { controller.clearLog() }) {
                Text("Clear Log")
            }
        }

        Text("Logs", style = MaterialTheme.typography.titleMedium)
        Text(logs, style = MaterialTheme.typography.bodySmall)
    }
}
