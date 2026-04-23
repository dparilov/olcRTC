package com.telemost.client

import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile

class TraceTestActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Mobile.setDebug(true)
        Mobile.setLogWriter(mobile.LogWriter { line ->
            android.util.Log.d("OLCRTC-TRACE", line)
        })
        setContent {
            MaterialTheme {
                Surface(modifier = Modifier.fillMaxSize().systemBarsPadding()) {
                    TraceScreen()
                }
            }
        }
    }
}

@Composable
fun TraceScreen() {
    var roomId by remember { mutableStateOf("") }
    var secret by remember { mutableStateOf("Software@18") }
    var status by remember { mutableStateOf("Idle") }
    var log by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()

    fun appendLog(msg: String) {
        log = "$msg\n$log"
    }

    Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
        Text("olcRTC Trace Test", style = MaterialTheme.typography.headlineMedium)
        Spacer(Modifier.height(16.dp))

        OutlinedTextField(
            value = roomId,
            onValueChange = { roomId = it },
            label = { Text("Room ID (numbers only)") },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true
        )
        Spacer(Modifier.height(8.dp))

        OutlinedTextField(
            value = secret,
            onValueChange = { secret = it },
            label = { Text("Master Secret") },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true
        )
        Spacer(Modifier.height(16.dp))

        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(
                onClick = {
                    if (roomId.isBlank()) return@Button
                    status = "Connecting..."
                    scope.launch {
                        try {
                            val keyHex = Mobile.deriveKeyFromSecret(secret, roomId)
                            appendLog("Key derived, connecting to room $roomId")
                            withContext(Dispatchers.IO) {
                                Mobile.start(roomId, keyHex, 10950, false, "", "", "1.1.1.1:53")
                                appendLog("Mobile.start done, waiting ready...")
                                Mobile.waitReady(30000)
                            }
                            status = "Connected"
                            appendLog("SOCKS ready on port 10950")
                        } catch (e: Exception) {
                            status = "Error: ${e.message}"
                            appendLog("ERROR: ${e.message}")
                        }
                    }
                },
                enabled = status != "Connecting..."
            ) { Text("Connect") }

            Button(
                onClick = {
                    try {
                        Mobile.stop()
                        status = "Stopped"
                        appendLog("Stopped")
                    } catch (e: Exception) {
                        appendLog("Stop error: ${e.message}")
                    }
                }
            ) { Text("Stop") }
        }

        Spacer(Modifier.height(8.dp))
        Text("Status: $status", style = MaterialTheme.typography.bodyLarge)
        Spacer(Modifier.height(8.dp))
        Text(log, style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
    }
}
