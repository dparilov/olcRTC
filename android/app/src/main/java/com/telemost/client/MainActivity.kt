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

        // Settings section
        var keyHex by remember { mutableStateOf(controller.getKeyHex()) }
        var socksPort by remember { mutableStateOf(controller.getSocksPort().toString()) }
        var showSettings by remember { mutableStateOf(false) }

        Button(onClick = { showSettings = !showSettings }) {
            Text(if (showSettings) "Hide Settings" else "Settings")
        }

        if (showSettings) {
            OutlinedTextField(
                value = keyHex,
                onValueChange = { keyHex = it },
                label = { Text("Encryption Key (hex)") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
            OutlinedTextField(
                value = socksPort,
                onValueChange = { socksPort = it },
                label = { Text("SOCKS Port") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
            Button(onClick = {
                controller.setKeyHex(keyHex)
                socksPort.toIntOrNull()?.let { controller.setSocksPort(it) }
            }) {
                Text("Save Settings")
            }
        }

        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { controller.launchFromClipboard() }) {
                Text("Launch tunnel")
            }
            Button(onClick = { controller.stopTunnel() }) {
                Text("Stop")
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
