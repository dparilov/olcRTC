package com.telemost.client

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import android.net.VpnService
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.Switch
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
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
import androidx.compose.runtime.mutableIntStateOf
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
    var selectedTab by remember { mutableIntStateOf(0) }

    // Shared state for settings (persisted on action)
    var masterSecret by remember { mutableStateOf(controller.getMasterSecret()) }
    var oauthToken by remember { mutableStateOf(controller.getOAuthToken()) }
    var serverEndpoint by remember { mutableStateOf(controller.getServerEndpoint()) }
    var socksPort by remember { mutableStateOf(controller.getSocksPort().toString()) }
    var roomUrl by remember { mutableStateOf(controller.getRoomUrl()) }
    var validationMsg by remember { mutableStateOf("") }

    LaunchedEffect(meeting) {
        val saved = controller.getRoomUrl()
        if (saved.isNotBlank()) roomUrl = saved
    }

    // Helper to save all settings before actions
    fun saveSettings(): Boolean {
        if (masterSecret.isBlank()) { validationMsg = "Master secret is required"; return false }
        if (masterSecret.length < 8) { validationMsg = "Min 8 characters"; return false }
        socksPort.toIntOrNull()?.let { controller.setSocksPort(it) }
        controller.setOAuthToken(oauthToken)
        controller.setMasterSecret(masterSecret)
        controller.setServerEndpoint(serverEndpoint)
        if (roomUrl.isNotBlank()) controller.setRoomUrl(roomUrl)
        validationMsg = ""
        return true
    }

    Column(modifier = Modifier.fillMaxSize().padding(top = 40.dp)) {
        // Tab bar
        TabRow(selectedTabIndex = selectedTab) {
            Tab(selected = selectedTab == 0, onClick = { selectedTab = 0 }) { Text("Control", modifier = Modifier.padding(14.dp)) }
            Tab(selected = selectedTab == 1, onClick = { selectedTab = 1 }) { Text("Settings", modifier = Modifier.padding(14.dp)) }
            Tab(selected = selectedTab == 2, onClick = { selectedTab = 2 }) { Text("Logs", modifier = Modifier.padding(14.dp)) }
        }

        when (selectedTab) {
            // === TAB 1: CONTROL ===
            0 -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(16.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                Text("olcRTC", style = MaterialTheme.typography.headlineSmall)
                Text("Status: $status")
                Text("Room: $meeting")

                // Status indicators
                Text(
                    if (masterSecret.isNotBlank()) "\u2713 Secret configured"
                    else "\u26A0 Set Master Secret in Settings tab",
                    style = MaterialTheme.typography.bodySmall
                )
                Text(
                    if (controller.hasYandexCookies()) "\u2713 Yandex logged in"
                    else "\u26A0 Login required for room creation",
                    style = MaterialTheme.typography.bodySmall
                )

                // Room URL field (stays on Control tab per spec)
                OutlinedTextField(
                    value = roomUrl,
                    onValueChange = { roomUrl = it },
                    label = { Text("Room URL") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    placeholder = { Text("https://telemost.yandex.ru/j/...") }
                )
                if (validationMsg.isNotBlank()) {
                    Text(validationMsg, color = MaterialTheme.colorScheme.error)
                }

                // Row 1: Login + Create & Start
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = { onLogin() }) { Text("Login to Yandex") }
                    Button(onClick = {
                        if (saveSettings()) controller.createAndLaunch()
                    }) { Text("Create & Start") }
                }

                // Row 2: Start Server + Connect Client
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = {
                        if (saveSettings()) controller.startServer()
                    }) { Text("Start Server") }
                    Button(onClick = {
                        if (saveSettings()) controller.connectClient()
                    }) { Text("Connect Client") }
                }

                // Row 3: Stop + Diagnostics
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = { controller.stopTunnel() }) { Text("Stop") }
                    Button(onClick = { controller.rerunDiagnostics() }) { Text("Run Diagnostics") }
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
                                if (enabled) onVpnRequest() else controller.stopVpnService()
                            } catch (t: Throwable) {
                                controller.appendLog("[VPN] CRASH in toggle: ${t.javaClass.simpleName}: ${t.message}")
                            }
                        }
                    )
                }

                // Short log preview
                Text("Recent log:", style = MaterialTheme.typography.titleSmall)
                Text(
                    logs.lines().takeLast(10).joinToString("\n"),
                    style = MaterialTheme.typography.bodySmall
                )
            }

            // === TAB 2: SETTINGS ===
            1 -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(16.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                Text("Settings", style = MaterialTheme.typography.headlineSmall)

                Text("Security", style = MaterialTheme.typography.titleSmall)
                OutlinedTextField(
                    value = masterSecret,
                    onValueChange = { masterSecret = it; validationMsg = "" },
                    label = { Text("Master Secret (required)") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    visualTransformation = PasswordVisualTransformation()
                )

                Text("Server", style = MaterialTheme.typography.titleSmall)
                OutlinedTextField(
                    value = serverEndpoint,
                    onValueChange = { serverEndpoint = it },
                    label = { Text("Server Endpoint (optional)") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    placeholder = { Text("http://your-vps:8080") }
                )

                Text("Fallback", style = MaterialTheme.typography.titleSmall)
                OutlinedTextField(
                    value = oauthToken,
                    onValueChange = { oauthToken = it },
                    label = { Text("OAuth Token (for Disk fallback)") },
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

                if (validationMsg.isNotBlank()) {
                    Text(validationMsg, color = MaterialTheme.colorScheme.error)
                }

                Button(onClick = {
                    if (saveSettings()) validationMsg = "Settings saved"
                }, modifier = Modifier.fillMaxWidth()) {
                    Text("Save Settings")
                }

                Text("Settings are auto-saved when you press action buttons.", style = MaterialTheme.typography.bodySmall)
                Text("Diagnostics: $diagnostics", style = MaterialTheme.typography.bodySmall)
            }

            // === TAB 3: LOGS ===
            2 -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(16.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Text("Logs", style = MaterialTheme.typography.headlineSmall)
                    Button(onClick = { controller.clearLog() }) { Text("Clear") }
                }
                Text(logs, style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}
