package com.telemost.client

import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import android.net.VpnService
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.Switch
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
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
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp

private const val APP_VERSION = "2.0.0-alpha"

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
            val token = result.data?.getStringExtra(YandexLoginActivity.EXTRA_OAUTH_TOKEN) ?: ""
            if (token.isNotBlank()) {
                controller.setOAuthToken(token)
                controller.appendLog("[SSO] OAuth token received — registering v2...")
                // V2: auto-register with Yandex token
                Thread {
                    val ep = controller.getServerEndpoint()
                    controller.registerV2(ep, token) { success, msg ->
                        runOnUiThread {
                            Toast.makeText(this@MainActivity, msg, Toast.LENGTH_LONG).show()
                        }
                    }
                }.start()
            }
            if (cookies.isNotBlank()) {
                controller.setYandexCookies(cookies)
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val defaultHandler = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, ex ->
            try {
                val trace = ex.stackTraceToString().take(2000)
                val crashMsg = "[CRASH] ${ex.javaClass.simpleName}: ${ex.message}\n$trace"
                applicationContext.getSharedPreferences("olcrtc", MODE_PRIVATE)
                    .edit().putString("last_crash", crashMsg).commit()
                controller.appendLog(crashMsg)
                controller.uploadLogNow()
                Thread.sleep(3000)
            } catch (_: Throwable) {}
            defaultHandler?.uncaughtException(thread, ex)
        }
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
    val currentTenantId by controller.tenantIdFlow.collectAsState()
    var selectedTab by remember { mutableIntStateOf(0) }
    var advancedMode by remember { mutableStateOf(false) }
    var versionTapCount by remember { mutableIntStateOf(0) }
    val context = LocalContext.current
    val coroutineScope = rememberCoroutineScope()

    var masterSecret by remember { mutableStateOf(controller.getMasterSecret()) }
    var oauthToken by remember { mutableStateOf(controller.getOAuthToken()) }
    var serverEndpoint by remember { mutableStateOf(controller.getServerEndpoint()) }
    var roomUrl by remember { mutableStateOf(controller.getRoomUrl()) }
    var validationMsg by remember { mutableStateOf("") }

    // Tenant lifecycle state
    var tenantStatus by remember { mutableStateOf(
        if (controller.getTenantId().isNotBlank()) "created" else "not created"
    ) }
    var oauthStatus by remember { mutableStateOf("") }

    // Sync oauthToken from controller (after SSO login updates prefs)
    LaunchedEffect(logs) {
        val saved = controller.getOAuthToken()
        if (saved.isNotBlank() && saved != oauthToken) oauthToken = saved
    }

    LaunchedEffect(meeting) {
        val saved = controller.getRoomUrl()
        if (saved.isNotBlank()) roomUrl = saved
    }

    fun saveSettings(): Boolean {
        // v2: secret comes from server, not from UI field
        // v2: secret from server, no length check
        // v2: oauth token from server, do not overwrite
        // v2: secret from server, do not overwrite from UI state
        controller.setServerEndpoint(serverEndpoint)
        if (roomUrl.isNotBlank()) controller.setRoomUrl(roomUrl)
        validationMsg = ""
        return true
    }

    Column(modifier = Modifier.fillMaxSize().padding(top = 40.dp)) {
        TabRow(selectedTabIndex = selectedTab) {
            Tab(selected = selectedTab == 0, onClick = { selectedTab = 0 }) { Text("Control", modifier = Modifier.padding(14.dp)) }
            Tab(selected = selectedTab == 1, onClick = { selectedTab = 1 }) { Text("Settings", modifier = Modifier.padding(14.dp)) }
            Tab(selected = selectedTab == 2, onClick = { selectedTab = 2 }) { Text("Apps", modifier = Modifier.padding(14.dp)) }
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
                Text("SOCKS Port: ${controller.getSocksPort()}")
                val tenantId = currentTenantId
                if (tenantId.isNotBlank()) {
                    Text("Tenant: $tenantId", style = MaterialTheme.typography.bodySmall)
                }

                Text(
                    if (controller.getMasterSecret().isNotBlank()) "\u2713 Secret configured"
                    else "\u26A0 Login required",
                    style = MaterialTheme.typography.bodySmall
                )

                if (diagnostics.isNotBlank()) {
                    Text("Diagnostics: $diagnostics", style = MaterialTheme.typography.bodySmall)
                }

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

                Button(
                    onClick = { if (saveSettings()) controller.createAndLaunch() },
                    modifier = Modifier.fillMaxWidth()
                ) { Text("Create & Launch") }

                // VPN mode toggle — always visible
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

                // === STOP BUTTON (always visible) ===
                Button(
                    onClick = {
                        coroutineScope.launch(Dispatchers.IO) { controller.stopTunnel() }
                    },
                    modifier = Modifier.fillMaxWidth()
                ) { Text("Stop") }

                // === ADVANCED MODE CONTROLS ===
                if (advancedMode) {
                    Text("Advanced Controls", style = MaterialTheme.typography.titleSmall)

                    Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Button(onClick = { controller.rerunDiagnostics() }) { Text("Diagnostics") }
                    }

                    Text("Logs:", style = MaterialTheme.typography.titleSmall)
                    Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                        Button(onClick = { controller.clearLog() }) { Text("Clear") }
                    }
                    Text(logs, style = MaterialTheme.typography.bodySmall)
                }

                Spacer(modifier = Modifier.height(24.dp))

                Text(
                    text = "v$APP_VERSION" + if (advancedMode) " (advanced)" else "",
                    style = MaterialTheme.typography.bodySmall,
                    textAlign = TextAlign.Center,
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable {
                            if (!advancedMode) {
                                versionTapCount++
                                if (versionTapCount >= 7) {
                                    advancedMode = true
                                    Toast.makeText(context, "Advanced mode enabled", Toast.LENGTH_SHORT).show()
                                }
                            }
                        }
                        .padding(vertical = 12.dp)
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

                // --- Tenant Configuration ---
                Text("Tenant Configuration", style = MaterialTheme.typography.titleSmall)

                OutlinedTextField(
                    value = serverEndpoint,
                    onValueChange = { serverEndpoint = it },
                    label = { Text("Server Endpoint") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    placeholder = { Text("your-vps-ip") }
                )

                // Tenant status
                // currentTenantId is reactive via tenantIdFlow
                if (currentTenantId.isNotBlank()) {
                    Text("\u2713 Tenant: $currentTenantId", style = MaterialTheme.typography.bodySmall)
                    Text("  SOCKS Port: ${controller.getSocksPort()}", style = MaterialTheme.typography.bodySmall)
                    tenantStatus = "created"
                } else {
                    Text("\u26A0 Tenant: not created", style = MaterialTheme.typography.bodySmall)
                    tenantStatus = "not created"
                }

                // Create / Update Tenant button
                val tenantButtonEnabled = serverEndpoint.isNotBlank()
                Button(
                    onClick = {
                        controller.setServerEndpoint(serverEndpoint)
                        onLogin()
                    },
                    enabled = tenantButtonEnabled,
                    modifier = Modifier.fillMaxWidth()
                ) { Text("Login to Yandex / Create Tenant") }

                // --- OAuth Token (Tenant Override, maintenance only) ---
                if (advancedMode) {
                Spacer(modifier = Modifier.height(8.dp))
                Text("OAuth Token (Tenant Override)", style = MaterialTheme.typography.titleSmall)

                OutlinedTextField(
                    value = oauthToken,
                    onValueChange = { oauthToken = it },
                    label = { Text("OAuth Token") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true
                )

                if (oauthToken.isNotBlank()) {
                    Text("\u2713 OAuth token configured", style = MaterialTheme.typography.bodySmall)

                    // Send OAuth to server button (if tenant exists)
                    if (currentTenantId.isNotBlank()) {
                        Button(
                            onClick = {
                                // v2: oauth token from server, do not overwrite
                                val ep = controller.getServerEndpoint()
                                coroutineScope.launch(Dispatchers.IO) {
                                    try {
                                        val url = java.net.URL("$ep/tenant/oauth")
                                        val conn = url.openConnection() as java.net.HttpURLConnection
                                        conn.requestMethod = "POST"
                                        conn.setRequestProperty("Content-Type", "application/json")
                                        conn.connectTimeout = 15000
                                        conn.readTimeout = 15000
                                        conn.doOutput = true
                                        val body = org.json.JSONObject()
                                            .put("tenant_id", currentTenantId)
                                            .put("secret", masterSecret)
                                            .put("oauth_token", oauthToken)
                                            .toString()
                                        conn.outputStream.use { it.write(body.toByteArray()) }
                                        val code = conn.responseCode
                                        if (code in 200..299) {
                                            oauthStatus = "attached"
                                            validationMsg = "OAuth token sent to server and attached to tenant"
                                            controller.appendLog("[SETTINGS] OAuth attached to tenant $currentTenantId")
                                        } else {
                                            validationMsg = "OAuth attach failed: HTTP $code"
                                        }
                                    } catch (t: Throwable) {
                                        validationMsg = "OAuth attach error: ${t.message}"
                                    }
                                }
                            },
                            modifier = Modifier.fillMaxWidth()
                        ) { Text("Apply Token to Tenant") }

                        if (oauthStatus == "attached") {
                            Text("\u2713 OAuth token sent to server", style = MaterialTheme.typography.bodySmall)
                            Text("\u2713 OAuth attached to tenant", style = MaterialTheme.typography.bodySmall)
                        }
                    }
                } else {
                    Text("\u26A0 No OAuth token — Disk fallback disabled", style = MaterialTheme.typography.bodySmall)
                }

                // Apply button always visible in maintenance mode
                if (currentTenantId.isNotBlank()) {
                    Button(
                        onClick = {
                            // v2: oauth token from server, do not overwrite
                            val ep = controller.getServerEndpoint()
                            coroutineScope.launch(Dispatchers.IO) {
                                try {
                                    val url = java.net.URL("$ep/tenant/oauth")
                                    val conn = url.openConnection() as java.net.HttpURLConnection
                                    conn.requestMethod = "POST"
                                    conn.setRequestProperty("Content-Type", "application/json")
                                    conn.connectTimeout = 15000
                                    conn.readTimeout = 15000
                                    conn.doOutput = true
                                    val body = org.json.JSONObject()
                                        .put("tenant_id", currentTenantId)
                                        .put("secret", masterSecret)
                                        .put("oauth_token", oauthToken)
                                        .toString()
                                    conn.outputStream.use { it.write(body.toByteArray()) }
                                    val code = conn.responseCode
                                    if (code in 200..299) {
                                        oauthStatus = "attached"
                                        validationMsg = "OAuth token applied to tenant"
                                        controller.appendLog("[SETTINGS] OAuth applied to tenant $currentTenantId")
                                    } else {
                                        validationMsg = "OAuth apply failed: HTTP $code"
                                    }
                                } catch (t: Throwable) {
                                    validationMsg = "OAuth apply error: ${t.message}"
                                }
                            }
                        },
                        enabled = oauthToken.isNotBlank(),
                        modifier = Modifier.fillMaxWidth()
                    ) { Text("Apply Token to Tenant") }
                }

                } // end advancedMode OAuth section



                if (validationMsg.isNotBlank()) {
                    Spacer(modifier = Modifier.height(4.dp))
                    Text(validationMsg, color = MaterialTheme.colorScheme.primary,
                        style = MaterialTheme.typography.bodySmall)
                }
            }

            // === TAB 3: APPS ===
            2 -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(16.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(6.dp)
            ) {
                Text("VPN Apps", style = MaterialTheme.typography.headlineSmall)
                Text("Apps routing through VPN tunnel", style = MaterialTheme.typography.bodySmall)

                Spacer(modifier = Modifier.height(8.dp))

                var vpnAppsState by remember { mutableStateOf(controller.getVpnApps()) }
                var showAddDialog by remember { mutableStateOf(false) }
                val pm = context.packageManager

                // Show only apps in the allow list
                vpnAppsState.sorted().forEach { pkg ->
                    val appName = try {
                        pm.getApplicationLabel(pm.getApplicationInfo(pkg, 0)).toString()
                    } catch (_: Exception) { pkg }

                    Row(
                        modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = androidx.compose.ui.Alignment.CenterVertically
                    ) {
                        Text(appName, style = MaterialTheme.typography.bodyMedium,
                            modifier = Modifier.weight(1f))
                        // Remove button
                        Button(
                            onClick = {
                                controller.setVpnApp(pkg, false)
                                vpnAppsState = controller.getVpnApps()
                            }
                        ) { Text("X") }
                    }
                }

                Spacer(modifier = Modifier.height(8.dp))

                // Add app button
                Button(
                    onClick = { showAddDialog = true },
                    modifier = Modifier.fillMaxWidth()
                ) { Text("+ Add App") }

                // Add app dialog — show installed apps not yet in list
                if (showAddDialog) {
                    val allApps = remember {
                        pm.getInstalledApplications(0)
                            .filter { app ->
                                // Show user-installed apps + system apps with launcher activity
                                (app.flags and android.content.pm.ApplicationInfo.FLAG_SYSTEM == 0 ||
                                 pm.getLaunchIntentForPackage(app.packageName) != null) &&
                                app.packageName != context.packageName
                            }
                            .sortedWith(
                            compareBy<android.content.pm.ApplicationInfo> {
                                // User apps first (flag 0), then system apps (flag 1)
                                if (it.flags and android.content.pm.ApplicationInfo.FLAG_SYSTEM == 0) 0 else 1
                            }.thenBy { pm.getApplicationLabel(it).toString().lowercase() }
                        )
                    }

                    Spacer(modifier = Modifier.height(8.dp))
                    Text("Select app to add:", style = MaterialTheme.typography.titleSmall)

                    allApps.filter { it.packageName !in vpnAppsState }.forEach { app ->
                        val pkg = app.packageName
                        val name = pm.getApplicationLabel(app).toString()
                        Row(
                            modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                                .clickable {
                                    controller.setVpnApp(pkg, true)
                                    vpnAppsState = controller.getVpnApps()
                                    showAddDialog = false
                                },
                            verticalAlignment = androidx.compose.ui.Alignment.CenterVertically
                        ) {
                            Text("+ $name", style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.primary)
                        }
                    }

                    Button(
                        onClick = { showAddDialog = false },
                        modifier = Modifier.fillMaxWidth()
                    ) { Text("Cancel") }
                }
            }
        }
    }
}
