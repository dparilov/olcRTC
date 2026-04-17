package com.telemost.client

import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Log
import mobile.Mobile

/**
 * VPN service that creates a TUN interface and routes all device traffic
 * through the olcRTC SOCKS5 tunnel via tun2socks.
 *
 * Flow: Apps → TUN → tun2socks (Go) → SOCKS5 (127.0.0.1:1080) → VP8 tunnel → VPS → Internet
 */
class TunnelVpnService : VpnService() {

    companion object {
        const val ACTION_START = "com.telemost.client.VPN_START"
        const val ACTION_STOP = "com.telemost.client.VPN_STOP"
        const val EXTRA_SOCKS_PORT = "socks_port"
        private const val TAG = "TunnelVpn"
        private var instance: TunnelVpnService? = null

        fun isRunning(): Boolean = instance != null
    }

    private var tunFd: ParcelFileDescriptor? = null

    override fun onCreate() {
        super.onCreate()
        instance = this
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                stopVpn()
                stopSelf()
                return START_NOT_STICKY
            }
            ACTION_START -> {
                val socksPort = intent.getIntExtra(EXTRA_SOCKS_PORT, 1080)
                startVpn(socksPort)
            }
        }
        return START_STICKY
    }

    private fun startVpn(socksPort: Int) {
        Log.i(TAG, "Starting VPN, SOCKS port=$socksPort")

        // Create TUN interface
        val builder = Builder()
            .setSession("olcRTC Tunnel")
            .addAddress("10.0.0.2", 24)
            .addRoute("0.0.0.0", 0)
            .addDnsServer("1.1.1.1")
            .addDnsServer("8.8.8.8")
            .setMtu(1500)
            .setBlocking(true)

        // Exclude our own app from VPN to prevent routing loops
        try {
            builder.addDisallowedApplication(packageName)
        } catch (e: Exception) {
            Log.w(TAG, "Could not exclude own app: ${e.message}")
        }

        tunFd = builder.establish()
        if (tunFd == null) {
            Log.e(TAG, "Failed to establish VPN")
            stopSelf()
            return
        }

        val fd = tunFd!!.fd
        Log.i(TAG, "VPN established, TUN fd=$fd")

        // Protect tunnel sockets from VPN routing
        Mobile.setProtector(object : mobile.SocketProtector {
            override fun protect(fd: Long): Boolean {
                return this@TunnelVpnService.protect(fd.toInt())
            }
        })

        // Start tun2socks in Go — routes TUN packets through SOCKS5
        Thread {
            try {
                Log.i(TAG, "Starting tun2socks fd=$fd socks=127.0.0.1:$socksPort")
                Mobile.startTun(fd.toLong(), socksPort.toLong())
                Log.i(TAG, "tun2socks stopped")
            } catch (e: Exception) {
                Log.e(TAG, "tun2socks error: ${e.message}")
            }
        }.start()
    }

    private fun stopVpn() {
        Log.i(TAG, "Stopping VPN")
        try {
            Mobile.stopTun()
        } catch (e: Exception) {
            Log.w(TAG, "stopTun error: ${e.message}")
        }
        tunFd?.close()
        tunFd = null
    }

    override fun onDestroy() {
        stopVpn()
        instance = null
        super.onDestroy()
    }

    override fun onRevoke() {
        stopVpn()
        stopSelf()
    }
}
