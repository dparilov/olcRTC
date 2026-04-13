package com.telemost.client

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.InetSocketAddress
import java.net.Proxy
import java.net.Socket
import java.net.URL
import javax.net.ssl.HttpsURLConnection

object DiagnosticsRunner {
    suspend fun runAll(socksHost: String, socksPort: Int): String = withContext(Dispatchers.IO) {
        val results = mutableListOf<String>()
        results += "== Tunnel diagnostics =="
        results += runCatching { externalIp(socksHost, socksPort) }
            .fold(
                onSuccess = { "External IP: $it" },
                onFailure = { "External IP check failed: ${it.message}" }
            )

        val targets = listOf(
            "https://example.com",
            "https://cloudflare.com",
            "https://ifconfig.me/all.json"
        )
        for (target in targets) {
            results += runCatching { httpsCheck(target, socksHost, socksPort) }
                .fold(
                    onSuccess = { "$target -> $it" },
                    onFailure = { "$target -> FAILED: ${it.message}" }
                )
        }

        results += runCatching { tcpCheck("1.1.1.1", 443, socksHost, socksPort) }
            .fold(
                onSuccess = { "TCP 1.1.1.1:443 -> OK (${it}ms)" },
                onFailure = { "TCP 1.1.1.1:443 -> FAILED: ${it.message}" }
            )

        results += ""
        results += "== Speedtest probes =="
        results += SpeedtestRunner.runAll(socksHost, socksPort)

        results.joinToString("\n")
    }

    private fun proxy(host: String, port: Int): Proxy = Proxy(Proxy.Type.SOCKS, InetSocketAddress(host, port))

    private fun externalIp(host: String, port: Int): String {
        val url = URL("https://ifconfig.me")
        val conn = (url.openConnection(proxy(host, port)) as HttpsURLConnection).apply {
            connectTimeout = 10_000
            readTimeout = 10_000
            requestMethod = "GET"
        }
        conn.inputStream.use { stream ->
            return BufferedReader(InputStreamReader(stream)).readText().trim()
        }
    }

    private fun httpsCheck(target: String, host: String, port: Int): String {
        val url = URL(target)
        val started = System.currentTimeMillis()
        val conn = (url.openConnection(proxy(host, port)) as HttpsURLConnection).apply {
            connectTimeout = 10_000
            readTimeout = 10_000
            requestMethod = "GET"
        }
        conn.inputStream.use { it.readNBytes(64) }
        val elapsed = System.currentTimeMillis() - started
        return "HTTP ${conn.responseCode}, ${elapsed}ms"
    }

    private fun tcpCheck(targetHost: String, targetPort: Int, host: String, port: Int): Long {
        val started = System.currentTimeMillis()
        Socket(proxy(host, port)).use { socket ->
            socket.connect(InetSocketAddress(targetHost, targetPort), 10_000)
        }
        return System.currentTimeMillis() - started
    }
}
