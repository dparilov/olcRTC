package com.telemost.client

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.net.InetSocketAddress
import java.net.Proxy
import java.net.URL
import javax.net.ssl.HttpsURLConnection

object SpeedtestRunner {
    suspend fun runAll(socksHost: String, socksPort: Int): String = withContext(Dispatchers.IO) {
        val results = mutableListOf<String>()
        results += runCatching { probe("Yandex speedtest", "https://yandex.ru/internet") }
            .fold(
                onSuccess = { "Yandex speedtest probe: $it" },
                onFailure = { "Yandex speedtest probe failed: ${it.message}" }
            )
        results += runCatching { probe("Ookla speedtest", "https://www.speedtest.net") }
            .fold(
                onSuccess = { "Ookla speedtest probe: $it" },
                onFailure = { "Ookla speedtest probe failed: ${it.message}" }
            )
        results.joinToString("\n")
    }

    private fun proxy(host: String, port: Int): Proxy = Proxy(Proxy.Type.SOCKS, InetSocketAddress(host, port))

    private fun probe(name: String, target: String): String {
        val started = System.currentTimeMillis()
        val url = URL(target)
        val conn = (url.openConnection(proxy("127.0.0.1", 1080)) as HttpsURLConnection).apply {
            connectTimeout = 15_000
            readTimeout = 15_000
            requestMethod = "GET"
            instanceFollowRedirects = true
        }
        conn.inputStream.use { it.readNBytes(128) }
        val elapsed = System.currentTimeMillis() - started
        return "HTTP ${conn.responseCode}, ${elapsed}ms"
    }
}
