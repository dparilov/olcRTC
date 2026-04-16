package com.telemost.client

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
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
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            MaterialTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    val controller = remember { TelemostTunnelController(applicationContext) }
                    controller.handleIntent(intent)
                    MainScreen(controller)
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
private fun MainScreen(controller: TelemostTunnelController) {
    val status by controller.status.collectAsState()
    val meeting by controller.meeting.collectAsState()
    val diagnostics by controller.diagnostics.collectAsState()
    val logs by controller.logs.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp)
            .verticalScroll(rememberScrollState()),
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
        var showSettings by remember { mutableStateOf(controller.getMasterSecret().isBlank()) }
        var validationMsg by remember { mutableStateOf("") }

        // Secret status
        Text(
            if (masterSecret.isNotBlank()) "\u2713 Master secret configured"
            else "\u26A0 Master secret required — open Settings",
            style = MaterialTheme.typography.bodySmall
        )

        Button(onClick = { showSettings = !showSettings }) {
            Text(if (showSettings) "Hide Settings" else "Settings")
        }

        if (showSettings) {
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
                singleLine = true,
                visualTransformation = PasswordVisualTransformation()
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
        }

        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = {
                // Auto-save settings before launch
                if (masterSecret.isBlank()) { validationMsg = "Master secret is required"; return@Button }
                if (masterSecret.length < 8) { validationMsg = "Min 8 characters"; return@Button }
                socksPort.toIntOrNull()?.let { controller.setSocksPort(it) }
                controller.setOAuthToken(oauthToken)
                controller.setMasterSecret(masterSecret)
                if (roomUrl.isNotBlank()) controller.setRoomUrl(roomUrl)
                validationMsg = ""
                controller.launchFromClipboard()
            }) {
                Text("Launch tunnel")
            }
            Button(onClick = { controller.stopTunnel() }) {
                Text("Stop")
            }
            Button(onClick = {
                // Auto-save settings before publish
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

        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { controller.rerunDiagnostics() }) {
                Text("Run diagnostics again")
            }
            Button(onClick = { controller.copyLogToClipboard() }) {
                Text("Copy log")
            }
        }

        Text("Logs", style = MaterialTheme.typography.titleMedium)
        Text(logs)
    }
}
