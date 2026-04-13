package com.telemost.client

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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            MaterialTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    val controller = remember { TelemostTunnelController(applicationContext) }
                    MainScreen(controller)
                }
            }
        }
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
